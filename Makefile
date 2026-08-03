# Every stage's verification uses these targets
# (docs/implementation-plan/00-conventions.md §12).

SHELL := /bin/bash
.DEFAULT_GOAL := help

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILT_AT ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE   ?= ghcr.io/antlko/moneyapp
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.builtAt=$(BUILT_AT)

# Local runs use ./tmp/config so a stray command can never touch /config.
LOCAL_CONFIG_DIR := tmp/config
LOCAL_CONFIG     := $(LOCAL_CONFIG_DIR)/config.yaml

.PHONY: help
help: ## List the available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: ui
ui: ## Build the Vue SPA into the Go embed directory
	cd ui && npm ci --no-audit --no-fund && npm run build

.PHONY: build
build: ui ## Build the UI and the binary
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/moneyapp ./cmd/moneyapp

.PHONY: build-go
build-go: ## Build only the binary, against whatever UI is already embedded
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/moneyapp ./cmd/moneyapp

.PHONY: test
test: ## go test -race plus the frontend unit tests
	go test -race ./...
	cd ui && npm run test

.PHONY: test-go
test-go: ## Go tests only
	go test -race ./...

# The config uses the v2 schema, so an older golangci-lint on PATH cannot read it.
# `make tools` installs a matching one into ./bin, which is gitignored.
GOLANGCI ?= $(shell test -x ./bin/golangci-lint && echo ./bin/golangci-lint || echo golangci-lint)

.PHONY: tools
tools: ## Install golangci-lint v2 into ./bin
	GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

.PHONY: lint
lint: ## golangci-lint plus eslint and prettier
	go vet ./...
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt found unformatted files" && exit 1)
	@if $(GOLANGCI) version >/dev/null 2>&1; then \
		$(GOLANGCI) run ./...; \
	else \
		echo "golangci-lint v2 not available; run 'make tools'. Ran go vet and gofmt only."; \
	fi
	cd ui && npm run lint && npm run format:check

.PHONY: openapi-validate
openapi-validate: ## Check that docs/openapi.yaml still parses and generates
	cd ui && npm run gen:api && git diff --quiet -- src/api/schema.d.ts \
		|| echo "note: generated API types changed; commit src/api/schema.d.ts"

$(LOCAL_CONFIG):
	mkdir -p $(LOCAL_CONFIG_DIR)
	sed -e 's#/config/moneyapp.db#$(LOCAL_CONFIG_DIR)/moneyapp.db#' \
	    -e 's#/config/uploads#$(LOCAL_CONFIG_DIR)/uploads#' \
	    -e 's#/config/backups#$(LOCAL_CONFIG_DIR)/backups#' config.yaml.dist > $(LOCAL_CONFIG)

.PHONY: migrate-up
migrate-up: build-go $(LOCAL_CONFIG) ## Apply migrations to the local database
	./bin/moneyapp migrate up --config $(LOCAL_CONFIG)

.PHONY: migrate-down
migrate-down: build-go $(LOCAL_CONFIG) ## Roll back one migration locally
	./bin/moneyapp migrate down --config $(LOCAL_CONFIG)

.PHONY: migrate-status
migrate-status: build-go $(LOCAL_CONFIG) ## Report migration status locally
	./bin/moneyapp migrate status --config $(LOCAL_CONFIG)

.PHONY: migrate-check
migrate-check: build-go ## up -> down -> up on a scratch database, the CI gate
	@set -euo pipefail; \
	scratch=$$(mktemp -d); \
	printf 'server:\n  addr: ":8080"\n  base_url: "http://localhost:8080"\ndatabase:\n  path: "%s/scratch.db"\n' "$$scratch" > $$scratch/config.yaml; \
	./bin/moneyapp migrate up --config $$scratch/config.yaml; \
	./bin/moneyapp migrate down-all --config $$scratch/config.yaml; \
	./bin/moneyapp migrate up --config $$scratch/config.yaml; \
	rm -rf $$scratch; \
	echo "migrate-check: up -> down -> up clean"

.PHONY: run
run: build-go $(LOCAL_CONFIG) ## Run locally against ./tmp/config
	MONEYAPP_BOOTSTRAP_PASSWORD=$${MONEYAPP_BOOTSTRAP_PASSWORD:-change-me-please} \
		./bin/moneyapp serve --config $(LOCAL_CONFIG)

.PHONY: dev
dev: ## Vite dev server with API proxy (run `make run` alongside)
	cd ui && npm run dev

.PHONY: docker-build
docker-build: ## Build the container image for the host architecture
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t $(IMAGE):dev .

.PHONY: docker-buildx
docker-buildx: ## Build for linux/amd64 and linux/arm64
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t $(IMAGE):dev .

.PHONY: parity
parity: ## The Excel parity gate, runnable on its own
	go test ./internal/store/ -run TestParity -v
	go test ./internal/domain/metrics/ -v

.PHONY: verify
verify: lint test migrate-check parity ## The CI gate
	@echo "verify: green"

.PHONY: clean
clean: ## Remove build output and the local database
	rm -rf bin tmp ui/node_modules/.vite
