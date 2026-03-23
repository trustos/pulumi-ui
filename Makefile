BINARY   := pulumi-ui
DATA_DIR := ./dev-data
ADDR     := :8080

.PHONY: all build frontend backend backend-static build-agent dev run watch-frontend dev-watch \
        docker docker-push deploy clean clean-all help test lint

# ── Default ───────────────────────────────────────────────────────────────────

all: build

# ── Build ─────────────────────────────────────────────────────────────────────

## build: Build frontend then Go binary
build: frontend backend

## frontend: Build the Svelte SPA into cmd/server/frontend/dist/
frontend:
	cd frontend && npm install --silent && npm run build

## backend: Compile the Go binary (requires frontend/dist to exist)
backend:
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/server

## backend-static: Compile a fully static binary (CGO_ENABLED=0, for Linux)
backend-static:
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o $(BINARY) ./cmd/server

## build-agent: Cross-compile agent for Linux arm64 + amd64
build-agent:
	@mkdir -p dist
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o dist/agent_linux_arm64 ./cmd/agent
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o dist/agent_linux_amd64 ./cmd/agent

# ── Dev ───────────────────────────────────────────────────────────────────────
#
# The encryption key is generated automatically on first run and saved to
# $(DATA_DIR)/encryption.key — no manual setup needed.
# Set PULUMI_UI_KEY_STORE=consul to use Consul KV instead.

## dev: Build everything then run the server
dev: build run

## run: Run the pre-built binary with local dev settings
run: _require-binary
	@mkdir -p $(DATA_DIR)/state
	@PULUMI_UI_DATA_DIR=$(DATA_DIR) \
	 PULUMI_UI_STATE_DIR=$(DATA_DIR)/state \
	 PULUMI_UI_ADDR=$(ADDR) \
	 ./$(BINARY)

## watch-frontend: Start Vite HMR dev server only (proxy → localhost:8080)
watch-frontend:
	cd frontend && npm run dev

## dev-watch: Build the Go binary, then run Go server + Vite HMR in parallel (Ctrl-C stops both)
dev-watch: backend
	@mkdir -p $(DATA_DIR)/state
	@echo "Starting Go server on $(ADDR) and Vite dev server on http://localhost:5173 ..."
	@trap 'kill 0' INT TERM; \
	 PULUMI_UI_DATA_DIR=$(DATA_DIR) \
	 PULUMI_UI_STATE_DIR=$(DATA_DIR)/state \
	 PULUMI_UI_ADDR=$(ADDR) \
	 ./$(BINARY) & \
	 cd frontend && npm run dev & \
	 wait

# Internal guards
_require-binary:
	@[ -f ./$(BINARY) ] || (echo "Binary not found — run 'make build' first." && exit 1)

# ── Test & Lint ───────────────────────────────────────────────────────────────

## test: Run all Go tests
test:
	go test ./internal/... -count=1

## lint: Run svelte-check on the frontend
lint:
	cd frontend && npx svelte-check --threshold warning

# ── Docker ────────────────────────────────────────────────────────────────────

IMAGE ?= pulumi-ui
TAG   ?= latest

## docker: Build the Docker image
docker:
	docker build -t $(IMAGE):$(TAG) .

## docker-push: Build and push the Docker image
docker-push: docker
	docker push $(IMAGE):$(TAG)

# ── Operations ────────────────────────────────────────────────────────────────

## deploy: Run the Nomad job
deploy:
	nomad job run deploy/nomad/pulumi-ui.nomad.hcl

# ── Maintenance ───────────────────────────────────────────────────────────────

## clean: Remove built binary, dev-data, and frontend dist
clean:
	rm -f $(BINARY)
	rm -rf $(DATA_DIR)
	rm -rf cmd/server/frontend/dist

## clean-all: clean + remove frontend node_modules
clean-all: clean
	rm -rf frontend/node_modules

## help: List all available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
