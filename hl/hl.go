package hl

import (
	"crypto/ecdsa"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sonirico/go-hyperliquid"
)

// ClientConfig is all the caller needs to supply.
type ClientConfig struct {
	BaseURL string
	Key     string
	Wallet  string
}

func NewExchange(config ClientConfig) (*hyperliquid.Exchange, error) {
	// we want to make sure the config defines main explicitly
	url := hyperliquid.TestnetAPIURL
	if config.BaseURL != "" {
		url = config.BaseURL
	}

	key := strings.TrimSpace(config.Key)
	key = strings.TrimPrefix(key, "0x")
	privateKey, err := crypto.HexToECDSA(key)
	if err != nil {
		return nil, fmt.Errorf("could not load private key: %s", err)
	}

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}
	accountAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(
		privateKey,
		url,
		nil, // Meta will be fetched automatically
		"",
		accountAddr,
		nil, // SpotMeta will be fetched automatically
	)

	return exchange, nil
}
