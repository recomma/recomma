package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewSystemStreamController(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)
	require.NotNil(t, ctrl)
	require.NotNil(t, ctrl.subscribers)
	require.Equal(t, SystemEventInfo, ctrl.minLevel)
	require.Equal(t, defaultSystemStreamBuffer, ctrl.bufferSize)
}

func TestSystemStreamController_Subscribe(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ctrl.Subscribe(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)

	// Verify subscriber was registered
	ctrl.mu.RLock()
	require.Len(t, ctrl.subscribers, 1)
	ctrl.mu.RUnlock()
}

func TestSystemStreamController_Publish(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ctrl.Subscribe(ctx)
	require.NoError(t, err)

	// Publish an event
	evt := SystemEvent{
		Level:     SystemEventInfo,
		Timestamp: time.Now().UTC(),
		Source:    "test",
		Message:   "test message",
	}
	ctrl.Publish(evt)

	// Should receive the event
	select {
	case received := <-ch:
		require.Equal(t, evt.Message, received.Message)
		require.Equal(t, evt.Source, received.Source)
		require.Equal(t, evt.Level, received.Level)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSystemStreamController_LevelFiltering(t *testing.T) {
	// Controller with Info level (should filter out debug)
	ctrl := NewSystemStreamController(SystemEventInfo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ctrl.Subscribe(ctx)
	require.NoError(t, err)

	// Publish debug event (should be filtered)
	ctrl.Publish(SystemEvent{
		Level:     SystemEventDebug,
		Timestamp: time.Now().UTC(),
		Source:    "test",
		Message:   "debug message",
	})

	// Publish info event (should pass)
	ctrl.Publish(SystemEvent{
		Level:     SystemEventInfo,
		Timestamp: time.Now().UTC(),
		Source:    "test",
		Message:   "info message",
	})

	// Should only receive info event
	select {
	case received := <-ch:
		require.Equal(t, "info message", received.Message)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for info event")
	}

	// Should not receive debug event
	select {
	case evt := <-ch:
		t.Fatalf("unexpected event received: %+v", evt)
	case <-time.After(200 * time.Millisecond):
		// Expected - no debug event
	}
}

func TestSystemStreamController_EventHistory(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)

	// Publish some events before subscribing
	for i := 0; i < 3; i++ {
		ctrl.Publish(SystemEvent{
			Level:     SystemEventInfo,
			Timestamp: time.Now().UTC(),
			Source:    "test",
			Message:   "historical event",
			Details:   map[string]interface{}{"index": i},
		})
	}

	// Wait a moment for history to be recorded
	time.Sleep(100 * time.Millisecond)

	// New subscriber should receive history
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ctrl.Subscribe(ctx)
	require.NoError(t, err)

	// Should receive historical events
	received := 0
	timeout := time.After(1 * time.Second)
	for received < 3 {
		select {
		case evt := <-ch:
			require.Equal(t, "historical event", evt.Message)
			received++
		case <-timeout:
			t.Fatalf("timeout waiting for historical events, received %d/3", received)
		}
	}
}

func TestSystemStreamController_ContextCancellation(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := ctrl.Subscribe(ctx)
	require.NoError(t, err)

	// Verify subscriber is registered
	ctrl.mu.RLock()
	initialCount := len(ctrl.subscribers)
	ctrl.mu.RUnlock()
	require.Equal(t, 1, initialCount)

	// Cancel context
	cancel()

	// Give time for cleanup
	time.Sleep(100 * time.Millisecond)

	// Subscriber should be unregistered
	ctrl.mu.RLock()
	finalCount := len(ctrl.subscribers)
	ctrl.mu.RUnlock()
	require.Equal(t, 0, finalCount)

	// Channel should be closed
	_, ok := <-ch
	require.False(t, ok, "channel should be closed")
}

func TestSystemStreamController_MultipleSubscribers(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	ch1, err := ctrl.Subscribe(ctx1)
	require.NoError(t, err)

	ch2, err := ctrl.Subscribe(ctx2)
	require.NoError(t, err)

	// Publish event
	evt := SystemEvent{
		Level:     SystemEventInfo,
		Timestamp: time.Now().UTC(),
		Source:    "test",
		Message:   "broadcast message",
	}
	ctrl.Publish(evt)

	// Both subscribers should receive
	select {
	case received := <-ch1:
		require.Equal(t, "broadcast message", received.Message)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout on subscriber 1")
	}

	select {
	case received := <-ch2:
		require.Equal(t, "broadcast message", received.Message)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout on subscriber 2")
	}
}

func TestSystemStreamController_Flush(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ctrl.Subscribe(ctx)
	require.NoError(t, err)

	// Flush all subscribers
	ctrl.Flush()

	// Channel should be closed
	_, ok := <-ch
	require.False(t, ok, "channel should be closed after flush")

	// No subscribers should remain
	ctrl.mu.RLock()
	count := len(ctrl.subscribers)
	ctrl.mu.RUnlock()
	require.Equal(t, 0, count)
}

func TestSystemStreamController_HistoryPruning(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)
	ctrl.maxHistorySize = 5 // Small size for testing

	// Publish more events than max history
	for i := 0; i < 10; i++ {
		ctrl.Publish(SystemEvent{
			Level:     SystemEventInfo,
			Timestamp: time.Now().UTC(),
			Source:    "test",
			Message:   "event",
			Details:   map[string]interface{}{"index": i},
		})
	}

	// History should be pruned to max size
	ctrl.mu.RLock()
	historySize := len(ctrl.eventHistory)
	ctrl.mu.RUnlock()

	require.LessOrEqual(t, historySize, ctrl.maxHistorySize, "history should be pruned")
}

func TestSystemStreamController_HistoryAgeFiltering(t *testing.T) {
	ctrl := NewSystemStreamController(SystemEventInfo)
	ctrl.maxHistoryAge = 100 * time.Millisecond

	// Publish old event
	oldEvt := SystemEvent{
		Level:     SystemEventInfo,
		Timestamp: time.Now().UTC().Add(-200 * time.Millisecond),
		Source:    "test",
		Message:   "old event",
	}
	ctrl.mu.Lock()
	ctrl.addToHistory(oldEvt)
	ctrl.mu.Unlock()

	// Wait for it to age out
	time.Sleep(150 * time.Millisecond)

	// Publish new event (triggers age pruning)
	ctrl.Publish(SystemEvent{
		Level:     SystemEventInfo,
		Timestamp: time.Now().UTC(),
		Source:    "test",
		Message:   "new event",
	})

	// Get history
	ctrl.mu.RLock()
	history := ctrl.getRecentHistory()
	ctrl.mu.RUnlock()

	// Old event should not be in recent history
	for _, evt := range history {
		require.NotEqual(t, "old event", evt.Message, "old event should be filtered from history")
	}
}

func TestSystemEvent_Levels(t *testing.T) {
	tests := []struct {
		minLevel      SystemEventLevel
		publishLevel  SystemEventLevel
		shouldReceive bool
	}{
		{SystemEventDebug, SystemEventDebug, true},
		{SystemEventDebug, SystemEventInfo, true},
		{SystemEventDebug, SystemEventWarn, true},
		{SystemEventDebug, SystemEventError, true},
		{SystemEventInfo, SystemEventDebug, false},
		{SystemEventInfo, SystemEventInfo, true},
		{SystemEventWarn, SystemEventInfo, false},
		{SystemEventWarn, SystemEventWarn, true},
		{SystemEventError, SystemEventWarn, false},
		{SystemEventError, SystemEventError, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.minLevel)+"_"+string(tt.publishLevel), func(t *testing.T) {
			ctrl := NewSystemStreamController(tt.minLevel)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ch, err := ctrl.Subscribe(ctx)
			require.NoError(t, err)

			ctrl.Publish(SystemEvent{
				Level:     tt.publishLevel,
				Timestamp: time.Now().UTC(),
				Source:    "test",
				Message:   "test",
			})

			select {
			case <-ch:
				require.True(t, tt.shouldReceive, "unexpected event received")
			case <-time.After(200 * time.Millisecond):
				require.False(t, tt.shouldReceive, "expected event not received")
			}
		})
	}
}

func TestSystemSubscriber_TrySend(t *testing.T) {
	t.Run("successful send", func(t *testing.T) {
		sub := &systemSubscriber{
			ch:     make(chan SystemEvent, 1),
			ctx:    context.Background(),
			closed: false,
		}

		evt := SystemEvent{
			Level:   SystemEventInfo,
			Message: "test",
		}

		sent, closed := sub.trySend(evt)
		require.True(t, sent)
		require.False(t, closed)
	})

	t.Run("buffer full", func(t *testing.T) {
		sub := &systemSubscriber{
			ch:     make(chan SystemEvent, 0), // Zero buffer
			ctx:    context.Background(),
			closed: false,
		}

		evt := SystemEvent{
			Level:   SystemEventInfo,
			Message: "test",
		}

		sent, closed := sub.trySend(evt)
		require.False(t, sent)
		require.False(t, closed)
	})

	t.Run("closed subscriber", func(t *testing.T) {
		sub := &systemSubscriber{
			ch:     make(chan SystemEvent, 1),
			ctx:    context.Background(),
			closed: true,
		}

		evt := SystemEvent{
			Level:   SystemEventInfo,
			Message: "test",
		}

		sent, closed := sub.trySend(evt)
		require.False(t, sent)
		require.True(t, closed)
	})
}

func TestSystemSubscriber_Close(t *testing.T) {
	t.Run("close once", func(t *testing.T) {
		sub := &systemSubscriber{
			ch:     make(chan SystemEvent, 1),
			ctx:    context.Background(),
			closed: false,
		}

		sub.close()

		require.True(t, sub.closed)
		_, ok := <-sub.ch
		require.False(t, ok, "channel should be closed")
	})

	t.Run("close twice (idempotent)", func(t *testing.T) {
		sub := &systemSubscriber{
			ch:     make(chan SystemEvent, 1),
			ctx:    context.Background(),
			closed: false,
		}

		sub.close()
		sub.close() // Should not panic

		require.True(t, sub.closed)
	})
}
