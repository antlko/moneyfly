.PHONY: help dev-api dev-ui build test lint icons docker clean

BACKEND := backend
UI      := web-ui
DIST    := $(BACKEND)/internal/web/dist
VERSION ?= dev

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

dev-api: ## Run the Go API on :8080 (serves the placeholder SPA)
	cd $(BACKEND) && go run ./cmd/moneyfly --config-dir ../config

dev-ui: ## Run the Vite dev server (proxies /api to :8080)
	cd $(UI) && npm run dev

build: ## Build the single binary with the SPA embedded
	cd $(UI) && npm run build
	rm -rf $(DIST)
	cp -r $(UI)/dist $(DIST)
	cd $(BACKEND) && CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X moneyfly/internal/api.Version=$(VERSION)" \
		-o ../moneyfly ./cmd/moneyfly
	@echo "built ./moneyfly ($(VERSION))"

test: ## Run backend and frontend tests
	cd $(BACKEND) && go vet ./... && go test ./...
	cd $(UI) && npm run type-check && npm run test

lint: ## Lint both toolchains
	cd $(BACKEND) && gofmt -l . && go vet ./...
	cd $(UI) && npm run lint

icons: ## Rasterise the PWA icons from their SVG sources
	./scripts/gen-icons.sh

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t moneyfly:$(VERSION) .

clean: ## Remove build output (restores the dist placeholder)
	rm -rf $(UI)/dist ./moneyfly
	git checkout -- $(DIST) 2>/dev/null || true
