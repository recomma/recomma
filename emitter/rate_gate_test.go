package emitter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRateGateEnforcesSpacing(t *testing.T) {
	gate := NewRateGate(30 * time.Millisecond)

	start := time.Now()
	require.NoError(t, gate.Wait(context.Background()))
	require.NoError(t, gate.Wait(context.Background()))

	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, 30*time.Millisecond, "expected gate to enforce minimum spacing")
}

func TestRateGateRespectsContext(t *testing.T) {
	gate := NewRateGate(100 * time.Millisecond)
	require.NoError(t, gate.Wait(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := gate.Wait(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRateGateCooldownExtendsDelay(t *testing.T) {
	gate := NewRateGate(5 * time.Millisecond)
	require.NoError(t, gate.Wait(context.Background()))

	gate.Cooldown(40 * time.Millisecond)

	start := time.Now()
	require.NoError(t, gate.Wait(context.Background()))
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, 40*time.Millisecond, "expected cooldown to extend wait duration")
}
