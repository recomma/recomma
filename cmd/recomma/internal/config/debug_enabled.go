//go:build debugmode

package config

import (
	"os"
	"strconv"

	"github.com/spf13/pflag"
)

func registerDebugFlag(fs *pflag.FlagSet, cfg *AppConfig) {
	fs.BoolVar(&cfg.Debug, "debug", cfg.Debug, "Enable debug mode (env: RECOMMA_DEBUG)")
}

func applyDebugEnvDefaults(flagSet map[string]struct{}, cfg *AppConfig) error {
	if _, ok := flagSet["debug"]; ok {
		return nil
	}
	if v, ok := os.LookupEnv("RECOMMA_DEBUG"); ok {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Debug = parsed
		}
	}
	return nil
}
