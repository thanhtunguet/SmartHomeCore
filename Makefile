SHELL   := /bin/bash
BINARY  := homecore
PKG     := github.com/thanhtunguet/homecore
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)
DEV_CONFIG ?= configs/dev.yaml
CROSS_TARGETS := linux/amd64 linux/arm64 linux/arm windows/amd64 darwin/arm64 darwin/amd64 android/arm64

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the homecore binary into build/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o build/$(BINARY) ./cmd/homecore

.PHONY: devtools
devtools: ## Build developer utilities (keygen, signed agent commands)
	go build -o build/devtools ./cmd/devtools

.PHONY: run
run: ## Run the Controller with the dev config
	go run ./cmd/homecore --config $(DEV_CONFIG) --mode controller

.PHONY: run-agent
run-agent: ## Run the Agent (dry-run) with the dev config
	go run ./cmd/homecore --config $(DEV_CONFIG) --mode agent

.PHONY: run-both
run-both: ## Run Controller + Agent in one process with the dev config
	go run ./cmd/homecore --config $(DEV_CONFIG) --mode both

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Test coverage report
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format code
	gofmt -w cmd internal mobile web

.PHONY: lint
lint: ## gofmt check, go vet, golangci-lint (if installed)
	@test -z "$$(gofmt -l cmd internal mobile web)" || (echo "gofmt needed:"; gofmt -l cmd internal mobile web; exit 1)
	go vet ./...
	@if command -v golangci-lint >/dev/null; then golangci-lint run; else echo "golangci-lint not installed, skipped"; fi

.PHONY: generate
generate: ## Regenerate code from api/openapi.yaml
	go generate ./internal/api/gen/...

.PHONY: check-generated
check-generated: generate ## Fail if generated code is out of date
	git diff --exit-code -- internal/api/gen

.PHONY: cross
cross: ## Cross-compile for all supported targets (CGO disabled)
	@for t in $(CROSS_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ "$$os" = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o build/$(BINARY)-$$os-$$arch$$ext ./cmd/homecore || exit 1; \
	done

.PHONY: android-aar
android-aar: ## Build the Android AAR (needs gomobile + Android NDK)
	@command -v gomobile >/dev/null || (echo "install: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init"; exit 1)
	gomobile bind -target=android/arm64,android/arm -androidapi 24 -ldflags "$(LDFLAGS)" -o build/homecore.aar ./mobile

.PHONY: swagger-ui
swagger-ui: ## Re-fetch pinned Swagger UI assets into web/swagger
	./scripts/fetch-swagger-ui.sh

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: clean
clean: ## Remove build outputs
	rm -rf build coverage.out
