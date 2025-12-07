package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	hyperliquid "github.com/sonirico/go-hyperliquid"
)

// StreamSource publishes live order mutations for the SSE endpoint.
type StreamSource interface {
	Subscribe(ctx context.Context, filter StreamFilter) (<-chan StreamEvent, error)
}

type StreamFilter struct {
	OrderIdPrefix *string
	BotID         *int64
	DealID        *int64
	BotEventID    *int64
	ObservedFrom  *time.Time
}

type StreamEvent struct {
	Type             OrderLogEntryType
	OrderID          orderid.OrderId
	Identifier       *recomma.OrderIdentifier
	ObservedAt       time.Time
	BotEvent         *tc.BotEvent
	Submission       interface{}
	Status           *hyperliquid.WsOrder
	Sequence         *int64
	ScalerConfig     *EffectiveOrderScaler
	ScaledOrderAudit *ScaledOrderAudit
	Actor            *string
}

// StreamOrders satisfies StrictServerInterface.
func (h *ApiHandler) StreamOrders(ctx context.Context, req StreamOrdersRequestObject) (StreamOrdersResponseObject, error) {
	if ok, expired, sealed, err := h.requireUnsealedSession(ctx, "StreamOrders"); err != nil {
		return StreamOrders500Response{}, nil
	} else if !ok {
		switch {
		case expired:
			return StreamOrders401Response{}, nil
		case sealed:
			return StreamOrders403Response{}, nil
		default:
			return StreamOrders401Response{}, nil
		}
	}

	if h.stream == nil {
		return nil, fmt.Errorf("order streaming not configured")
	}

	filter := StreamFilter{
		OrderIdPrefix: req.Params.OrderId,
		BotID:         req.Params.BotId,
		DealID:        req.Params.DealId,
		BotEventID:    req.Params.BotEventId,
		ObservedFrom:  req.Params.ObservedFrom,
	}

	ch, err := h.stream.Subscribe(ctx, filter)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if err := h.writeSSEFrame(ctx, pw, evt); err != nil {
					h.logger.WarnContext(ctx, "write SSE frame", slog.String("error", err.Error()))
					return
				}
			}
		}
	}()

	return StreamOrders200TexteventStreamResponse{
		Body: pr,
	}, nil
}

// StreamSystemEvents satisfies StrictServerInterface.
func (h *ApiHandler) StreamSystemEvents(ctx context.Context, req StreamSystemEventsRequestObject) (StreamSystemEventsResponseObject, error) {
	if ok, expired, sealed, err := h.requireUnsealedSession(ctx, "StreamSystemEvents"); err != nil {
		return StreamSystemEvents500Response{}, nil
	} else if !ok {
		switch {
		case expired:
			return StreamSystemEvents401Response{}, nil
		case sealed:
			return StreamSystemEvents403Response{}, nil
		default:
			return StreamSystemEvents401Response{}, nil
		}
	}

	if h.systemStream == nil {
		h.logger.ErrorContext(ctx, "systemStream is nil")
		return StreamSystemEvents500Response{}, nil
	}

	eventCh, err := h.systemStream.Subscribe(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to subscribe to system stream", slog.String("error", err.Error()))
		return StreamSystemEvents500Response{}, nil
	}

	// Use custom SSE response that flushes after each write
	return streamSystemEventsSSEResponse{
		ctx:     ctx,
		eventCh: eventCh,
		handler: h,
	}, nil
}

// streamSystemEventsSSEResponse implements custom SSE streaming with explicit flushing
type streamSystemEventsSSEResponse struct {
	ctx     context.Context
	eventCh <-chan SystemEvent
	handler *ApiHandler
}

func (r streamSystemEventsSSEResponse) VisitStreamSystemEventsResponse(w http.ResponseWriter) error {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		r.handler.logger.Error("ResponseWriter does not support flushing")
		return fmt.Errorf("streaming not supported")
	}

	// CRITICAL: Write initial SSE comment and flush immediately to complete handshake
	if _, err := w.Write([]byte(": connected\n\n")); err != nil {
		r.handler.logger.WarnContext(r.ctx, "failed to write initial SSE comment", slog.String("error", err.Error()))
		return err
	}
	flusher.Flush()

	// Stream events
	for {
		select {
		case <-r.ctx.Done():
			return nil
		case evt, ok := <-r.eventCh:
			if !ok {
				return nil
			}

			if err := r.handler.writeSystemSSEFrame(w, evt); err != nil {
				r.handler.logger.WarnContext(r.ctx, "write system event frame",
					slog.String("error", err.Error()))
				return err
			}
			flusher.Flush()
		}
	}
}

func (h *ApiHandler) writeSystemSSEFrame(w io.Writer, evt SystemEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal system event: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("event: system_")
	buf.WriteString(string(evt.Level))
	buf.WriteString("\ndata: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	_, err = w.Write(buf.Bytes())
	return err
}

// StreamHyperliquidPrices satisfies StrictServerInterface.
func (h *ApiHandler) StreamHyperliquidPrices(ctx context.Context, req StreamHyperliquidPricesRequestObject) (StreamHyperliquidPricesResponseObject, error) {
	if h.prices == nil {
		if h.logger != nil {
			h.logger.Warn("StreamHyperliquidPrices requested but price source not configured")
		}
		return StreamHyperliquidPrices500Response{}, nil
	}

	coins := dedupeCoins(req.Params.Coin)
	if len(coins) == 0 {
		return StreamHyperliquidPrices400Response{}, nil
	}

	type subscription struct {
		cancel context.CancelFunc
		ch     <-chan hl.BestBidOffer
	}

	subs := make([]subscription, 0, len(coins))
	for _, coin := range coins {
		subCtx, cancel := context.WithCancel(ctx)
		stream, err := h.prices.SubscribeBBO(subCtx, coin)
		if err != nil {
			cancel()
			for _, s := range subs {
				s.cancel()
			}
			if h.logger != nil {
				h.logger.Error("StreamHyperliquidPrices subscribe failed", slog.String("coin", coin), slog.String("error", err.Error()))
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPriceSourceNotReady) {
				return StreamHyperliquidPrices500Response{}, nil
			}
			return StreamHyperliquidPrices500Response{}, nil
		}
		if h.logger != nil {
			h.logger.Debug("StreamHyperliquidPrices subscribed coin", slog.String("coin", coin))
		}
		subs = append(subs, subscription{cancel: cancel, ch: stream})
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		defer func() {
			for _, s := range subs {
				s.cancel()
			}
		}()

		updates := make(chan hl.BestBidOffer, len(subs)*4)
		var wg sync.WaitGroup

		for _, sub := range subs {
			wg.Add(1)
			s := sub
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case bbo, ok := <-s.ch:
						if !ok {
							return
						}
						select {
						case updates <- bbo:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}

		go func() {
			wg.Wait()
			close(updates)
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case bbo, ok := <-updates:
				if !ok {
					return
				}
				if err := h.writeBBOFrame(pw, bbo); err != nil {
					if h.logger != nil {
						h.logger.Warn("StreamHyperliquidPrices write frame", slog.String("coin", bbo.Coin), slog.String("error", err.Error()))
					}
					return
				}
			}
		}
	}()

	return StreamHyperliquidPrices200TexteventStreamResponse{Body: pr}, nil
}

// writeSSEFrame marshals the event payload and writes an SSE-formatted frame.
func (h *ApiHandler) writeSSEFrame(ctx context.Context, w io.Writer, evt StreamEvent) error {
	ident := makeOrderIdentifiers(evt.OrderID, evt.BotEvent, evt.ObservedAt, evt.Identifier)

	entry, ok := h.makeOrderLogEntry(ctx, evt.OrderID, evt.ObservedAt, evt.Type, evt.BotEvent, evt.Submission, evt.Status, evt.ScalerConfig, evt.ScaledOrderAudit, evt.Actor, evt.Identifier, &ident, evt.Sequence)
	if !ok {
		return nil
	}

	data, err := entry.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal stream frame: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("event: ")
	buf.WriteString(string(evt.Type))
	buf.WriteString("\n")
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write SSE payload: %w", err)
	}
	return nil
}

type bboFramePayload struct {
	Coin string            `json:"coin"`
	Time time.Time         `json:"time"`
	Bid  priceLevelPayload `json:"bid"`
	Ask  priceLevelPayload `json:"ask"`
}

func (h *ApiHandler) writeBBOFrame(w io.Writer, bbo hl.BestBidOffer) error {
	ts := bbo.Time
	if ts.IsZero() {
		if h.now != nil {
			ts = h.now()
		} else {
			ts = time.Now()
		}
	}

	payload := bboFramePayload{
		Coin: bbo.Coin,
		Time: ts.UTC(),
		Bid: priceLevelPayload{
			Price: bbo.Bid.Price,
			Size:  bbo.Bid.Size,
		},
		Ask: priceLevelPayload{
			Price: bbo.Ask.Price,
			Size:  bbo.Ask.Size,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal bbo frame: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("event: bbo\n")
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write bbo payload: %w", err)
	}

	return nil
}

func dedupeCoins(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToUpper(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}
