package main

import (
	"flag"
	"log"

	"github.com/recomma/recomma/hyperliquid-mock/server"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP server address")
	flag.Parse()

	log.Printf("Starting Hyperliquid mock server on %s", *addr)
	if err := server.Run(*addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
