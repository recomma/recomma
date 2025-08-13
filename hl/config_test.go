package hl

import "github.com/sonirico/go-hyperliquid"

var wallet = "0x0000000000000000000000000000000000000000"
var raw = "0x38d55ff1195c57b9dbc8a72c93119500f1fcd47a33f98149faa18d2fc37932fa"

var config = ClientConfig{
	Key:     raw,
	BaseURL: hyperliquid.TestnetAPIURL,
}
