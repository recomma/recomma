package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/rs/cors"
	"k8s.io/client-go/util/workqueue"

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
	"github.com/recomma/recomma/ratelimit"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/recomma/recomma/webui"
)

// App represents the entire Recomma application with all its components
type App struct {
	// Configuration
	Config config.AppConfig

	// Core components
	Store           *storage.Storage
	VaultController *vault.Controller
	Logger          *slog.Logger

	// API
	APIHandler       *api.ApiHandler
	StreamController *api.StreamController
	SystemStream     *api.SystemStreamController
	SystemStatus     *api.SystemStatusTracker

	// 3Commas integration
	ThreeCommasClient engine.ThreeCommasAPI
	RateLimiter       *ratelimit.Limiter

	// Hyperliquid integration
	StatusClients hl.StatusClientRegistry
	WsClients     map[recomma.VenueID]*ws.Client
	PrimaryVenue  recomma.VenueID

	// Workers and queues
	Engine       *engine.Engine
	DealQueue    workqueue.TypedRateLimitingInterface[engine.WorkKey]
	OrderQueue   workqueue.TypedRateLimitingInterface[recomma.OrderWork]
	OrderEmitter *emitter.QueueEmitter
	FillTracker  *filltracker.Service
	OrderScaler  *orderscaler.Service

	// HTTP Server
	Server          *http.Server
	serverAddr      string // Actual bound address (for random ports)
	serverErrCh     chan error
	serverStarted   bool
	serverStartedMu sync.Mutex

	// Lifecycle management
	ctx           context.Context
	cancelFunc    context.CancelFunc
	workerCtx     context.Context
	cancelWorkers context.CancelFunc
	wg            sync.WaitGroup
	shutdownOnce  sync.Once
	venueClosers  []func()

	// Runtime configuration
	allowedOrigins []string
	rpID           string
	dealWorkers    int
	resyncInterval time.Duration
	resyncTicker   *time.Ticker
	cleanupTicker  *time.Ticker
}

// AppOptions configures application creation
type AppOptions struct {
	Config config.AppConfig
	Store  *storage.Storage // Optional: inject storage (if nil, created from Config.StoragePath)

	// Optional: inject 3commas client for testing
	ThreeCommasClient engine.ThreeCommasAPI
}

// NewApp creates and initializes the application (but doesn't start workers/server)
//
// This performs all initialization up to the point where vault unsealing would occur.
// Call WaitForVaultUnseal() then Start() to begin operation.
func NewApp(ctx context.Context, opts AppOptions) (*App, error) {
	cfg := opts.Config

	allowedOrigins := origin.BuildAllowedOrigins(cfg.HTTPListen, cfg.PublicOrigin)
	rpID := origin.DeriveRPID(cfg.HTTPListen, cfg.PublicOrigin)

	// Create contexts
	appCtx, cancel := context.WithCancel(ctx)
	workerCtx, cancelWorkers := context.WithCancel(context.Background())

	// Setup logging
	logger := slog.New(config.GetLogHandler(cfg))
	slog.SetDefault(logger)
	log.SetOutput(slog.NewLogLogger(logger.Handler(), slog.LevelDebug).Writer())

	webui.SetDebug(cfg.Debug)
	appCtx = rlog.ContextWithLogger(appCtx, logger)

	// Initialize stream controllers
	streamController := api.NewStreamController(api.WithStreamLogger(logger))

	// Parse system stream log level
	systemLevel := api.SystemEventInfo // default
	switch strings.ToLower(cfg.SystemStreamMinLevel) {
	case "debug":
		systemLevel = api.SystemEventDebug
	case "info":
		systemLevel = api.SystemEventInfo
	case "warn", "warning":
		systemLevel = api.SystemEventWarn
	case "error":
		systemLevel = api.SystemEventError
	default:
		slog.Warn("unknown system stream level, defaulting to info", slog.String("level", cfg.SystemStreamMinLevel))
	}

	systemStream := api.NewSystemStreamController(systemLevel)
	systemStatus := api.NewSystemStatusTracker()

	// Initialize storage (use provided store or create new one)
	var store *storage.Storage
	var err error
	if opts.Store != nil {
		store = opts.Store
	} else {
		store, err = storage.New(cfg.StoragePath, storage.WithStreamPublisher(streamController), storage.WithLogger(logger))
		if err != nil {
			return nil, fmt.Errorf("storage init failed: %w", err)
		}
	}

	// Initialize WebAuthn
	webAuth, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Recomma",
		RPID:          rpID,
		RPOrigins:     allowedOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("webauth init failed: %w", err)
	}

	// Initialize vault controller
	initialVaultState := vault.StateSetupRequired
	var controllerOpts []vault.ControllerOption

	existingUser, err := store.GetVaultUser(appCtx)
	if err != nil {
		return nil, fmt.Errorf("load vault user: %w", err)
	}
	if existingUser != nil {
		controllerOpts = append(controllerOpts, vault.WithInitialUser(existingUser))

		payload, err := store.GetVaultPayloadForUser(appCtx, existingUser.ID)
		if err != nil {
			return nil, fmt.Errorf("load vault payload: %w", err)
		}
		if payload != nil {
			initialVaultState = vault.StateSealed
			sealedAt := payload.UpdatedAt
			controllerOpts = append(controllerOpts, vault.WithInitialTimestamps(&sealedAt, nil, nil))
		}
	}

	vaultController := vault.NewController(initialVaultState, controllerOpts...)

	// Initialize WebAuthn API
	webAuthApi, err := api.NewWebAuthnService(api.WebAuthnServiceConfig{
		WebAuthn: webAuth,
		Store:    store,
		Logger:   logger,
	})
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("webauth api init failed: %w", err)
	}

	// Initialize API handler (without order emitter and price source, set later)
	apiHandler := api.NewHandler(store, streamController,
		api.WithLogger(logger),
		api.WithWebAuthnService(webAuthApi),
		api.WithVaultController(vaultController),
		api.WithOrderScalerMaxMultiplier(cfg.OrderScalerMaxMultiplier),
		api.WithDebugMode(cfg.Debug),
		api.WithSystemStream(systemStream),
		api.WithSystemStatus(systemStatus),
	)

	// Setup HTTP server
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
	rootMux.Handle("/stream/", apiHandlerWithCORS)
	rootMux.Handle("/stream", apiHandlerWithCORS)
	rootMux.Handle("/webauthn/", apiHandlerWithCORS)
	rootMux.Handle("/vault/", apiHandlerWithCORS)
	rootMux.Handle("/vault", apiHandlerWithCORS)
	rootMux.Handle("/", webHandler)

	apiSrv := &http.Server{
		Addr:    cfg.HTTPListen,
		Handler: rootMux,
	}

	app := &App{
		Config:           cfg,
		Store:            store,
		VaultController:  vaultController,
		Logger:           logger,
		APIHandler:       apiHandler,
		StreamController: streamController,
		SystemStream:     systemStream,
		SystemStatus:     systemStatus,
		Server:           apiSrv,
		serverErrCh:      make(chan error, 1),
		ctx:              appCtx,
		cancelFunc:       cancel,
		workerCtx:        workerCtx,
		cancelWorkers:    cancelWorkers,
		allowedOrigins:   allowedOrigins,
		rpID:             rpID,
		StatusClients:    make(hl.StatusClientRegistry),
		WsClients:        make(map[recomma.VenueID]*ws.Client),
	}

	// If 3commas client provided, store it (for testing)
	if opts.ThreeCommasClient != nil {
		app.ThreeCommasClient = opts.ThreeCommasClient
	}

	return app, nil
}

// StartHTTPServer starts the HTTP server (non-blocking)
// This is called before vault unsealing to allow WebAuthn/vault UI access
func (a *App) StartHTTPServer() {
	a.serverStartedMu.Lock()
	defer a.serverStartedMu.Unlock()

	if a.serverStarted {
		return
	}

	// Create listener to get the actual bound address (important for random ports)
	listener, err := net.Listen("tcp", a.Server.Addr)
	if err != nil {
		a.serverErrCh <- err
		return
	}

	// Store the actual bound address
	a.serverAddr = listener.Addr().String()

	go func() {
		a.Logger.Info("HTTP API listening", slog.String("addr", a.serverAddr), slog.String("public_origin", a.Config.PublicOrigin))
		if err := a.Server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.serverErrCh <- err
		}
	}()

	a.serverStarted = true
}

// CheckHTTPServerError returns any error from HTTP server startup
func (a *App) CheckHTTPServerError() error {
	select {
	case err := <-a.serverErrCh:
		return err
	default:
		return nil
	}
}

// WaitForVaultUnseal blocks until vault is unsealed, context is canceled, or HTTP server fails
// Returns nil if vault is unsealed successfully
func (a *App) WaitForVaultUnseal(ctx context.Context) error {
	a.Logger.Debug("Waiting for vault to be unsealed")

	unsealedCh := make(chan error, 1)
	go func() {
		unsealedCh <- a.VaultController.WaitUntilUnsealed(ctx)
	}()

	select {
	case err := <-a.serverErrCh:
		if err != nil {
			a.Logger.Error("HTTP server failed before unseal", slog.String("error", err.Error()))
			return fmt.Errorf("HTTP server failed: %w", err)
		}
		a.Logger.Info("HTTP server closed before unseal")
		return errors.New("HTTP server closed before unseal")

	case <-ctx.Done():
		a.Logger.Warn("context cancelled while waiting for vault unseal", slog.String("error", ctx.Err().Error()))
		return ctx.Err()

	case err := <-unsealedCh:
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				a.Logger.Warn("vaultController WaitUntilUnsealed returned an error", slog.String("error", err.Error()))
			}
			return err
		}
		return nil
	}
}

// HTTPAddr returns the HTTP server address
func (a *App) HTTPAddr() string {
	a.serverStartedMu.Lock()
	defer a.serverStartedMu.Unlock()

	// Return actual bound address if server has started (handles random ports)
	if a.serverStarted && a.serverAddr != "" {
		return a.serverAddr
	}

	// Fallback to configured address
	if a.Server == nil {
		return ""
	}
	return a.Server.Addr
}

// Start initializes all components after vault is unsealed and starts workers
// This must be called after WaitForVaultUnseal() returns successfully
func (a *App) Start(ctx context.Context) error {
	secrets := a.VaultController.Secrets()
	if secrets == nil {
		return errors.New("vault secrets unavailable")
	}

	// Parse ThreeCommas plan tier for rate limiting configuration
	planTier, defaulted, err := recomma.ParseThreeCommasPlanTierOrDefault(secrets.Secrets.THREECOMMASPLANTIER)
	if err != nil {
		return fmt.Errorf("parse threecommas plan tier: %w", err)
	}
	if defaulted {
		a.Logger.Warn("THREECOMMAS_PLAN_TIER missing from vault; defaulting to expert rate limits")
	}

	// Get tier-specific rate limit configuration
	rateLimitCfg := planTier.RateLimitConfig()
	a.dealWorkers = rateLimitCfg.DealWorkers
	a.resyncInterval = rateLimitCfg.ResyncInterval

	a.Logger.Info("Rate limiting configured",
		slog.String("tier", string(planTier)),
		slog.Int("requests_per_minute", rateLimitCfg.RequestsPerMinute),
		slog.Int("deal_workers", a.dealWorkers),
		slog.Duration("resync_interval", a.resyncInterval),
	)

	// Create rate limiter
	a.RateLimiter = ratelimit.NewLimiter(ratelimit.Config{
		RequestsPerMinute: rateLimitCfg.RequestsPerMinute,
		PrioritySlots:     rateLimitCfg.PrioritySlots,
		Logger:            a.Logger,
	})

	// Create ThreeCommas client (unless already provided, e.g., for testing)
	if a.ThreeCommasClient == nil {
		baseClient, err := tc.New3CommasClient(
			tc.WithAPIKey(secrets.Secrets.THREECOMMASAPIKEY),
			tc.WithPrivatePEM([]byte(secrets.Secrets.THREECOMMASPRIVATEKEY)),
			tc.WithPlanTier(planTier.SDKTier()),
			tc.WithClientOption(tc.WithRequestEditorFn(func(ctx context.Context, r *http.Request) error {
				slog.Default().WithGroup("threecommas").Debug("requesting", "method", r.Method, "url", r.URL.String())
				return nil
			})),
		)
		if err != nil {
			return fmt.Errorf("3commas client init failed: %w", err)
		}
		a.ThreeCommasClient = ratelimit.NewClient(baseClient, a.RateLimiter)
	}

	// Initialize fill tracker
	a.FillTracker = filltracker.New(a.Store, a.Logger)

	// Create order queue and emitter
	rlOrders := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[recomma.OrderWork](1*time.Second, 30*time.Second),
	)
	oqCfg := workqueue.TypedRateLimitingQueueConfig[recomma.OrderWork]{Name: "orders"}
	a.OrderQueue = workqueue.NewTypedRateLimitingQueueWithConfig(rlOrders, oqCfg)
	a.OrderEmitter = emitter.NewQueueEmitter(a.OrderQueue)

	// Initialize Hyperliquid venues
	if err := a.initializeHyperliquidVenues(ctx, secrets); err != nil {
		return fmt.Errorf("initialize hyperliquid venues: %w", err)
	}

	// Create deal queue
	rl := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[engine.WorkKey](1*time.Second, 2*time.Hour),
	)
	dealQueueCfg := workqueue.TypedRateLimitingQueueConfig[engine.WorkKey]{Name: "deals"}
	a.DealQueue = workqueue.NewTypedRateLimitingQueueWithConfig(rl, dealQueueCfg)

	// Wire up API handler
	api.WithOrderEmitter(a.OrderEmitter)(a.APIHandler)

	// Create engine
	a.Engine = engine.NewEngine(a.ThreeCommasClient,
		engine.WithStorage(a.Store),
		engine.WithEmitter(a.OrderEmitter),
		engine.WithFillTracker(a.FillTracker),
		engine.WithOrderScaler(a.OrderScaler),
		engine.WithRateLimiter(a.RateLimiter),
		engine.WithProduceConcurrency(rateLimitCfg.ProduceConcurrency),
	)

	// Start order workers
	for i := 0; i < a.Config.OrderWorkers; i++ {
		a.wg.Add(1)
		go runOrderWorker(a.workerCtx, &a.wg, a.OrderQueue, a.OrderEmitter)
	}

	// Start deal workers
	for i := 0; i < a.dealWorkers; i++ {
		a.wg.Add(1)
		go runWorker(a.workerCtx, &a.wg, a.DealQueue, a.Engine)
	}

	a.Logger.Info("Service ready",
		slog.Int("hyperliquid_venues", len(a.StatusClients)),
		slog.String("primary_venue", string(a.PrimaryVenue)),
	)

	// Initial produce
	a.ProduceActiveDealsOnce(ctx)
	a.FillTracker.ReconcileTakeProfits(ctx, a.OrderEmitter)

	// Start periodic tasks
	a.startPeriodicTasks(ctx)

	return nil
}

// ProduceActiveDealsOnce triggers a single deal production cycle
func (a *App) ProduceActiveDealsOnce(ctx context.Context) {
	if err := a.Engine.ProduceActiveDeals(ctx, a.DealQueue); err != nil {
		slog.Error("ProduceActiveDeals returned error", slog.String("error", err.Error()))

		// Publish error to UI via system stream
		a.SystemStream.Publish(api.SystemEvent{
			Level:     api.SystemEventError,
			Timestamp: time.Now().UTC(),
			Source:    "3commas",
			Message:   err.Error(),
		})

		// Track error for polling endpoint
		a.SystemStatus.SetThreeCommasError(err.Error())
	} else {
		a.SystemStatus.ClearThreeCommasError()
	}
}

// startPeriodicTasks starts background periodic tasks (resync, reconcile, cleanup)
func (a *App) startPeriodicTasks(ctx context.Context) {
	a.resyncTicker = time.NewTicker(a.resyncInterval)
	a.cleanupTicker = time.NewTicker(10 * time.Minute)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.resyncTicker.C:
				if ctx.Err() != nil {
					return
				}
				a.ProduceActiveDealsOnce(ctx)
				a.FillTracker.ReconcileTakeProfits(ctx, a.OrderEmitter)
			case <-a.cleanupTicker.C:
				if ctx.Err() != nil {
					return
				}
				a.FillTracker.CleanupStaleDeals(time.Hour)
			}
		}
	}()
}

// initializeHyperliquidVenues sets up all Hyperliquid venues from vault secrets
func (a *App) initializeHyperliquidVenues(ctx context.Context, secrets *vault.Secrets) error {
	primaryHyperliquid, ok := secrets.Secrets.PrimaryVenueByType("hyperliquid")
	if !ok {
		return errors.New("no primary hyperliquid venue configured")
	}

	primaryVenueID := strings.TrimSpace(primaryHyperliquid.ID)
	if primaryVenueID == "" {
		return errors.New("primary hyperliquid venue missing identifier")
	}
	primaryWallet := strings.TrimSpace(primaryHyperliquid.Wallet)
	if primaryWallet == "" {
		return errors.New("primary hyperliquid venue missing wallet")
	}
	primaryAPIURL := strings.TrimSpace(primaryHyperliquid.APIURL)
	if primaryAPIURL == "" {
		return errors.New("primary hyperliquid venue missing api_url")
	}

	defaultAssignment, err := a.Store.ResolveDefaultAlias(ctx)
	if err != nil {
		return fmt.Errorf("resolve default alias: %w", err)
	}
	defaultHyperliquidIdent := defaultAssignment.VenueID
	defaultHyperliquidWallet := defaultAssignment.Wallet

	defaultVenueWallet := primaryWallet
	if shouldUseSentinelDefaultHyperliquidWallet(
		secrets.Secrets.Venues,
		recomma.VenueID(primaryVenueID),
		primaryWallet,
		defaultHyperliquidIdent,
	) {
		defaultVenueWallet = ""
	}

	defaultAliasWallet := strings.TrimSpace(defaultVenueWallet)
	if defaultAliasWallet == "" {
		defaultAliasWallet = defaultHyperliquidWallet
	}

	primaryIdent := recomma.VenueID(primaryVenueID)
	a.PrimaryVenue = primaryIdent

	gateRegistry := make(map[rateGateKey]emitter.RateGate)
	var venueOrder []recomma.VenueID

	emitterLogger := a.Logger.WithGroup("hyperliquid").WithGroup("emitter")
	priceLogger := a.Logger.WithGroup("hyperliquid").WithGroup("prices")
	runtimeLogger := a.Logger.WithGroup("hyperliquid")

	var constraintsInfo *hl.Info

	for _, venue := range secrets.Secrets.Venues {
		if !strings.EqualFold(venue.Type, "hyperliquid") {
			runtimeLogger.Warn("unsupported venue type in secrets", slog.String("venue", venue.ID), slog.String("type", venue.Type))
			continue
		}

		venueID := strings.TrimSpace(venue.ID)
		if venueID == "" {
			return errors.New("hyperliquid venue missing identifier")
		}

		wallet := strings.TrimSpace(venue.Wallet)
		if wallet == "" {
			return errors.New("hyperliquid venue missing wallet")
		}

		displayName := strings.TrimSpace(venue.DisplayName)
		if displayName == "" {
			displayName = venueID
		}

		privateKey := strings.TrimSpace(venue.PrivateKey)
		if privateKey == "" {
			return errors.New("hyperliquid venue missing private key")
		}

		apiURL := strings.TrimSpace(venue.APIURL)
		if apiURL == "" {
			apiURL = primaryAPIURL
		}

		// WebSocket URL: use explicit websocket_url if provided, otherwise fall back to api_url
		wsURL := strings.TrimSpace(venue.WebsocketURL)
		if wsURL == "" {
			wsURL = apiURL
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

		if _, err := a.Store.UpsertVenue(ctx, venueID, payload); err != nil {
			return fmt.Errorf("persist venue configuration: %w", err)
		}

		exchange, err := hl.NewExchange(ctx, hl.ClientConfig{
			BaseURL: apiURL,
			Wallet:  wallet,
			Key:     privateKey,
		})
		if err != nil {
			return fmt.Errorf("create hyperliquid exchange: %w", err)
		}

		info := hl.NewInfo(ctx, hl.ClientConfig{
			BaseURL: apiURL,
			Wallet:  wallet,
		})
		registerHyperliquidStatusClient(a.StatusClients, info, venueIdent, primaryIdent, defaultHyperliquidIdent)
		if constraintsInfo == nil || venueIdent == primaryIdent {
			constraintsInfo = info
		}

		wsClient, err := ws.New(ctx, a.Store, a.FillTracker, venueIdent, wallet, wsURL)
		if err != nil {
			return fmt.Errorf("create hyperliquid websocket: %w", err)
		}
		registerHyperliquidWsClient(a.WsClients, wsClient, venueIdent)
		venueOrder = append(venueOrder, venueIdent)

		client := wsClient
		closeVenue := func() {
			if err := client.Close(); err != nil {
				runtimeLogger.Debug("websocket close failed", slog.String("venue", venueID), slog.String("error", err.Error()))
			}
		}
		a.venueClosers = append(a.venueClosers, closeVenue)

		gateKey := rateGateKey{venueType: strings.ToLower(venue.Type), wallet: strings.ToLower(wallet)}
		gate, exists := gateRegistry[gateKey]
		if !exists {
			gate = emitter.NewRateGate(0)
			gateRegistry[gateKey] = gate
		}

		submitter := emitter.NewHyperLiquidEmitter(exchange, venueIdent, wsClient, a.Store, hl.NewOrderIdCache(constraintsInfo),
			emitter.WithHyperLiquidRateGate(gate),
			emitter.WithHyperLiquidStatusClient(info),
			emitter.WithHyperLiquidEmitterConfig(emitter.HyperLiquidEmitterConfig{
				InitialIOCOffsetBps: a.Config.HyperliquidIOCInitialOffsetBps,
			}),
			emitter.WithHyperLiquidEmitterLogger(emitterLogger.With(slog.String("venue", venueID), slog.String("wallet", wallet))),
		)

		registerHyperliquidEmitter(a.OrderEmitter, submitter, venueIdent, primaryIdent, defaultHyperliquidIdent)

		runtimeLogger.Info("hyperliquid venue configured",
			slog.String("venue", venueID),
			slog.String("wallet", wallet),
			slog.String("api_url", apiURL),
			slog.Bool("primary", venue.Primary),
		)
	}

	// Ensure default venue is configured after all venues from secrets are processed
	// This allows the default venue to detect conflicts and skip creation if needed
	if err := a.Store.EnsureDefaultVenueWallet(ctx, defaultVenueWallet); err != nil {
		return fmt.Errorf("update default venue wallet: %w", err)
	}

	if len(a.StatusClients) == 0 {
		return errors.New("no hyperliquid venues configured")
	}

	if _, ok := a.StatusClients[primaryIdent]; !ok {
		return errors.New("primary hyperliquid venue missing from configuration")
	}
	if constraintsInfo == nil {
		return errors.New("unable to resolve constraints info client")
	}

	if err := syncPrimaryVenueAssignments(ctx, a.Store, runtimeLogger, primaryIdent); err != nil {
		return fmt.Errorf("sync primary venue assignments: %w", err)
	}

	a.OrderScaler = orderscaler.New(a.Store, hl.NewOrderIdCache(constraintsInfo), a.Logger, orderscaler.WithMaxMultiplier(a.Config.OrderScalerMaxMultiplier))

	statusRefresher := hl.NewStatusRefresher(a.StatusClients, a.Store,
		hl.WithStatusRefresherLogger(a.Logger),
		hl.WithStatusRefresherTracker(a.FillTracker),
	)

	// TODO: needs to become venue aware!
	if err := statusRefresher.Refresh(ctx); err != nil {
		a.Logger.Warn("status refresher failed", slog.String("error", err.Error()))
	}

	if err := a.FillTracker.Rebuild(ctx); err != nil {
		a.Logger.Warn("fill tracker rebuild failed", slog.String("error", err.Error()))
	}

	priceSource := newPriceSourceMultiplexer(priceLogger, primaryIdent, venueOrder, a.WsClients)
	api.WithHyperliquidPriceSource(priceSource)(a.APIHandler)

	return nil
}

// Shutdown gracefully stops all components
func (a *App) Shutdown(ctx context.Context) error {
	var shutdownErr error

	a.shutdownOnce.Do(func() {
		a.Logger.Info("shutdown requested")

		// Stop periodic tasks
		if a.resyncTicker != nil {
			a.resyncTicker.Stop()
		}
		if a.cleanupTicker != nil {
			a.cleanupTicker.Stop()
		}

		// Shutdown HTTP server first to prevent new requests from being accepted
		// This must happen before draining queues to avoid panics when API handlers
		// try to enqueue work on a shutdown queue
		if a.Server != nil {
			if err := drainHTTPServer(a.Server, a.serverErrCh); err != nil {
				a.Logger.Warn("HTTP server shutdown error", slog.String("error", err.Error()))
				shutdownErr = err
			}
		}

		// Drain queues (safe now that HTTP server is shut down)
		if a.DealQueue != nil {
			a.Logger.Debug("draining deal queue")
			a.DealQueue.ShutDownWithDrain()
		}
		if a.OrderQueue != nil {
			a.Logger.Debug("draining order queue")
			a.OrderQueue.ShutDownWithDrain()
		}

		// Wait for workers to finish (with timeout)
		done := make(chan struct{})
		go func() {
			a.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			a.Logger.Debug("workers drained")
		case <-ctx.Done():
			a.Logger.Warn("shutdown timeout, canceling workers")
			a.cancelWorkers()
			<-done
		}

		// Close websocket connections
		for _, closeFn := range a.venueClosers {
			closeFn()
		}

		// Close storage
		if a.Store != nil {
			if err := a.Store.Close(); err != nil {
				a.Logger.Warn("storage close error", slog.String("error", err.Error()))
				if shutdownErr == nil {
					shutdownErr = err
				}
			}
		}

		// Cancel app context
		a.cancelFunc()

		a.Logger.Debug("shutdown complete")
	})

	return shutdownErr
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
