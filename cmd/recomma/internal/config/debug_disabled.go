//go:build !debugmode

package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/pflag"
)

type disabledDebugValue struct {
	underlying pflag.Value
	target     *bool
}

func (v *disabledDebugValue) Set(s string) error {
	if s == "" {
		s = "true"
	}
	if err := v.underlying.Set(s); err != nil {
		return err
	}
	parsed, err := strconv.ParseBool(v.underlying.String())
	if err != nil {
		return err
	}
	if parsed {
		*v.target = false
		return fmt.Errorf("debug mode requires building with -tags debugmode")
	}
	*v.target = false
	return nil
}

func (v *disabledDebugValue) String() string {
	return v.underlying.String()
}

func (v *disabledDebugValue) Type() string {
	return "bool"
}

func registerDebugFlag(fs *pflag.FlagSet, cfg *AppConfig) {
	fs.BoolVar(&cfg.Debug, "debug", false, "Debug mode requires building with -tags debugmode")
	flag := fs.Lookup("debug")
	underlying := flag.Value
	flag.Value = &disabledDebugValue{underlying: underlying, target: &cfg.Debug}
	flag.Hidden = true
	flag.NoOptDefVal = "true"
	cfg.Debug = false
}

func applyDebugEnvDefaults(flagSet map[string]struct{}, cfg *AppConfig) error {
	cfg.Debug = false
	if _, ok := flagSet["debug"]; ok {
		return nil
	}
	if v, ok := os.LookupEnv("RECOMMA_DEBUG"); ok && v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return err
		}
		if parsed {
			return fmt.Errorf("RECOMMA_DEBUG=true requires building with -tags debugmode")
		}
	}
	return nil
}
