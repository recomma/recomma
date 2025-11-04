package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/util/workqueue"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/rs/cors"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/cmd/recomma/internal/config"
	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/engine"
	"github.com/recomma/recomma/engine/orderscaler"
	"github.com/recomma/recomma/filltracker"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/internal/debugmode"
	"github.com/recomma/recomma/internal/origin"
	"github.com/recomma/recomma/internal/vault"
	rlog "github.com/recomma/recomma/log"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/recomma/recomma/webui"
)

func fatal(msg string, err error) {
	slog.Error(msg, slog.String("error", err.Error()))
	os.Exit(1)
}

func main() {
	cfg := config.DefaultConfig()
	fs := config.NewConfigFlagSet(&cfg)

	if err := fs.Parse(os.Args[1:]); err != nil {
		fatal("parsing flags failed", err)
	}

	if err := config.ApplyEnvDefaults(fs, &cfg); err != nil {
		fatal("invalid parameters", err)
	}

	if err := config.ValidateConfig(cfg); err != nil {
		fatal("invalid configuration", err)
	}

	if cfg.Debug && !debugmode.Available() {
		fatal("debug mode unavailable", debugmode.ErrUnavailable)
	}

	debugEnabled := cfg.Debug && debugmode.Available()

	allowedOrigins := origin.BuildAllowedOrigins(cfg.HTTPListen, cfg.PublicOrigin)
	rpID := origin.DeriveRPID(cfg.HTTPListen, cfg.PublicOrigin)

	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerCtx, cancelWorkers := context.WithCancel(context.Background())

	logger := slog.New(config.GetLogHandler(cfg))
	slog.SetDefault(logger)
	log.SetOutput(slog.NewLogLogger(logger.Handler(), slog.LevelDebug).Writer())

	webui.SetDebug(debugEnabled)

	appCtx = rlog.ContextWithLogger(appCtx, logger)

	streamController := api.NewStreamController(api.WithStreamLogger(logger))

	store, err := storage.New(cfg.StoragePath, storage.WithStreamPublisher(streamController))
	if err != nil {
		fatal("storage init failed", err)
	}
	defer store.Close()

	webAuth, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Recomma",
		RPID:          rpID,
		RPOrigins:     allowedOrigins,
	})
	if err != nil {
		fatal("webauth init failed", err)
	}

	initialVaultState := vault.StateSetupRequired
	var controllerOpts []vault.ControllerOption

	if debugEnabled {
		secrets, err := debugmode.LoadSecretsFromEnv()
		if err != nil {
			fatal("load debug secrets", err)
		}
		now := secrets.ReceivedAt
		if now.IsZero() {
			now = time.Now().UTC()
		}
		controllerOpts = append(controllerOpts,
			vault.WithInitialSecrets(secrets),
			vault.WithInitialUser(debugmode.DebugUser(now)),
			vault.WithInitialTimestamps(nil, &now, nil),
		)
		initialVaultState = vault.StateUnsealed
	} else {
		existingUser, err := store.GetVaultUser(appCtx)
		if err != nil {
			fatal("load vault user", err)
		}
		if existingUser != nil {
			controllerOpts = append(controllerOpts, vault.WithInitialUser(existingUser))

			payload, err := store.GetVaultPayloadForUser(appCtx, existingUser.ID)
			if err != nil {
				fatal("load vault payload", err)
			}
			if payload != nil {
				initialVaultState = vault.StateSealed
				sealedAt := payload.UpdatedAt
				controllerOpts = append(controllerOpts, vault.WithInitialTimestamps(&sealedAt, nil, nil))
			}
		}
	}

	vaultController := vault.NewController(initialVaultState, controllerOpts...)
	webAuthApi, err := api.NewWebAuthnService(api.WebAuthnServiceConfig{
		WebAuthn: webAuth,
		Store:    store,
		Logger:   logger,
	})
	if err != nil {
		fatal("webauth api init failed", err)
	}

	apiHandler := api.NewHandler(store, streamController,
		api.WithLogger(logger),
		api.WithWebAuthnService(webAuthApi),
		api.WithVaultController(vaultController),
		api.WithOrderScalerMaxMultiplier(cfg.OrderScalerMaxMultiplier),
		api.WithDebugMode(debugEnabled),
	)

	strictServer := api.NewStrictHandler(apiHandler, []api.StrictMiddlewareFunc{
		api.RequestContextMiddleware(),
	})

	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins: allowedOrigins,
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowedHeaders: []string{"*"},
	})

	apiMux := http.NewServeMux()
	api.HandlerWithOptions(strictServer, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
	})

	apiHandlerWithCORS := corsMiddleware.Handler(apiMux)
	tlsEnabled := false
	webHandler := webui.Handler(cfg.HTTPListen, cfg.PublicOrigin, tlsEnabled)

	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", apiHandlerWithCORS)
	rootMux.Handle("/api", apiHandlerWithCORS)
	rootMux.Handle("/sse/", apiHandlerWithCORS)
	rootMux.Handle("/sse", apiHandlerWithCORS)
	rootMux.Handle("/webauthn/", apiHandlerWithCORS)
	rootMux.Handle("/vault/", apiHandlerWithCORS)
	rootMux.Handle("/vault", apiHandlerWithCORS)
	rootMux.Handle("/", webHandler)

	apiSrv := &http.Server{
		Addr:    cfg.HTTPListen,
		Handler: rootMux,
	}

	apiErrCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP API listening", slog.String("addr", apiSrv.Addr), slog.String("public_origin", cfg.PublicOrigin))
		if err := apiSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			apiErrCh <- err
		}
	}()

	logger.Debug("Waiting for vault to be unsealed")
	unsealedCh := make(chan error, 1)
	go func() {
		unsealedCh <- vaultController.WaitUntilUnsealed(appCtx)
	}()

	select {
	case err := <-apiErrCh:
		if err != nil {
			logger.Error("HTTP server failed before unseal", slog.String("error", err.Error()))
			os.Exit(1)
		}
		logger.Info("HTTP server closed before unseal")
		os.Exit(0)
	case <-appCtx.Done():
		logger.Warn("context cancelled while waiting for vault unseal", slog.String("error", appCtx.Err().Error()))
		os.Exit(0)
	case err := <-unsealedCh:
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("vaultController WaitUntilUnsealed returned an error", slog.String("error", err.Error()))
			}
			os.Exit(0)
		}
	}

	secrets := vaultController.Secrets()

	if secrets == nil {
		fatal("vault secrets unavailable", errors.New("vault secrets unavailable"))
	}

	client, err := tc.New3CommasClient(tc.ClientConfig{
		APIKey:     secrets.Secrets.THREECOMMASAPIKEY,
		PrivatePEM: []byte(secrets.Secrets.THREECOMMASPRIVATEKEY),
	},
		tc.WithRequestEditorFn(func(ctx context.Context, r *http.Request) error {
			slog.Default().WithGroup("threecommas").Debug("sending", "method", r.Method, "url", r.URL.String())
			return nil
		}))
	if err != nil {
		fatal("3commas client init failed", err)
	}

	fillTracker := filltracker.New(store, logger)

	rlOrders := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[recomma.OrderWork](1*time.Second, 30*time.Second),
	)
	oqCfg := workqueue.TypedRateLimitingQueueConfig[recomma.OrderWork]{Name: "orders"}
	oq := workqueue.NewTypedRateLimitingQueueWithConfig(rlOrders, oqCfg)
	engineEmitter := emitter.NewQueueEmitter(oq)

	primaryHyperliquid, ok := secrets.Secrets.PrimaryVenueByType("hyperliquid")
	if !ok {
		fatal("locate primary hyperliquid venue", errors.New("no primary hyperliquid venue configured"))
	}

	primaryVenueID := strings.TrimSpace(primaryHyperliquid.ID)
	if primaryVenueID == "" {
		fatal("load hyperliquid configuration", errors.New("primary hyperliquid venue missing identifier"))
	}
	primaryWallet := strings.TrimSpace(primaryHyperliquid.Wallet)
	if primaryWallet == "" {
		fatal("load hyperliquid configuration", errors.New("primary hyperliquid venue missing wallet"))
	}
	primaryAPIURL := strings.TrimSpace(primaryHyperliquid.APIURL)
	if primaryAPIURL == "" {
		fatal("load hyperliquid configuration", errors.New("primary hyperliquid venue missing api_url"))
	}

	defaultIdentifier := storage.DefaultHyperliquidIdentifier(orderid.OrderId{})
	defaultHyperliquidIdent := defaultIdentifier.VenueID
	defaultHyperliquidWallet := defaultIdentifier.Wallet

	defaultVenueWallet := primaryWallet
	if shouldUseSentinelDefaultHyperliquidWallet(
		secrets.Secrets.Venues,
		recomma.VenueID(primaryVenueID),
		primaryWallet,
		defaultHyperliquidIdent,
	) {
		defaultVenueWallet = ""
	}
	if err := store.EnsureDefaultVenueWallet(appCtx, defaultVenueWallet); err != nil {
		fatal("update default venue wallet", err)
	}

	defaultAliasWallet := strings.TrimSpace(defaultVenueWallet)
	if defaultAliasWallet == "" {
		defaultAliasWallet = defaultHyperliquidWallet
	}

	defaultVenueConfigured := false
	for _, venue := range secrets.Secrets.Venues {
		if recomma.VenueID(strings.TrimSpace(venue.ID)) == defaultHyperliquidIdent {
			defaultVenueConfigured = true
			break
		}
	}

	primaryIdent := recomma.VenueID(primaryVenueID)
	shouldBootstrapDefaultWs := !defaultVenueConfigured && defaultHyperliquidIdent != "" && defaultHyperliquidIdent != primaryIdent && defaultAliasWallet != defaultHyperliquidWallet

	statusClients := make(hl.StatusClientRegistry)
	wsClients := make(map[recomma.VenueID]*ws.Client)
	var venueOrder []recomma.VenueID
	var venueClosers []func()

	emitterLogger := logger.WithGroup("hyperliquid").WithGroup("emitter")
	priceLogger := logger.WithGroup("hyperliquid").WithGroup("prices")
	runtimeLogger := logger.WithGroup("hyperliquid")

	var constraintsInfo *hl.Info

	for _, venue := range secrets.Secrets.Venues {
		if !strings.EqualFold(venue.Type, "hyperliquid") {
			runtimeLogger.Warn("unsupported venue type in secrets", slog.String("venue", venue.ID), slog.String("type", venue.Type))
			continue
		}

		venueID := strings.TrimSpace(venue.ID)
		if venueID == "" {
			fatal("load hyperliquid configuration", errors.New("hyperliquid venue missing identifier"))
		}

		wallet := strings.TrimSpace(venue.Wallet)
		if wallet == "" {
			fatal("load hyperliquid configuration", errors.New("hyperliquid venue missing wallet"))
		}

		displayName := strings.TrimSpace(venue.DisplayName)
		if displayName == "" {
			displayName = venueID
		}

		privateKey := strings.TrimSpace(venue.PrivateKey)
		if privateKey == "" {
			fatal("load hyperliquid configuration", errors.New("hyperliquid venue missing private key"))
		}

		apiURL := strings.TrimSpace(venue.APIURL)
		if apiURL == "" {
			apiURL = primaryAPIURL
		}

		venueIdent := recomma.VenueID(venueID)

		payload := api.VenueUpsertRequest{
			Type:        "hyperliquid",
			DisplayName: displayName,
			Wallet:      wallet,
		}
		if flags := decorateVenueFlags(venue.Flags, venue.Primary); flags != nil {
			payload.Flags = &flags
		}

		if _, err := store.UpsertVenue(appCtx, venueID, payload); err != nil {
			fatal("persist venue configuration", err)
		}

		exchange, err := hl.NewExchange(appCtx, hl.ClientConfig{
			BaseURL: apiURL,
			Wallet:  wallet,
			Key:     privateKey,
		})
		if err != nil {
			fatal("create hyperliquid exchange", err)
		}

		info := hl.NewInfo(appCtx, hl.ClientConfig{
			BaseURL: apiURL,
			Wallet:  wallet,
		})
		registerHyperliquidStatusClient(statusClients, info, venueIdent, primaryIdent, defaultHyperliquidIdent)
		if constraintsInfo == nil || venueIdent == primaryIdent {
			constraintsInfo = info
		}

		wsClient, err := ws.New(appCtx, store, fillTracker, venueIdent, wallet, apiURL)
		if err != nil {
			fatal("create hyperliquid websocket", err)
		}
		registerHyperliquidWsClient(wsClients, wsClient, venueIdent)
		venueOrder = append(venueOrder, venueIdent)

		client := wsClient
		closeVenue := func() {
			if err := client.Close(); err != nil {
				runtimeLogger.Debug("websocket close failed", slog.String("venue", venueID), slog.String("error", err.Error()))
			}
		}
		venueClosers = append(venueClosers, closeVenue)

		submitter := emitter.NewHyperLiquidEmitter(exchange, venueIdent, wsClient, store,
			emitter.WithHyperLiquidEmitterConfig(emitter.HyperLiquidEmitterConfig{
				InitialIOCOffsetBps: cfg.HyperliquidIOCInitialOffsetBps,
			}),
			emitter.WithHyperLiquidEmitterLogger(emitterLogger.With(slog.String("venue", venueID), slog.String("wallet", wallet))),
		)

		if shouldBootstrapDefaultWs && venueIdent == primaryIdent {
			aliasClient, err := ws.New(appCtx, store, fillTracker, defaultHyperliquidIdent, defaultAliasWallet, apiURL)
			if err != nil {
				fatal("create default hyperliquid websocket", err)
			}
			registerHyperliquidWsClient(wsClients, aliasClient, defaultHyperliquidIdent)
			submitter.RegisterWsClient(defaultHyperliquidIdent, aliasClient)
			statusClients[defaultHyperliquidIdent] = info

			alias := aliasClient
			venueClosers = append(venueClosers, func() {
				if err := alias.Close(); err != nil {
					runtimeLogger.Debug("websocket close failed", slog.String("venue", string(defaultHyperliquidIdent)), slog.String("error", err.Error()))
				}
			})

			runtimeLogger.Info("default hyperliquid alias configured",
				slog.String("venue", string(defaultHyperliquidIdent)),
				slog.String("wallet", defaultAliasWallet),
				slog.String("api_url", apiURL),
			)
		}

		registerHyperliquidEmitter(engineEmitter, submitter, venueIdent, primaryIdent, defaultHyperliquidIdent)

		runtimeLogger.Info("hyperliquid venue configured",
			slog.String("venue", venueID),
			slog.String("wallet", wallet),
			slog.String("api_url", apiURL),
			slog.Bool("primary", venue.Primary),
		)
	}

	if len(statusClients) == 0 {
		fatal("load hyperliquid configuration", errors.New("no hyperliquid venues configured"))
	}

	defer func() {
		for _, closeFn := range venueClosers {
			closeFn()
		}
	}()

	if _, ok := statusClients[primaryIdent]; !ok {
		fatal("load hyperliquid configuration", errors.New("primary hyperliquid venue missing from configuration"))
	}
	if constraintsInfo == nil {
		fatal("load hyperliquid configuration", errors.New("unable to resolve constraints info client"))
	}

	constraints := hl.NewOrderIdCache(constraintsInfo)
	scaler := orderscaler.New(store, constraints, logger, orderscaler.WithMaxMultiplier(cfg.OrderScalerMaxMultiplier))

	statusRefresher := hl.NewStatusRefresher(statusClients, store,
		hl.WithStatusRefresherLogger(logger),
		hl.WithStatusRefresherTracker(fillTracker),
	)
	if err := statusRefresher.Refresh(appCtx); err != nil {
		logger.Warn("status refresher failed", slog.String("error", err.Error()))
	}

	if err := fillTracker.Rebuild(appCtx); err != nil {
		logger.Warn("fill tracker rebuild failed", slog.String("error", err.Error()))
	}

	priceSource := newPriceSourceMultiplexer(priceLogger, primaryIdent, venueOrder, wsClients)
	api.WithHyperliquidPriceSource(priceSource)(apiHandler)

	logger.Info("Service ready",
		slog.Int("hyperliquid_venues", len(statusClients)),
		slog.String("primary_venue", string(primaryIdent)),
	)

	// Queue + workers
	// Q creation (typed)
	rl := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[engine.WorkKey](1*time.Second, 30*time.Second), // backoff on failures
	)

	config := workqueue.TypedRateLimitingQueueConfig[engine.WorkKey]{
		Name: "deals",
	}

	q := workqueue.NewTypedRateLimitingQueueWithConfig(rl, config)

	api.WithOrderEmitter(engineEmitter)(apiHandler)
	e := engine.NewEngine(client,
		engine.WithStorage(store),
		engine.WithEmitter(engineEmitter),
		engine.WithFillTracker(fillTracker),
		engine.WithOrderScaler(scaler),
	)

	var wg sync.WaitGroup
	for i := 0; i < cfg.OrderWorkers; i++ {
		wg.Add(1)
		go runOrderWorker(workerCtx, &wg, oq, engineEmitter)
	}

	for i := 0; i < cfg.DealWorkers; i++ {
		wg.Add(1)
		go runWorker(workerCtx, &wg, q, e)
	}

	// Initial produce, then periodic re-enqueue by central resync
	produceOnce := func(ctx context.Context) {
		if err := e.ProduceActiveDeals(ctx, q); err != nil {
			slog.Debug("ProduceActiveDeals returned error", slog.String("error", err.Error()))
		}
	}
	produceOnce(appCtx)

	reconcileTakeProfits := func(ctx context.Context) {
		fillTracker.ReconcileTakeProfits(ctx, engineEmitter)
	}

	reconcileTakeProfits(appCtx)

	// Periodic resync; stops automatically when ctx is cancelled
	resync := cfg.ResyncInterval
	go func() {
		ticker := time.NewTicker(resync)
		defer ticker.Stop()
		for {
			select {
			case <-appCtx.Done():
				if err := drainHTTPServer(apiSrv, apiErrCh); err != nil {
					logger.Warn("HTTP API shutdown error", slog.String("error", err.Error()))
				}
				return
			case <-ticker.C:
				if appCtx.Err() != nil {
					return
				}
				produceOnce(appCtx)
				reconcileTakeProfits(appCtx)
			}
		}
	}()

	// Block until cancellation
	<-appCtx.Done()
	slog.Info("shutdown requested; draining queue...")
	q.ShutDownWithDrain()
	oq.ShutDownWithDrain()

	// Give the workers some time to shutdown, if not we abort
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done: // workers are drained here
	case <-waitCtx.Done():
		cancelWorkers()
	}

	// wait for the waitgroup to exit
	<-done

	slog.Debug("drained; fully shutdown")
}

type priceSourceMultiplexer struct {
	logger  *slog.Logger
	primary recomma.VenueID
	order   []recomma.VenueID
	sources map[recomma.VenueID]*ws.Client
}

func newPriceSourceMultiplexer(logger *slog.Logger, primary recomma.VenueID, order []recomma.VenueID, sources map[recomma.VenueID]*ws.Client) *priceSourceMultiplexer {
	orderCopy := append([]recomma.VenueID(nil), order...)
	sourceCopy := make(map[recomma.VenueID]*ws.Client, len(sources))
	for id, src := range sources {
		sourceCopy[id] = src
	}
	return &priceSourceMultiplexer{
		logger:  logger,
		primary: primary,
		order:   orderCopy,
		sources: sourceCopy,
	}
}

func (m *priceSourceMultiplexer) SubscribeBBO(ctx context.Context, coin string) (<-chan hl.BestBidOffer, error) {
	if len(m.sources) == 0 {
		return nil, errors.New("no hyperliquid price sources configured")
	}

	var errs []error
	try := func(id recomma.VenueID) (<-chan hl.BestBidOffer, bool) {
		client, ok := m.sources[id]
		if !ok || client == nil {
			return nil, false
		}
		ch, err := client.SubscribeBBO(ctx, coin)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("price subscription failed",
					slog.String("venue", string(id)),
					slog.String("coin", coin),
					slog.String("error", err.Error()),
				)
			}
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			return nil, false
		}
		if m.logger != nil {
			m.logger.Debug("price subscription registered",
				slog.String("venue", string(id)),
				slog.String("coin", coin),
			)
		}
		return ch, true
	}

	if m.primary != "" {
		if ch, ok := try(m.primary); ok {
			return ch, nil
		}
	}

	for _, id := range m.order {
		if id == m.primary {
			continue
		}
		if ch, ok := try(id); ok {
			return ch, nil
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, errors.New("no hyperliquid price source available")
}

func decorateVenueFlags(src map[string]interface{}, isPrimary bool) map[string]interface{} {
	flags := cloneVenueFlags(src)
	if flags == nil && !isPrimary {
		return nil
	}
	if flags == nil {
		flags = make(map[string]interface{}, 1)
	}
	flags["is_primary"] = isPrimary
	return flags
}

func shouldUseSentinelDefaultHyperliquidWallet(
	venues []vault.VenueSecret,
	primaryIdent recomma.VenueID,
	primaryWallet string,
	defaultIdent recomma.VenueID,
) bool {
	trimmedPrimary := strings.TrimSpace(primaryWallet)
	if trimmedPrimary == "" {
		return false
	}
	for _, venue := range venues {
		if !strings.EqualFold(venue.Type, "hyperliquid") {
			continue
		}
		venueID := recomma.VenueID(strings.TrimSpace(venue.ID))
		if venueID == defaultIdent {
			continue
		}
		if primaryIdent != "" && strings.EqualFold(string(venueID), string(primaryIdent)) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(venue.Wallet), trimmedPrimary) {
			return true
		}
	}
	return false
}

func cloneVenueFlags(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		if src == nil {
			return nil
		}
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func runWorker(ctx context.Context, wg *sync.WaitGroup, q workqueue.TypedRateLimitingInterface[engine.WorkKey], e *engine.Engine) {
	defer wg.Done()
	for {
		wi, shutdown := q.Get()
		if shutdown {
			return
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		processWorkItem(reqCtx, q, e, wi)
		cancel()
	}
}

func processWorkItem(ctx context.Context, q workqueue.TypedRateLimitingInterface[engine.WorkKey], e *engine.Engine, wi engine.WorkKey) {
	logger := rlog.LoggerFromContext(ctx).With("work-key", wi)
	defer q.Done(wi)
	if err := e.HandleDeal(ctx, wi); err != nil {
		if errors.Is(err, context.Canceled) {
			q.Forget(wi)
			return
		}

		// Special-case: cache miss should be retried (rate-limited) without applying the generic cap.
		if errors.Is(err, engine.ErrDealNotCached) {
			q.AddRateLimited(wi)
			return
		}

		logger.Debug("error handling deal, forgetting", slog.String("error", err.Error()))
		if q.NumRequeues(wi) < 5 {
			q.AddRateLimited(wi)
			return
		}
		q.Forget(wi)
		return
	}
	q.Forget(wi)
}

func runOrderWorker(
	ctx context.Context,
	wg *sync.WaitGroup,
	oq workqueue.TypedRateLimitingInterface[recomma.OrderWork],
	dispatcher *emitter.QueueEmitter,
) {
	defer wg.Done()
	for {
		w, shutdown := oq.Get()
		if shutdown {
			return
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		processOrderItem(reqCtx, oq, dispatcher, w)
		cancel()
	}
}

func processOrderItem(
	ctx context.Context,
	oq workqueue.TypedRateLimitingInterface[recomma.OrderWork],
	dispatcher *emitter.QueueEmitter,
	w recomma.OrderWork,
) {
	logger := rlog.LoggerFromContext(ctx).With("order-work", w)
	defer oq.Done(w)
	if err := dispatcher.Dispatch(ctx, w); err != nil {
		if errors.Is(err, recomma.ErrOrderAlreadySatisfied) {
			logger.Debug("order already satisfied; skipping submission")
			oq.Forget(w)
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Requeue with backoff so transient timeouts or global pacing don't drop work
			oq.AddRateLimited(w)
			return
		}
		if errors.Is(err, emitter.ErrMissingOrderIdentifier) || errors.Is(err, emitter.ErrUnregisteredVenueEmitter) || errors.Is(err, emitter.ErrOrderIdentifierMismatch) {
			logger.Error("discarding order work", slog.String("reason", err.Error()))
			oq.Forget(w)
			return
		}

		logger.Debug("error submitting order, forgetting", slog.String("error", err.Error()))
		if oq.NumRequeues(w) < 5 {
			oq.AddRateLimited(w)
			return
		}
		oq.Forget(w)
		return
	}

	oq.Forget(w)
}

func drainHTTPServer(srv *http.Server, errCh <-chan error) error {
	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	default:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
