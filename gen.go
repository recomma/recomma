//go:generate go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi.yaml openapi.yaml
//go:generate go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate
package main
