package recomma

import (
	"fmt"
	"strings"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
)

// ThreeCommasPlanTier captures the subscription tier we should honour for rate limiting.
type ThreeCommasPlanTier string

const (
	ThreeCommasPlanTierStarter ThreeCommasPlanTier = "starter"
	ThreeCommasPlanTierPro     ThreeCommasPlanTier = "pro"
	ThreeCommasPlanTierExpert  ThreeCommasPlanTier = "expert"
)

// RateLimitConfig holds tier-specific rate limiting configuration
type RateLimitConfig struct {
	RequestsPerMinute   int
	PrioritySlots       int
	DealWorkers         int
	ProduceConcurrency  int
	ResyncInterval      time.Duration
}

// DefaultThreeCommasPlanTier returns the tier assumed when secrets pre-date the tier field.
func DefaultThreeCommasPlanTier() ThreeCommasPlanTier {
	return ThreeCommasPlanTierExpert
}

// ParseThreeCommasPlanTier normalises user input into a canonical tier string.
func ParseThreeCommasPlanTier(raw string) (ThreeCommasPlanTier, error) {
	normalized := normalizeThreeCommasPlanTier(raw)
	if normalized == "" {
		return "", fmt.Errorf("threecommas plan tier is required")
	}

	switch normalized {
	case string(ThreeCommasPlanTierStarter):
		return ThreeCommasPlanTierStarter, nil
	case string(ThreeCommasPlanTierPro):
		return ThreeCommasPlanTierPro, nil
	case string(ThreeCommasPlanTierExpert):
		return ThreeCommasPlanTierExpert, nil
	default:
		return "", fmt.Errorf("invalid threecommas plan tier %q", raw)
	}
}

// ParseThreeCommasPlanTierOrDefault mirrors ParseThreeCommasPlanTier but falls back to
// DefaultThreeCommasPlanTier() when the input is empty. It reports whether the fallback
// path was taken so callers can warn about legacy payloads.
func ParseThreeCommasPlanTierOrDefault(raw string) (tier ThreeCommasPlanTier, defaulted bool, err error) {
	normalized := normalizeThreeCommasPlanTier(raw)
	if normalized == "" {
		return DefaultThreeCommasPlanTier(), true, nil
	}
	parsed, err := ParseThreeCommasPlanTier(normalized)
	return parsed, false, err
}

// SDKTier converts the tier into the SDK enum used by the 3Commas client.
func (t ThreeCommasPlanTier) SDKTier() tc.PlanTier {
	switch t {
	case ThreeCommasPlanTierStarter:
		return tc.PlanStarter
	case ThreeCommasPlanTierPro:
		return tc.PlanPro
	default:
		return tc.PlanExpert
	}
}

// RateLimitConfig returns the rate limiting configuration for this tier
func (t ThreeCommasPlanTier) RateLimitConfig() RateLimitConfig {
	switch t {
	case ThreeCommasPlanTierStarter:
		return RateLimitConfig{
			RequestsPerMinute:  5,
			PrioritySlots:      1, // 20%
			DealWorkers:        1,
			ProduceConcurrency: 1, // Sequential bot processing
			ResyncInterval:     60 * time.Second,
		}
	case ThreeCommasPlanTierPro:
		return RateLimitConfig{
			RequestsPerMinute:  50,
			PrioritySlots:      5, // 10%
			DealWorkers:        5,
			ProduceConcurrency: 10,
			ResyncInterval:     30 * time.Second,
		}
	case ThreeCommasPlanTierExpert:
		fallthrough
	default:
		return RateLimitConfig{
			RequestsPerMinute:  120,
			PrioritySlots:      10, // 8%
			DealWorkers:        25,
			ProduceConcurrency: 32,
			ResyncInterval:     15 * time.Second,
		}
	}
}

func normalizeThreeCommasPlanTier(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
