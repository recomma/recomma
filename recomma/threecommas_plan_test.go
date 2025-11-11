package recomma

import (
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
)

func TestParseThreeCommasPlanTier(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ThreeCommasPlanTier
		wantErr bool
	}{
		{
			name:    "starter lowercase",
			input:   "starter",
			want:    ThreeCommasPlanTierStarter,
			wantErr: false,
		},
		{
			name:    "starter uppercase",
			input:   "STARTER",
			want:    ThreeCommasPlanTierStarter,
			wantErr: false,
		},
		{
			name:    "starter mixed case",
			input:   "StArTeR",
			want:    ThreeCommasPlanTierStarter,
			wantErr: false,
		},
		{
			name:    "pro lowercase",
			input:   "pro",
			want:    ThreeCommasPlanTierPro,
			wantErr: false,
		},
		{
			name:    "expert lowercase",
			input:   "expert",
			want:    ThreeCommasPlanTierExpert,
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "invalid tier",
			input:   "premium",
			want:    "",
			wantErr: true,
		},
		{
			name:    "whitespace trimmed",
			input:   "  starter  ",
			want:    ThreeCommasPlanTierStarter,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseThreeCommasPlanTier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseThreeCommasPlanTier() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseThreeCommasPlanTier() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseThreeCommasPlanTierOrDefault(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		want            ThreeCommasPlanTier
		wantDefaulted   bool
		wantErr         bool
	}{
		{
			name:          "starter",
			input:         "starter",
			want:          ThreeCommasPlanTierStarter,
			wantDefaulted: false,
			wantErr:       false,
		},
		{
			name:          "empty string defaults to expert",
			input:         "",
			want:          ThreeCommasPlanTierExpert,
			wantDefaulted: true,
			wantErr:       false,
		},
		{
			name:          "invalid tier",
			input:         "invalid",
			want:          "",
			wantDefaulted: false,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, defaulted, err := ParseThreeCommasPlanTierOrDefault(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseThreeCommasPlanTierOrDefault() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseThreeCommasPlanTierOrDefault() tier = %v, want %v", got, tt.want)
			}
			if defaulted != tt.wantDefaulted {
				t.Errorf("ParseThreeCommasPlanTierOrDefault() defaulted = %v, want %v", defaulted, tt.wantDefaulted)
			}
		})
	}
}

func TestThreeCommasPlanTier_SDKTier(t *testing.T) {
	tests := []struct {
		name string
		tier ThreeCommasPlanTier
		want tc.PlanTier
	}{
		{
			name: "starter",
			tier: ThreeCommasPlanTierStarter,
			want: tc.PlanStarter,
		},
		{
			name: "pro",
			tier: ThreeCommasPlanTierPro,
			want: tc.PlanPro,
		},
		{
			name: "expert",
			tier: ThreeCommasPlanTierExpert,
			want: tc.PlanExpert,
		},
		{
			name: "unknown defaults to expert",
			tier: ThreeCommasPlanTier("unknown"),
			want: tc.PlanExpert,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tier.SDKTier(); got != tt.want {
				t.Errorf("SDKTier() = %v, want %v", got, tt.want)
			}
		})
	}
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
