# Stage 1: Build Svelte SPA (native — output is platform-independent)
FROM --platform=$BUILDPLATFORM node:22-slim AS frontend-build
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: Build Go binary (native — cross-compile for target arch)
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY --from=frontend-build /app/cmd/server/frontend/dist ./cmd/server/frontend/dist
COPY . .
ARG TARGETARCH
# Cross-compile agent binaries for Linux arm64 + amd64 so they can be embedded.
RUN mkdir -p cmd/server/dist && \
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o cmd/server/dist/agent_linux_arm64 ./cmd/agent && \
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o cmd/server/dist/agent_linux_amd64 ./cmd/agent
# CGO_ENABLED=0 → truly static binary (modernc.org/sqlite is pure Go)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o pulumi-ui ./cmd/server

# Stage 3: Minimal runtime (Pulumi plugins pre-installed)
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates curl && rm -rf /var/lib/apt/lists/*

# Install Pulumi CLI (pinned version for reproducibility)
RUN curl -fsSL https://get.pulumi.com | sh -s -- --version 3.227.0
ENV PATH="/root/.pulumi/bin:$PATH"

# Pre-warm Pulumi resource plugins (avoids runtime downloads).
RUN pulumi plugin install resource oci 4.3.1

# Copy the single Go binary
COPY --from=go-build /app/pulumi-ui /usr/local/bin/pulumi-ui

# Data directory (mount a persistent volume here)
RUN mkdir -p /data/state
VOLUME ["/data"]

ENV PULUMI_UI_DATA_DIR=/data
ENV PULUMI_UI_ADDR=:8080
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/pulumi-ui"]
