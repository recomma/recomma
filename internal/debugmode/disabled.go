//go:build !debugmode

package debugmode

import (
	"time"

	"github.com/recomma/recomma/internal/vault"
)

// Available reports whether the binary includes debug mode helpers.
func Available() bool {
	return false
}

// LoadSecretsFromEnv returns ErrUnavailable when debug mode is disabled at build time.
func LoadSecretsFromEnv() (*vault.Secrets, error) {
	return nil, ErrUnavailable
}

// DebugUser panics when invoked in a binary without debug mode support.
func DebugUser(time.Time) *vault.User {
	panic("debug mode was not compiled into this binary")
}
