.PHONY: build run clean test

build:
	go build -o hyperliquid-mock main.go

run:
	go run main.go -addr :8080

clean:
	rm -f hyperliquid-mock

test:
	go test ./...

install:
	go build -o $(GOPATH)/bin/hyperliquid-mock main.go
