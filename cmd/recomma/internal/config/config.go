package config

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

type AppConfig struct {
	StoragePath                    string
	DealWorkers                    int
	OrderWorkers                   int
	ResyncInterval                 time.Duration
	HTTPListen                     string
	PublicOrigin                   string
	LogLevel                       string
	LogFormatJSON                  bool
	Debug                          bool
	HyperliquidIOCInitialOffsetBps float64
	OrderScalerMaxMultiplier       float64
}

func DefaultConfig() AppConfig {
	return AppConfig{
		StoragePath:                    "db.sqlite3",
		DealWorkers:                    25,
		OrderWorkers:                   5,
		ResyncInterval:                 15 * time.Second,
		HTTPListen:                     ":8080",
		LogLevel:                       "info",
		LogFormatJSON:                  false,
		Debug:                          false,
		HyperliquidIOCInitialOffsetBps: 0,
		OrderScalerMaxMultiplier:       5,
	}
}

// NewConfigFlagSet declares the flags against the provided struct but does not parse.
func NewConfigFlagSet(cfg *AppConfig) *pflag.FlagSet {
	fs := pflag.NewFlagSet("recomma", pflag.ContinueOnError)
	fs.SortFlags = false

	fs.StringVar(&cfg.StoragePath, "storage-path", cfg.StoragePath, "SQLite3 storage path (env: RECOMMA_STORAGE_PATH)")
	fs.IntVar(&cfg.DealWorkers, "deal-workers", cfg.DealWorkers, "Number of deal-processing workers (env: RECOMMA_DEAL_WORKERS)")
	fs.IntVar(&cfg.OrderWorkers, "order-workers", cfg.OrderWorkers, "Number of order emit workers (env: RECOMMA_ORDER_WORKERS)")
	fs.DurationVar(&cfg.ResyncInterval, "resync-interval", cfg.ResyncInterval, "Interval between deal resyncs (env: RECOMMA_RESYNC_INTERVAL)")
	fs.StringVar(&cfg.HTTPListen, "http-listen", cfg.HTTPListen, "HTTP listen address (env: RECOMMA_HTTP_LISTEN)")
	fs.StringVar(&cfg.PublicOrigin, "public-origin", cfg.PublicOrigin, "Public origin served to clients (env: RECOMMA_PUBLIC_ORIGIN)")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (env: RECOMMA_LOG_LEVEL)")
	fs.BoolVar(&cfg.LogFormatJSON, "log-json", cfg.LogFormatJSON, "Emit logs as JSON (env: RECOMMA_LOG_JSON)")
	fs.BoolVar(&cfg.Debug, "debug", cfg.Debug, "Enable debug mode (env: RECOMMA_DEBUG)")
	fs.Float64Var(&cfg.HyperliquidIOCInitialOffsetBps, "hyperliquid-ioc-offset-bps", cfg.HyperliquidIOCInitialOffsetBps, "Basis points to widen the first IOC price check (env: RECOMMA_HYPERLIQUID_IOC_OFFSET_BPS)")
	fs.Float64Var(&cfg.OrderScalerMaxMultiplier, "order-scaler-max-multiplier", cfg.OrderScalerMaxMultiplier, "Maximum allowed order scaler multiplier (env: RECOMMA_ORDER_SCALER_MAX_MULTIPLIER)")

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
	setFloat := func(name, envKey string, target *float64) {
		if _, ok := flagSet[name]; ok {
			return
		}
		if v, ok := os.LookupEnv(envKey); ok {
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
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

	setString("storage-path", "RECOMMA_STORAGE_PATH", &cfg.StoragePath)
	setInt("deal-workers", "RECOMMA_DEAL_WORKERS", &cfg.DealWorkers)
	setInt("order-workers", "RECOMMA_ORDER_WORKERS", &cfg.OrderWorkers)
	setDuration("resync-interval", "RECOMMA_RESYNC_INTERVAL", &cfg.ResyncInterval)
	setString("http-listen", "RECOMMA_HTTP_LISTEN", &cfg.HTTPListen)
	setString("public-origin", "RECOMMA_PUBLIC_ORIGIN", &cfg.PublicOrigin)
	setString("log-level", "RECOMMA_LOG_LEVEL", &cfg.LogLevel)
	setBool("log-json", "RECOMMA_LOG_JSON", &cfg.LogFormatJSON)
	setBool("debug", "RECOMMA_DEBUG", &cfg.Debug)
	setFloat("hyperliquid-ioc-offset-bps", "RECOMMA_HYPERLIQUID_IOC_OFFSET_BPS", &cfg.HyperliquidIOCInitialOffsetBps)
	setFloat("order-scaler-max-multiplier", "RECOMMA_ORDER_SCALER_MAX_MULTIPLIER", &cfg.OrderScalerMaxMultiplier)

	return nil
}

func ValidateConfig(cfg AppConfig) error {
	var missing []string

	if cfg.OrderScalerMaxMultiplier <= 0 {
		return fmt.Errorf("order scaler max multiplier must be positive")
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
