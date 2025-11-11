package recomma

import (
	"testing"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/stretchr/testify/require"
)

func TestParseThreeCommasPlanTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected ThreeCommasPlanTier
		err      string
	}{
		{name: "starter lowercase", input: "starter", expected: ThreeCommasPlanTierStarter},
		{name: "pro mixed case", input: " Pro ", expected: ThreeCommasPlanTierPro},
		{name: "expert uppercase", input: "EXPERT", expected: ThreeCommasPlanTierExpert},
		{name: "missing", input: "   ", err: "required"},
		{name: "invalid", input: "gold", err: "invalid"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseThreeCommasPlanTier(tt.input)
			if tt.err != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestParseThreeCommasPlanTierOrDefault(t *testing.T) {
	tier, defaulted, err := ParseThreeCommasPlanTierOrDefault("")
	require.NoError(t, err)
	require.True(t, defaulted)
	require.Equal(t, DefaultThreeCommasPlanTier(), tier)

	tier, defaulted, err = ParseThreeCommasPlanTierOrDefault("starter")
	require.NoError(t, err)
	require.False(t, defaulted)
	require.Equal(t, ThreeCommasPlanTierStarter, tier)
}

func TestSDKTier(t *testing.T) {
	require.Equal(t, tc.PlanStarter, ThreeCommasPlanTierStarter.SDKTier())
	require.Equal(t, tc.PlanPro, ThreeCommasPlanTierPro.SDKTier())
	require.Equal(t, tc.PlanExpert, ThreeCommasPlanTierExpert.SDKTier())
	require.Equal(t, tc.PlanExpert, ThreeCommasPlanTier("unknown").SDKTier())
}
