package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/util/workqueue"

	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/emitter"
	"github.com/terwey/recomma/engine"
	"github.com/terwey/recomma/hl"
	"github.com/terwey/recomma/storage"
)

func main() {
	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerCtx, cancelWorkers := context.WithCancel(context.Background())

	store, err := storage.New("badger")
	if err != nil {
		log.Fatalf("storage init: %v", err)
	}
	defer store.Close()

	tcStore := store.WithPrefix("tc")

	client, err := tc.New3CommasClient(config)
	if err != nil {
		log.Fatalf("3commas client: %v", err)
	}

	exchange, err := hl.NewExchange(hlConfig)
	if err != nil {
		log.Fatalf("Could not create Hyperliquid Exchange: %s", err)
	}

	info := hl.NewInfo(hlConfig)

	// Warm the seen-keys bloom/index with the last three months (prefix: YYYY-MM)
	for n := 0; n <= 2; n++ {
		prefix := time.Now().AddDate(0, -n, 0).Format("2006-01")
		if err := tcStore.LoadSeenKeys(prefix); err != nil {
			log.Fatalf("cannot get seen hashes for %s: %v", prefix, err)
		}
	}

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
		workqueue.NewTypedItemExponentialFailureRateLimiter[emitter.OrderWork](1*time.Second, 30*time.Second),
	)
	oqCfg := workqueue.TypedRateLimitingQueueConfig[emitter.OrderWork]{Name: "orders"}
	oq := workqueue.NewTypedRateLimitingQueueWithConfig(rlOrders, oqCfg)

	engineEmitter := emitter.NewQueueEmitter(oq)
	e := engine.NewEngine(client, tcStore, engineEmitter)

	submitter := emitter.NewHyperLiquidEmitter(exchange, info, store.WithPrefix("hl"))

	var wg sync.WaitGroup
	const orderWorkers = 5
	for i := 0; i < orderWorkers; i++ {
		wg.Add(1)
		go runOrderWorker(workerCtx, &wg, oq, submitter)
	}

	const numWorkers = 25
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go runWorker(workerCtx, &wg, q, e)
	}

	// Initial produce, then periodic re-enqueue by central resync
	produceOnce := func(ctx context.Context) {
		if err := e.ProduceActiveDeals(ctx, q); err != nil {
			log.Printf("producer: %v", err)
		}
	}
	produceOnce(appCtx)

	// Periodic resync; stops automatically when ctx is cancelled
	resync := 15 * time.Second
	go func() {
		ticker := time.NewTicker(resync)
		defer ticker.Stop()
		for {
			select {
			case <-appCtx.Done():
				return
			case <-ticker.C:
				if appCtx.Err() != nil {
					return
				}
				produceOnce(appCtx)
			}
		}
	}()

	// Block until cancellation
	<-appCtx.Done()
	log.Printf("shutdown requested; draining queue...")
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

	log.Printf("drained; seen %d entries in storage", tcStore.Size())
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

		log.Printf("deal %d: %v", wi.DealID, err)
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
	oq workqueue.TypedRateLimitingInterface[emitter.OrderWork],
	submitter emitter.Emitter,
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
	oq workqueue.TypedRateLimitingInterface[emitter.OrderWork],
	submitter emitter.Emitter,
	w emitter.OrderWork,
) {
	defer oq.Done(w)
	if err := submitter.Emit(ctx, w); err != nil {
		if errors.Is(err, context.Canceled) {
			oq.Forget(w)
			return
		}
		log.Printf("order %s: %v", w.MD.String(), err)
		if oq.NumRequeues(w) < 5 {
			oq.AddRateLimited(w)
			return
		}
		oq.Forget(w)
		return
	}
	oq.Forget(w)
}
