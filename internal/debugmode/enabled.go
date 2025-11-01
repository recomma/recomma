//go:build debugmode

package debugmode

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/recomma/recomma/internal/vault"
)

const (
	envDebugThreeCommasAPIKey  = "RECOMMA_DEBUG_THREECOMMAS_API_KEY"
	envDebugThreeCommasSecret  = "RECOMMA_DEBUG_THREECOMMAS_PRIVATE_KEY"
	envDebugHyperliquidWallet  = "RECOMMA_DEBUG_HYPERLIQUID_WALLET"
	envDebugHyperliquidKey     = "RECOMMA_DEBUG_HYPERLIQUID_PRIVATE_KEY"
	envDebugHyperliquidBaseURL = "RECOMMA_DEBUG_HYPERLIQUID_URL"
	debugUserUsername          = "debug"
)

// Available reports whether the binary includes debug mode helpers.
func Available() bool {
	return true
}

// LoadSecretsFromEnv reads the debug secrets from the environment.
func LoadSecretsFromEnv() (*vault.Secrets, error) {
	lookup := func(envKey string) (string, error) {
		raw, ok := os.LookupEnv(envKey)
		if !ok {
			return "", fmt.Errorf("%s not set", envKey)
		}
		value := strings.TrimSpace(raw)
		if value == "" {
			return "", fmt.Errorf("%s is empty", envKey)
		}
		return value, nil
	}

	threeCommasAPIKey, err := lookup(envDebugThreeCommasAPIKey)
	if err != nil {
		return nil, err
	}
	threeCommasPrivateKey, err := lookup(envDebugThreeCommasSecret)
	if err != nil {
		return nil, err
	}
	hyperliquidWallet, err := lookup(envDebugHyperliquidWallet)
	if err != nil {
		return nil, err
	}
	hyperliquidPrivateKey, err := lookup(envDebugHyperliquidKey)
	if err != nil {
		return nil, err
	}
	hyperliquidURL, err := lookup(envDebugHyperliquidBaseURL)
	if err != nil {
		return nil, err
	}

	if _, err := url.ParseRequestURI(hyperliquidURL); err != nil {
		return nil, fmt.Errorf("%s invalid: %w", envDebugHyperliquidBaseURL, err)
	}

	now := time.Now().UTC()

	secrets := &vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     threeCommasAPIKey,
			THREECOMMASPRIVATEKEY: threeCommasPrivateKey,
			HYPERLIQUIDWALLET:     hyperliquidWallet,
			HYPERLIQUIDPRIVATEKEY: hyperliquidPrivateKey,
			HYPERLIQUIDURL:        hyperliquidURL,
		},
		ReceivedAt: now,
	}

	return secrets, nil
}

// DebugUser returns the synthetic debug vault user.
func DebugUser(now time.Time) *vault.User {
	return &vault.User{
		ID:        0,
		Username:  debugUserUsername,
		CreatedAt: now,
	}
}
