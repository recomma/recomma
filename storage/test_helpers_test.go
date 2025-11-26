package storage

import (
	"context"
	"testing"

	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/stretchr/testify/require"
)

func defaultIdentifier(t *testing.T, store *Storage, ctx context.Context, oid orderid.OrderId) recomma.OrderIdentifier {
	t.Helper()
	if ctx == nil {
		ctx = context.Background()
	}
	assignment, err := store.ResolveDefaultAlias(ctx)
	require.NoError(t, err)
	return recomma.NewOrderIdentifier(assignment.VenueID, assignment.Wallet, oid)
}
