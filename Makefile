BINARY   := pulumi-ui
DATA_DIR := ./dev-data
PORT     ?= 9770
ADDR     := :$(PORT)

.PHONY: all build frontend backend backend-static build-agent dev run run-bg stop watch-frontend dev-watch \
        docker docker-push deploy clean clean-all help test test-frontend lint test-all \
        release _release-preflight

# ── Default ───────────────────────────────────────────────────────────────────

all: build

# ── Build ─────────────────────────────────────────────────────────────────────

## build: Build frontend, agent binaries, then Go server binary
build: frontend build-agent backend

## frontend: Build the Svelte SPA into cmd/server/frontend/dist/
frontend:
	cd frontend && npm install --silent && npm run build

## backend: Compile the Go binary (requires frontend/dist to exist)
backend:
	@# Touch the embed package so go build detects changes to embedded YAML files
	@touch blueprints/builtins.go
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/server

## backend-static: Compile a fully static binary (CGO_ENABLED=0, for Linux)
backend-static:
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o $(BINARY) ./cmd/server

## build-agent: Cross-compile agent for Linux arm64 + amd64
## Output goes to cmd/server/dist/ so the server binary can embed them via go:embed.
build-agent:
	@mkdir -p cmd/server/dist
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o cmd/server/dist/agent_linux_arm64 ./cmd/agent
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o cmd/server/dist/agent_linux_amd64 ./cmd/agent

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

## watch-frontend: Start Vite HMR dev server only (proxy → localhost:$(PORT))
watch-frontend:
	cd frontend && PORT=$(PORT) npm run dev

## dev-watch: Build everything, then run Go server + Vite HMR in parallel (Ctrl-C stops both)
# Kills any stale server/vite processes first, then starts fresh.
# kill 0 on exit sends SIGTERM to the entire process group so npm AND its
# vite/node child are both cleaned up — simple PID capture only kills npm.
dev-watch: frontend build-agent backend
	@mkdir -p $(DATA_DIR)/state
	@echo "Killing any stale pulumi-ui / vite processes..."
	@pkill -f './$(BINARY)' 2>/dev/null || true
	@pkill -f 'vite' 2>/dev/null || true
	@sleep 0.3
	@echo "Starting Vite HMR on http://localhost:5173 and Go server on $(ADDR) ..."
	@trap 'kill 0' INT TERM EXIT; \
	 cd frontend && PORT=$(PORT) npm run dev & \
	 PULUMI_UI_DATA_DIR=$(DATA_DIR) \
	 PULUMI_UI_STATE_DIR=$(DATA_DIR)/state \
	 PULUMI_UI_ADDR=$(ADDR) \
	 ./$(BINARY); \
	 wait

# Internal guards
_require-binary:
	@[ -f ./$(BINARY) ] || (echo "Binary not found — run 'make build' first." && exit 1)

# ── Test & Lint ───────────────────────────────────────────────────────────────

## test: Run all Go tests
test:
	go test ./internal/... -count=1

## test-frontend: Run Vitest frontend unit tests
test-frontend:
	cd frontend && npx vitest run

## lint: Run svelte-check on the frontend
lint:
	cd frontend && npx svelte-check --threshold warning

## test-all: Run Go tests + Vitest + svelte-check (local pre-push check)
test-all: test test-frontend lint

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

## run-bg: Build and run the server in the background
run-bg: build
	@mkdir -p $(DATA_DIR)/state
	@PULUMI_UI_DATA_DIR=$(DATA_DIR) \
	 PULUMI_UI_STATE_DIR=$(DATA_DIR)/state \
	 PULUMI_UI_ADDR=$(ADDR) \
	 nohup ./$(BINARY) > pulumi-ui.log 2>&1 & echo "$$!" > .pulumi-ui.pid
	@echo "Server running in background (PID $$(cat .pulumi-ui.pid)) → http://localhost$(ADDR)"
	@echo "Logs: tail -f pulumi-ui.log"

## stop: Stop the background server
stop:
	@if [ -f .pulumi-ui.pid ] && kill -0 $$(cat .pulumi-ui.pid) 2>/dev/null; then \
	  kill $$(cat .pulumi-ui.pid) && rm -f .pulumi-ui.pid && echo "Server stopped."; \
	else \
	  echo "No running server found."; rm -f .pulumi-ui.pid; \
	fi

## clean: Remove built binary, dev-data, frontend dist, and embedded agent binaries
clean:
	rm -f $(BINARY)
	rm -rf $(DATA_DIR)
	rm -rf cmd/server/frontend/dist
	rm -rf cmd/server/dist

## clean-all: clean + remove frontend node_modules
clean-all: clean
	rm -rf frontend/node_modules

# ── Release ────────────────────────────────────────────────────────────────────
#
# Usage:
#   make release VERSION=v0.2.0        Explicit version
#   make release                        Auto patch-bump from latest git tag
#
# What it does:
#   1. Determines version (from VERSION= or auto patch-bump)
#   2. Patches AgentVersion in engine.go, agent_bootstrap.sh, and test
#   3. Runs full test suite (Go + frontend + svelte-check)
#   4. Commits, tags, and pushes to origin (triggers release workflow)

CURRENT_TAG := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")

ifndef VERSION
  _MAJOR := $(shell echo $(CURRENT_TAG) | sed 's/^v//' | cut -d. -f1)
  _MINOR := $(shell echo $(CURRENT_TAG) | sed 's/^v//' | cut -d. -f2)
  _PATCH := $(shell echo $(CURRENT_TAG) | sed 's/^v//' | cut -d. -f3)
  VERSION := v$(_MAJOR).$(_MINOR).$(shell echo $$(( $(_PATCH) + 1 )))
endif

_release-preflight:
	@if [ -n "$$(git status --porcelain)" ]; then \
	  echo "Error: working tree is dirty. Commit or stash changes first."; exit 1; \
	fi
	@echo "Releasing $(VERSION) (current: $(CURRENT_TAG))"

## release: Bump agent version, test, commit, tag, and push
release: _release-preflight
	@# Patch version in source files (perl -i avoids the macOS sed backup-file quirk)
	perl -i -pe 's/AgentVersion:\s*"v[\d.]+"/AgentVersion:       "$(VERSION)"/g' \
	  internal/engine/engine.go
	perl -i -pe 's/assert\.Equal\(t, "v[\d.]+", vars\.AgentVersion\)/assert.Equal(t, "$(VERSION)", vars.AgentVersion)/g' \
	  internal/engine/agent_vars_test.go
	perl -i -pe 's/AGENT_VERSION="v[\d.]+"/AGENT_VERSION="$(VERSION)"/g' \
	  internal/agentinject/agent_bootstrap.sh
	@# Run full test suite
	@echo "Running tests..."
	go test ./internal/... -count=1
	cd frontend && npx vitest run
	cd frontend && npx svelte-check --threshold warning
	@# Commit (skip if version files are already at target version), tag
	git add internal/engine/engine.go internal/engine/agent_vars_test.go internal/agentinject/agent_bootstrap.sh
	git diff --cached --quiet || git commit -m "release: $(VERSION)"
	git tag -f $(VERSION)
	git push origin main
	git push origin $(VERSION)
	@echo ""
	@echo "Released $(VERSION) — watch the workflow at:"
	@echo "  https://github.com/trustos/pulumi-ui/actions"

## help: List all available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
