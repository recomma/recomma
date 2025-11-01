package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
	"github.com/recomma/recomma/internal/origin"
	"github.com/recomma/recomma/internal/vault"
	rlog "github.com/recomma/recomma/log"
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

	allowedOrigins := origin.BuildAllowedOrigins(cfg.HTTPListen, cfg.PublicOrigin)
	rpID := origin.DeriveRPID(cfg.HTTPListen, cfg.PublicOrigin)

	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerCtx, cancelWorkers := context.WithCancel(context.Background())

	logger := slog.New(config.GetLogHandler(cfg))
	slog.SetDefault(logger)
	log.SetOutput(slog.NewLogLogger(logger.Handler(), slog.LevelDebug).Writer())

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

	// we can now access the secrets
	secrets := vaultController.Secrets()

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

	exchange, err := hl.NewExchange(appCtx, hl.ClientConfig{
		BaseURL: secrets.Secrets.HYPERLIQUIDURL,
		Wallet:  secrets.Secrets.HYPERLIQUIDWALLET,
		Key:     secrets.Secrets.HYPERLIQUIDPRIVATEKEY,
	})
	if err != nil {
		fatal("Could not create Hyperliquid Exchange", err)
	}

	info := hl.NewInfo(appCtx, hl.ClientConfig{
		BaseURL: secrets.Secrets.HYPERLIQUIDURL,
		Wallet:  secrets.Secrets.HYPERLIQUIDWALLET,
	})

	constraints := hl.NewOrderIdCache(info)
	scaler := orderscaler.New(store, constraints, logger, orderscaler.WithMaxMultiplier(cfg.OrderScalerMaxMultiplier))

	fillTracker := filltracker.New(store, logger)

	statusRefresher := hl.NewStatusRefresher(info, store,
		hl.WithStatusRefresherLogger(logger),
		hl.WithStatusRefresherTracker(fillTracker),
	)
	if err := statusRefresher.Refresh(appCtx); err != nil {
		logger.Warn("status refresher failed", slog.String("error", err.Error()))
	}

	if err := fillTracker.Rebuild(appCtx); err != nil {
		logger.Warn("fill tracker rebuild failed", slog.String("error", err.Error()))
	}

	ws, err := ws.New(appCtx, store, fillTracker, secrets.Secrets.HYPERLIQUIDWALLET, secrets.Secrets.HYPERLIQUIDURL)
	if err != nil {
		fatal("Could not create Hyperliquid websocket conn", err)
	}
	defer ws.Close()
	api.WithHyperliquidPriceSource(ws)(apiHandler)

	logger.Info("Service ready", slog.String("baseurl", secrets.Secrets.HYPERLIQUIDURL))

	// Queue + workers
	// Q creation (typed)
	rl := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[engine.WorkKey](1*time.Second, 30*time.Second), // backoff on failures
	)

	config := workqueue.TypedRateLimitingQueueConfig[engine.WorkKey]{
		Name: "deals",
	}

	q := workqueue.NewTypedRateLimitingQueueWithConfig(rl, config)

	rlOrders := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[recomma.OrderWork](1*time.Second, 30*time.Second),
	)
	oqCfg := workqueue.TypedRateLimitingQueueConfig[recomma.OrderWork]{Name: "orders"}
	oq := workqueue.NewTypedRateLimitingQueueWithConfig(rlOrders, oqCfg)

	engineEmitter := emitter.NewQueueEmitter(oq)
	api.WithOrderEmitter(engineEmitter)(apiHandler)
	e := engine.NewEngine(client,
		engine.WithStorage(store),
		engine.WithEmitter(engineEmitter),
		engine.WithFillTracker(fillTracker),
		engine.WithOrderScaler(scaler),
	)

	submitter := emitter.NewHyperLiquidEmitter(exchange, ws, store,
		emitter.WithHyperLiquidEmitterConfig(emitter.HyperLiquidEmitterConfig{
			InitialIOCOffsetBps: cfg.HyperliquidIOCInitialOffsetBps,
		}),
	)

	var wg sync.WaitGroup
	for i := 0; i < cfg.OrderWorkers; i++ {
		wg.Add(1)
		go runOrderWorker(workerCtx, &wg, oq, submitter)
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
		fillTracker.ReconcileTakeProfits(ctx, submitter)
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
	submitter recomma.Emitter,
) {
	defer wg.Done()
	for {
		w, shutdown := oq.Get()
		if shutdown {
			return
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		processOrderItem(reqCtx, oq, submitter, w)
		cancel()
	}
}

func processOrderItem(
	ctx context.Context,
	oq workqueue.TypedRateLimitingInterface[recomma.OrderWork],
	submitter recomma.Emitter,
	w recomma.OrderWork,
) {
	logger := rlog.LoggerFromContext(ctx).With("order-work", w)
	defer oq.Done(w)
	if err := submitter.Emit(ctx, w); err != nil {
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
