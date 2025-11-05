# Introduction
This file is a helper to prepare your environment for the task at hand. Refer to `AGENTS.md` for more detailed instructions.

# Setup
To be able to run the go tests we need to ensure everything is available.
We require Go version 1.25

## Commands
go mod download

# OpenAPI
This project requires some Go tools for code generation. To pre-warm the generation process you can do the following commands.

## Commands
git submodule update --init --recursive
go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -version

# SQLc
The schema and queries are stored in package storage and require sqlc

## Commands
go tool github.com/sqlc-dev/sqlc/cmd/sqlc version