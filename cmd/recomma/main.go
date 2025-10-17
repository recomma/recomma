package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/util/workqueue"

	"github.com/rs/cors"
	"github.com/sonirico/go-hyperliquid"

	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/cmd/recomma/internal/config"
	"github.com/terwey/recomma/emitter"
	"github.com/terwey/recomma/engine"
	"github.com/terwey/recomma/hl"
	"github.com/terwey/recomma/hl/ws"
	"github.com/terwey/recomma/internal/api"
	rlog "github.com/terwey/recomma/log"
	"github.com/terwey/recomma/recomma"
	"github.com/terwey/recomma/storage"
	"github.com/terwey/recomma/webui"
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

	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerCtx, cancelWorkers := context.WithCancel(context.Background())

	logger := slog.New(config.GetLogHandler(cfg))
	slog.SetDefault(logger)
	log.SetOutput(slog.NewLogLogger(logger.Handler(), slog.LevelDebug).Writer())

	appCtx = rlog.ContextWithLogger(appCtx, logger)

	store, err := storage.New(cfg.StoragePath, nil)
	if err != nil {
		fatal("storage init failed", err)
	}
	defer store.Close()

	client, err := tc.New3CommasClient(cfg.ThreeCommas,
		tc.WithRequestEditorFn(func(ctx context.Context, r *http.Request) error {
			slog.Default().WithGroup("threecommas").Debug("sending", "method", r.Method, "url", r.URL.String())
			return nil
		}))
	if err != nil {
		fatal("3commas client init failed", err)
	}

	exchange, err := hl.NewExchange(appCtx, cfg.Hyperliquid)
	if err != nil {
		fatal("Could not create Hyperliquid Exchange", err)
	}

	ws, err := ws.New(appCtx, store, cfg.Hyperliquid.Wallet, cfg.Hyperliquid.BaseURL)
	if err != nil {
		fatal("Could not create Hyperliquid websocket conn", err)
	}
	defer ws.Close()

	apiHandler := api.NewHandler(store, nil /* StreamSource */, logger)

	strictServer := api.NewStrictHandler(apiHandler, nil)

	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins: buildAllowedOrigins(cfg.HTTPListen),
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
	webHandler := webui.Handler()

	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", apiHandlerWithCORS)
	rootMux.Handle("/api", apiHandlerWithCORS)
	rootMux.Handle("/sse/", apiHandlerWithCORS)
	rootMux.Handle("/sse", apiHandlerWithCORS)
	rootMux.Handle("/", webHandler)

	apiSrv := &http.Server{
		Addr:    cfg.HTTPListen,
		Handler: rootMux,
	}

	apiErrCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP API listening", slog.String("addr", apiSrv.Addr))
		if err := apiSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			apiErrCh <- err
		}
	}()

	logger.Info("Service ready", slog.String("wallet", cfg.Hyperliquid.Wallet), slog.String("baseurl", cfg.Hyperliquid.BaseURL))

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
	e := engine.NewEngine(client, store, engineEmitter)

	submitter := emitter.NewHyperLiquidEmitter(exchange, ws, store)

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

	cancelTakeProfit := func(ctx context.Context) {
		deals, err := store.ListDealIDs(ctx)
		if err != nil {
			slog.Debug("cancelTakeProfit ListDeals returned an error", slog.String("error", err.Error()))
		}

		logger.Debug("checking deals if completed", slog.Any("deals", deals))

		for _, d := range deals {
			dealLogger := logger.With("deal_id", d)
			filled, err := store.DealSafetiesFilled(uint32(d))
			if err != nil {
				dealLogger.Debug("cancelTakeProfit DealSafetiesFilled returned an error", slog.String("error", err.Error()))
			}
			if !filled {
				continue
			}

			md, event, err := store.LoadTakeProfitForDeal(uint32(d))
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					dealLogger.Debug("deal has no TakeProfit")
					continue
				}
				dealLogger.Warn("cancelTakeProfit LoadTakeProfitForDeal returned an error", slog.String("error", err.Error()))
				continue
			}

			cancelCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

			err = submitter.Emit(cancelCtx, recomma.OrderWork{
				MD: *md,
				Action: recomma.Action{
					Type: recomma.ActionCancel,
					Cancel: &hyperliquid.CancelOrderRequestByCloid{
						Coin:  event.Coin,
						Cloid: md.Hex(),
					},
					Reason: "safeties filled for deal",
				},
			})
			cancel()
			if err != nil {
				dealLogger.Warn("could not cancel TP", slog.String("error", err.Error()))
			} else {
				dealLogger.Info("Safeties filled for Deal, cancelled TP")
			}
		}
	}

	cancelTakeProfit(appCtx)

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
				cancelTakeProfit(appCtx)
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

func buildAllowedOrigins(listenAddr string) []string {
	origins := []string{"http://localhost:3000"}

	host := strings.TrimSpace(listenAddr)
	if host == "" {
		return origins
	}

	addr := host
	if !strings.Contains(host, ":") {
		addr = ":" + host
	}

	parsedHost, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return origins
	}

	candidates := []string{"localhost", "127.0.0.1"}
	if parsedHost != "" && parsedHost != "0.0.0.0" && parsedHost != "::" {
		candidates = append(candidates, parsedHost)
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		hostLabel := candidate
		if strings.Contains(candidate, ":") && !strings.HasPrefix(candidate, "[") {
			hostLabel = "[" + candidate + "]"
		}

		origin := fmt.Sprintf("http://%s:%s", hostLabel, port)
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}

	return origins
}
