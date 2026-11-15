SHELL   := /bin/bash
BINARY  := homecore
PKG     := github.com/thanhtunguet/homecore
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)
DEV_CONFIG ?= configs/dev.yaml
UI_DIR  := frontend
CROSS_TARGETS := linux/amd64 linux/arm64 linux/arm windows/amd64 darwin/arm64 darwin/amd64 android/arm64

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the homecore binary into build/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o build/$(BINARY) ./cmd/homecore

.PHONY: ui-install
ui-install: ## Install frontend dependencies (needs: git submodule update --init)
	@test -f $(UI_DIR)/package.json || (echo "frontend submodule missing: git submodule update --init"; exit 1)
	cd $(UI_DIR) && pnpm install --frozen-lockfile

.PHONY: ui-build
ui-build: ui-install ## Build the frontend into web/static (embedded by `make build`)
	cd $(UI_DIR) && pnpm run build --outDir ../web/static --emptyOutDir
	@touch web/static/.gitkeep

.PHONY: ui-lint
ui-lint: ## Type-check the frontend
	cd $(UI_DIR) && pnpm run lint

.PHONY: ui-test
ui-test: ui-install ## Frontend unit and contract tests (no Controller needed)
	cd $(UI_DIR) && pnpm test

.PHONY: ui-check-api
ui-check-api: ui-install ## Fail if the UI's generated wire types are stale against api/openapi.yaml
	cd $(UI_DIR) && pnpm run check:api

.PHONY: ui-check
ui-check: ui-lint ui-test ui-check-api ## All frontend checks: type-check, tests, generated types

.PHONY: ui-dev
ui-dev: ## Frontend dev server on :3000, proxying /api to the Controller on :8080 (run `make run` too)
	cd $(UI_DIR) && pnpm run dev

.PHONY: build-full
build-full: ui-build build ## Build the UI, then the homecore binary with the UI embedded

.PHONY: package
package: ui-build ## Package Linux release archives (amd64 + arm64) into build/dist, same as CI
	@for a in amd64 arm64; do scripts/package-backend.sh $$a $(VERSION) || exit 1; done

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
