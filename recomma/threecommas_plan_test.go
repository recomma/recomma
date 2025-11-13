package recomma

import (
	"testing"
	"time"

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

func TestThreeCommasPlanTier_RateLimitConfig(t *testing.T) {
	tests := []struct {
		name                   string
		tier                   ThreeCommasPlanTier
		wantRequestsPerMinute  int
		wantPrioritySlots      int
		wantDealWorkers        int
		wantProduceConcurrency int
		wantResyncInterval     time.Duration
	}{
		{
			name:                   "starter tier",
			tier:                   ThreeCommasPlanTierStarter,
			wantRequestsPerMinute:  5,
			wantPrioritySlots:      1,
			wantDealWorkers:        1,
			wantProduceConcurrency: 1,
			wantResyncInterval:     60 * time.Second,
		},
		{
			name:                   "pro tier",
			tier:                   ThreeCommasPlanTierPro,
			wantRequestsPerMinute:  50,
			wantPrioritySlots:      5,
			wantDealWorkers:        5,
			wantProduceConcurrency: 10,
			wantResyncInterval:     30 * time.Second,
		},
		{
			name:                   "expert tier",
			tier:                   ThreeCommasPlanTierExpert,
			wantRequestsPerMinute:  120,
			wantPrioritySlots:      10,
			wantDealWorkers:        25,
			wantProduceConcurrency: 32,
			wantResyncInterval:     15 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.tier.RateLimitConfig()

			if cfg.RequestsPerMinute != tt.wantRequestsPerMinute {
				t.Errorf("RequestsPerMinute = %v, want %v", cfg.RequestsPerMinute, tt.wantRequestsPerMinute)
			}
			if cfg.PrioritySlots != tt.wantPrioritySlots {
				t.Errorf("PrioritySlots = %v, want %v", cfg.PrioritySlots, tt.wantPrioritySlots)
			}
			if cfg.DealWorkers != tt.wantDealWorkers {
				t.Errorf("DealWorkers = %v, want %v", cfg.DealWorkers, tt.wantDealWorkers)
			}
			if cfg.ProduceConcurrency != tt.wantProduceConcurrency {
				t.Errorf("ProduceConcurrency = %v, want %v", cfg.ProduceConcurrency, tt.wantProduceConcurrency)
			}
			if cfg.ResyncInterval != tt.wantResyncInterval {
				t.Errorf("ResyncInterval = %v, want %v", cfg.ResyncInterval, tt.wantResyncInterval)
			}
		})
	}
}

func TestDefaultThreeCommasPlanTier(t *testing.T) {
	want := ThreeCommasPlanTierExpert
	got := DefaultThreeCommasPlanTier()
	if got != want {
		t.Errorf("DefaultThreeCommasPlanTier() = %v, want %v", got, want)
	}
}

// Test that starter tier has reasonable restrictions
func TestStarterTierRestrictions(t *testing.T) {
	cfg := ThreeCommasPlanTierStarter.RateLimitConfig()

	if cfg.DealWorkers != 1 {
		t.Errorf("Starter tier should have 1 DealWorker, got %d", cfg.DealWorkers)
	}
	if cfg.ProduceConcurrency != 1 {
		t.Errorf("Starter tier should have sequential bot processing (1), got %d", cfg.ProduceConcurrency)
	}
	if cfg.RequestsPerMinute != 5 {
		t.Errorf("Starter tier should have 5 req/min, got %d", cfg.RequestsPerMinute)
	}
	if cfg.ResyncInterval < 60*time.Second {
		t.Errorf("Starter tier should have at least 60s resync interval, got %v", cfg.ResyncInterval)
	}
}

// Test that priority slots are a reasonable percentage of total quota
func TestPrioritySlotsPercentage(t *testing.T) {
	tiers := []ThreeCommasPlanTier{
		ThreeCommasPlanTierStarter,
		ThreeCommasPlanTierPro,
		ThreeCommasPlanTierExpert,
	}

	for _, tier := range tiers {
		cfg := tier.RateLimitConfig()
		pct := float64(cfg.PrioritySlots) * 100 / float64(cfg.RequestsPerMinute)

		// Priority slots should be between 5% and 25% of total quota
		if pct < 5 || pct > 25 {
			t.Errorf("%s tier has %d priority slots out of %d total (%.1f%%), expected 5-25%%",
				tier, cfg.PrioritySlots, cfg.RequestsPerMinute, pct)
		}
	}
}
