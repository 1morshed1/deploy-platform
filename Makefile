# Makefile for deploy-platform.
#
# Run `make` (or `make help`) to see the available targets. Targets are grouped:
# local Go dev, local dependencies, images, and cluster.

# --- configuration --------------------------------------------------------
REGISTRY ?= deploy-platform          # local image name prefix
TAG      ?= dev
CMDS      = api worker operator
# DATABASE_URL used by migrate/seed when run on the host against docker-compose.
export DATABASE_URL ?= postgres://postgres:postgres@localhost:5432/deploy_platform?sslmode=disable

.DEFAULT_GOAL := help

# --- help -----------------------------------------------------------------
.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# --- Go dev ---------------------------------------------------------------
.PHONY: build
build: ## Compile all three binaries into ./bin
	@mkdir -p bin
	go build -o bin/ ./cmd/...

.PHONY: test
test: ## Run all unit tests
	go test ./...

.PHONY: lint
lint: ## Static checks: go vet + gofmt verification
	go vet ./...
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -w .

.PHONY: tidy
tidy: ## Sync go.mod/go.sum
	go mod tidy

.PHONY: run-api run-worker run-operator
run-api: ## Run the API server locally
	go run ./cmd/api
run-worker: ## Run the worker locally (needs a kubeconfig/cluster)
	go run ./cmd/worker
run-operator: ## Run the operator locally (needs a kubeconfig/cluster)
	go run ./cmd/operator

.PHONY: seed-tenant
seed-tenant: ## Create a dev tenant and print an API key
	go run ./cmd/seed

# --- local dependencies ---------------------------------------------------
.PHONY: dev-deps
dev-deps: ## Start Postgres + Redis via docker-compose
	docker compose up -d

.PHONY: dev-deps-down
dev-deps-down: ## Stop Postgres + Redis
	docker compose down

.PHONY: migrate-up
migrate-up: ## Apply database migrations (against docker-compose Postgres)
	docker compose exec -T postgres psql -U postgres -d deploy_platform < migrations/0001_init.up.sql

.PHONY: migrate-down
migrate-down: ## Revert database migrations
	docker compose exec -T postgres psql -U postgres -d deploy_platform < migrations/0001_init.down.sql

# --- images ---------------------------------------------------------------
.PHONY: docker-build
docker-build: ## Build container images for all three binaries
	@for c in $(CMDS); do \
		echo "==> building $(REGISTRY)-$$c:$(TAG)"; \
		docker build -f build/Dockerfile --build-arg CMD=$$c -t $(REGISTRY)-$$c:$(TAG) . ; \
	done

# --- cluster --------------------------------------------------------------
.PHONY: kind-up
kind-up: ## Create a local kind cluster
	kind create cluster --config deploy/kind/kind-config.yaml

.PHONY: kind-down
kind-down: ## Delete the local kind cluster
	kind delete cluster --name deploy-platform

.PHONY: kind-load
kind-load: docker-build ## Load locally-built images into the kind cluster
	@for c in $(CMDS); do kind load docker-image $(REGISTRY)-$$c:$(TAG) --name deploy-platform; done

.PHONY: deploy-crd
deploy-crd: ## Install the TenantApp CRD
	kubectl apply -f deploy/crd/tenantapp-crd.yaml

.PHONY: deploy-platform
deploy-platform: deploy-crd ## Apply platform namespaces, RBAC, and workloads
	kubectl apply -f deploy/k8s/
