package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/storage"
	"golang.org/x/sync/semaphore"

	badger "github.com/dgraph-io/badger/v4"
)

type marketOrdersCache struct {
	Data map[metadata]threecommas.MarketOrder
	mu   sync.RWMutex
}

func NewMarketOrdersCache() *marketOrdersCache {
	return &marketOrdersCache{
		Data: make(map[metadata]threecommas.MarketOrder),
	}
}

func (c *marketOrdersCache) Add(md metadata, order threecommas.MarketOrder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Data[md] = order
}

func main() {
	moc := NewMarketOrdersCache()
	sem := semaphore.NewWeighted(25)

	storage, err := storage.NewStorage("badger")
	if err != nil {
		log.Fatal(err)
	}
	defer storage.Close()

	client, err := threecommas.New3CommasClient(config)
	if err != nil {
		log.Fatalf("Could not create client: %s", err)
	}

	listBotsOpts := []threecommas.ListBotsParamsOption{
		threecommas.WithScopeForListBots(threecommas.Enabled),
	}

	// let's load the last three months
	n := 0
	for n <= 2 {
		prefix := time.Now().AddDate(0, -n, 0).Format("2006-01")
		err = storage.LoadSeenKeys([]byte(prefix))
		if err != nil {
			log.Fatalf("cannot get seen hashes: %s", err)
		}
		n++
	}

	bots, err := client.ListBots(context.Background(), listBotsOpts...)
	if err != nil {
		apiErr := &threecommas.APIError{}
		if errors.As(err, &apiErr) {
			log.Fatalf("Could not list bots: %s\n%s", err, apiErr.ErrorPayload)
		} else {
			log.Fatalf("Could not list bots: %s", err)
		}
	}

	var wg sync.WaitGroup

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{
		"ID",
		"Name",
		"Trades",
		"PnL",
		"Avg. daily PnL",
		"Exchange",
		"Pair",
		"Status",
	})

	for _, bot := range bots {
		t.AppendRow(table.Row{
			bot.Id,
			*bot.Name,
			bot.FinishedDealsCount,
			bot.FinishedDealsProfitUsd,
			"",              // ?? no idea which
			bot.AccountName, // query Account info to get the Exchange info
			bot.Pairs,
			fmt.Sprintf("%d / %d", bot.ActiveDealsCount, bot.MaxActiveDeals),
			bot.IsEnabled,
		})
	}
	t.Render()

	for _, bot := range bots {
		deals, err := client.GetListOfDeals(context.Background(), threecommas.WithBotIdForListDeals(bot.Id))

		if err != nil {
			log.Fatalf("Could not list deals for bot %d: %s", bot.Id, err)
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.AppendHeader(table.Row{
			"Deals for Bot",
			bot.Id,
		})
		t.AppendSeparator()
		t.AppendHeader(table.Row{
			"ID", "Pair", "Created", "Completed S.O.", "Active S.O.", "Max S.O.", "Profit %", "Profit (amt)", "Bought Vol.", "Bought Amt.", "Status",
		})

		for _, d := range deals {
			actualProfit, err := d.ActualProfit.Get()
			if err != nil {
				log.Printf("actual profit was not set: %s", err)
			}
			t.AppendRow(table.Row{
				d.Id,
				d.Pair, // e.g. "DOGE/USDT"
				d.CreatedAt.Format("2006-01-02 15:04:05"), // your preferred layout
				d.CompletedSafetyOrdersCount,
				d.CurrentActiveSafetyOrdersCount,
				d.MaxSafetyOrders,
				d.ActualProfitPercentage, // e.g. "+1.07%"
				actualProfit,             // e.g. "+$0.16"
				d.BoughtVolume,           // e.g. "16.58921 USDT"
				d.BoughtAmount,           // e.g. "80.000000 DOGE"
				d.Status,                 // e.g. "active"
			})
		}

		t.Render()

		for _, deal := range deals {

			wg.Add(1)
			go func() {
				if err := sem.Acquire(context.Background(), 1); err != nil {
					log.Printf("semaphore acquire failed: %v", err)
					return
				}
				defer sem.Release(1)
				defer wg.Done()
				orders, err := client.GetMarketOrdersForDeal(context.Background(), deal.Id)

				if err != nil {
					log.Fatalf("Could not list market orders for deal %d: %s", deal.Id, err)
				}

				for _, order := range orders {
					md := metadata{
						BotID:     deal.BotId,
						DealID:    deal.Id,
						OrderID:   order.OrderId,
						CreatedAt: order.CreatedAt,
					}

					moc.Add(md, order)
				}
			}()
		}
	}

	wg.Wait()

	// we now have all unprocessed MarketOrders

	// unprocessed := unprocessedMarketOrders(db, md.BotId, md.DealID, orders)
	// unprocessed := unprocessedMarketOrdersMap(db, moc.Data)

	log.Printf("Seen %d", storage.Size())

	t = table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Key", "DealID", "ID", "Side", "Order Type", "Status", "Rate", "Amount", "Volume", "Created", "Updated"})

	for md, order := range moc.Data {
		if storage.SeenKey([]byte(md.String())) {
			continue
		}

		data, err := json.Marshal(order)
		if err != nil {
			log.Fatalf("Could not json marshal MarketOrder: %s", err)
		}
		err = storage.Add(md.String(), data)
		if err != nil {
			log.Fatalf("Could not add MarketOrder to Storage: %s", err)
		}

		t.AppendRow(table.Row{
			md.String(),
			md.DealID,
			order.OrderId,
			order.OrderType,
			order.DealOrderType,
			order.StatusString,
			order.Rate,
			order.Quantity,
			order.Total,
			order.CreatedAt,
			order.UpdatedAt,
		})
	}

	t.Render()

	log.Printf("MarketOrders %d", len(moc.Data))
}

type metadata struct {
	BotID     int
	DealID    int
	OrderID   string
	CreatedAt time.Time
}

func (md *metadata) String() string {
	return fmt.Sprintf("%s::%d::%d::%s", md.CreatedAt.Format("2006-01-02"), md.BotID, md.DealID, md.OrderID)
}

func unprocessedMarketOrdersMap(db *badger.DB, orders map[metadata]threecommas.MarketOrder) map[metadata]threecommas.MarketOrder {
	txn := db.NewTransaction(false)
	defer txn.Discard()

	unprocessed := make(map[metadata]threecommas.MarketOrder)
	for md, order := range orders {
		// key := fmt.Sprintf("%d::%d::%s", botId, dealId, order.OrderId)
		log.Printf("Looking for %s\n", md.String())
		_, err := txn.Get([]byte(md.String()))

		switch err {
		case badger.ErrKeyNotFound:
			// unprocessed = append(unprocessed, order)
			unprocessed[md] = order
			log.Printf("Unprocessed %s\n", md.String())

		case nil:
			continue

		default:
			log.Printf("Unexpected error for key %s: %s", md.String(), err)
		}
	}

	return unprocessed
}

func unprocessedMarketOrders(db *badger.DB, botId, dealId int, orders []threecommas.MarketOrder) []threecommas.MarketOrder {
	txn := db.NewTransaction(false)
	defer txn.Discard()

	var unprocessed []threecommas.MarketOrder
	for _, order := range orders {
		key := fmt.Sprintf("%d::%d::%s", botId, dealId, order.OrderId)
		// log.Printf("Looking for %s\n", key)
		_, err := txn.Get([]byte(key))

		switch err {
		case badger.ErrKeyNotFound:
			unprocessed = append(unprocessed, order)

		case nil:
			continue

		default:
			log.Printf("Unexpected error for key %s: %s", key, err)
		}
	}

	return unprocessed
}
