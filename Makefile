.DEFAULT_GOAL := test

## fmt: formats Go source code
fmt:
	@gofumpt -l -w .
.PHONY: fmt

## vet: examines Go source code and reports suspicious constructs
vet: fmt
	@go vet ./...
	@staticcheck ./...
	@shadow ./...
.PHONY: vet


## lint: runs linters for Go source code
lint: vet
	@golangci-lint --version
	@golangci-lint run --config .golangci.toml ./...
.PHONY: lint


## test: runs all tests
test: vet
	@go test -v -cover -count=1
.PHONY: test


## bench: runs `go test` with benchmarks
bench: lint
	@go test -bench . -benchmem -run=^$
.PHONY: bench


## escape: runs `go build` with escape analysis
escape: lint
	@go build -gcflags=-m 2>&1
.PHONY: escape


## help: prints this help message
help:
	@echo "Usage:"
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' |  sed -e 's/^/ /'
.PHONY: help