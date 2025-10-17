package config

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sonirico/go-hyperliquid"
	"github.com/spf13/pflag"

	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/hl"
)

type AppConfig struct {
	ThreeCommas tc.ClientConfig
	Hyperliquid hl.ClientConfig

	tcPrivateRaw  string
	tcPrivateFile string

	StoragePath    string
	DealWorkers    int
	OrderWorkers   int
	ResyncInterval time.Duration
	HTTPListen     string
	LogLevel       string
	LogFormatJSON  bool
}

func DefaultConfig() AppConfig {
	return AppConfig{
		// HyperliquidAPIURL: hyperliquid.TestnetAPIURL,
		Hyperliquid:    hl.ClientConfig{BaseURL: hyperliquid.TestnetAPIURL},
		StoragePath:    "db.sqlite3",
		DealWorkers:    25,
		OrderWorkers:   5,
		ResyncInterval: 15 * time.Second,
		HTTPListen:     ":8080",
		LogLevel:       "info",
		LogFormatJSON:  false,
	}
}

// NewConfigFlagSet declares the flags against the provided struct but does not parse.
func NewConfigFlagSet(cfg *AppConfig) *pflag.FlagSet {
	fs := pflag.NewFlagSet("recomma", pflag.ContinueOnError)
	fs.SortFlags = false

	fs.StringVar(&cfg.ThreeCommas.APIKey, "threecommas-api-key", cfg.ThreeCommas.APIKey, "3Commas API key (env: THREECOMMAS_API_KEY)")
	fs.StringVar(&cfg.tcPrivateRaw, "threecommas-private-key", cfg.tcPrivateRaw, "3Commas private key PEM (env: THREECOMMAS_PRIVATE_KEY)")
	fs.StringVar(&cfg.tcPrivateFile, "threecommas-private-key-file", cfg.tcPrivateFile, "3Commas private key PEM file (env: THREECOMMAS_PRIVATE_KEY_FILE). Overrides threecommas-private-key if set.")
	fs.StringVar(&cfg.ThreeCommas.BaseURL, "threecommas-api-url", cfg.ThreeCommas.BaseURL, "3Commas API base URL (env: THREECOMMAS_API_URL)")

	fs.StringVar(&cfg.Hyperliquid.Wallet, "hyperliquid-wallet", cfg.Hyperliquid.Wallet, "Hyperliquid wallet address (env: HYPERLIQUID_WALLET)")
	fs.StringVar(&cfg.Hyperliquid.Key, "hyperliquid-private-key", cfg.Hyperliquid.Key, "Hyperliquid private key (env: HYPERLIQUID_PRIVATE_KEY)")
	fs.StringVar(&cfg.Hyperliquid.BaseURL, "hyperliquid-api-url", cfg.Hyperliquid.BaseURL, "Hyperliquid API base URL (env: HYPERLIQUID_API_URL)")

	fs.StringVar(&cfg.StoragePath, "storage-path", cfg.StoragePath, "Badger storage path (env: RECOMMA_STORAGE_PATH)")
	fs.IntVar(&cfg.DealWorkers, "deal-workers", cfg.DealWorkers, "Number of deal-processing workers (env: RECOMMA_DEAL_WORKERS)")
	fs.IntVar(&cfg.OrderWorkers, "order-workers", cfg.OrderWorkers, "Number of order emit workers (env: RECOMMA_ORDER_WORKERS)")
	fs.DurationVar(&cfg.ResyncInterval, "resync-interval", cfg.ResyncInterval, "Interval between deal resyncs (env: RECOMMA_RESYNC_INTERVAL)")
	fs.StringVar(&cfg.HTTPListen, "http-listen", cfg.HTTPListen, "HTTP listen address (env: RECOMMA_HTTP_LISTEN)")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (env: RECOMMA_LOG_LEVEL)")
	fs.BoolVar(&cfg.LogFormatJSON, "log-json", cfg.LogFormatJSON, "Emit logs as JSON (env: RECOMMA_LOG_JSON)")

	return fs
}

// applyEnvDefaults inspects flags that were left at their zero value and pulls from env.
func ApplyEnvDefaults(fs *pflag.FlagSet, cfg *AppConfig) error {
	flagSet := map[string]struct{}{}
	fs.Visit(func(f *pflag.Flag) { flagSet[f.Name] = struct{}{} })

	setString := func(name, envKey string, target *string) {
		if _, ok := flagSet[name]; ok {
			return
		}
		if v, ok := os.LookupEnv(envKey); ok && v != "" {
			*target = v
		}
	}
	setInt := func(name, envKey string, target *int) {
		if _, ok := flagSet[name]; ok {
			return
		}
		if v, ok := os.LookupEnv(envKey); ok {
			if parsed, err := strconv.Atoi(v); err == nil {
				*target = parsed
			}
		}
	}
	setBool := func(name, envKey string, target *bool) {
		if _, ok := flagSet[name]; ok {
			return
		}
		if v, ok := os.LookupEnv(envKey); ok {
			if parsed, err := strconv.ParseBool(v); err == nil {
				*target = parsed
			}
		}
	}
	setDuration := func(name, envKey string, target *time.Duration) {
		if _, ok := flagSet[name]; ok {
			return
		}
		if v, ok := os.LookupEnv(envKey); ok {
			if parsed, err := time.ParseDuration(v); err == nil {
				*target = parsed
			}
		}
	}

	setString("threecommas-api-key", "THREECOMMAS_API_KEY", &cfg.ThreeCommas.APIKey)
	setString("threecommas-private-key", "THREECOMMAS_PRIVATE_KEY", &cfg.tcPrivateRaw)
	setString("threecommas-private-key-file", "THREECOMMAS_PRIVATE_KEY_FILE", &cfg.tcPrivateFile)
	setString("threecommas-api-url", "THREECOMMAS_API_URL", &cfg.ThreeCommas.BaseURL)

	setString("hyperliquid-wallet", "HYPERLIQUID_WALLET", &cfg.Hyperliquid.Wallet)
	setString("hyperliquid-private-key", "HYPERLIQUID_PRIVATE_KEY", &cfg.Hyperliquid.Key)
	setString("hyperliquid-api-url", "HYPERLIQUID_API_URL", &cfg.Hyperliquid.BaseURL)

	setString("storage-path", "RECOMMA_STORAGE_PATH", &cfg.StoragePath)
	setInt("deal-workers", "RECOMMA_DEAL_WORKERS", &cfg.DealWorkers)
	setInt("order-workers", "RECOMMA_ORDER_WORKERS", &cfg.OrderWorkers)
	setDuration("resync-interval", "RECOMMA_RESYNC_INTERVAL", &cfg.ResyncInterval)
	setString("http-listen", "RECOMMA_HTTP_LISTEN", &cfg.HTTPListen)
	setString("log-level", "RECOMMA_LOG_LEVEL", &cfg.LogLevel)
	setBool("log-json", "RECOMMA_LOG_JSON", &cfg.LogFormatJSON)

	if cfg.tcPrivateFile != "" {
		pem, err := os.ReadFile(cfg.tcPrivateFile)
		if err != nil {
			return fmt.Errorf("reading threecommas private key from %q: %w", cfg.tcPrivateFile, err)
		}
		cfg.ThreeCommas.PrivatePEM = pem
	} else {
		cfg.ThreeCommas.PrivatePEM = []byte(cfg.tcPrivateRaw)
	}
	return nil
}

func ValidateConfig(cfg AppConfig) error {
	var missing []string
	if cfg.ThreeCommas.APIKey == "" {
		missing = append(missing, "threecommas-api-key")
	}
	if len(cfg.ThreeCommas.PrivatePEM) == 0 {
		missing = append(missing, "threecommas-private-key or threecommas-private-key-file")
	}
	if cfg.Hyperliquid.Wallet == "" {
		missing = append(missing, "hyperliquid-wallet")
	}
	if strings.TrimSpace(cfg.Hyperliquid.Key) == "" {
		missing = append(missing, "hyperliquid-private-key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func GetLogHandler(cfg AppConfig) slog.Handler {
	var level slog.Level
	if cfg.LogLevel == "" {
		level = slog.LevelInfo
	} else if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
		log.Printf("unknown log level %q, defaulting to info", cfg.LogLevel)
	}

	handlerOpts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.LogFormatJSON {
		handler = slog.NewJSONHandler(os.Stderr, handlerOpts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, handlerOpts)
	}

	return handler
}
