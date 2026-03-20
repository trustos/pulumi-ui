# Stage 1: Build Svelte SPA
FROM node:22-slim AS frontend-build
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: Build Go binary (with embedded frontend)
FROM golang:1.23-bookworm AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY --from=frontend-build /app/cmd/server/frontend/dist ./cmd/server/frontend/dist
COPY . .
# CGO_ENABLED=0 → truly static binary (modernc.org/sqlite is pure Go)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o pulumi-ui ./cmd/server

# Stage 3: Minimal runtime (Pulumi plugins pre-installed)
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates curl && rm -rf /var/lib/apt/lists/*

# Install Pulumi CLI
RUN curl -fsSL https://get.pulumi.com | sh
ENV PATH="/root/.pulumi/bin:$PATH"

# Pre-warm Pulumi resource plugins (avoids runtime downloads)
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
