# syntax=docker/dockerfile:1

# --- Stage 1: frontend build ---
FROM node:22-alpine AS webbuild
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- Stage 2: Go build (CGO-free, modernc sqlite) ---
FROM golang:1.22-alpine AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Ensure the frontend artifact from stage 1 is embedded
COPY --from=webbuild /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gpuhub ./cmd/gpuhub

# --- Stage 3: runtime ---
FROM alpine:3.20
# nodejs is REQUIRED by the Playwright python driver (its bundled driver is JS).
RUN apk add --no-cache python3 py3-pip chromium nodejs npm \
	&& addgroup -S gpuhub && adduser -S gpuhub -G gpuhub \
	&& mkdir -p /data && chown gpuhub:gpuhub /data
# Real provider runtimes: Modal SDK (gRPC) + Playwright for Colab web login.
RUN pip3 install --no-cache-dir --break-system-packages modal playwright \
	&& python3 -m playwright install --with-deps chromium 2>/dev/null || true
USER gpuhub
WORKDIR /app
COPY --from=gobuild /out/gpuhub /app/gpuhub
COPY docs/openapi.yaml /app/docs/openapi.yaml
COPY config.yaml /app/config.yaml
COPY examples/ /app/examples/
ENV GPUHUB_OPENAPI_PATH=/app/docs/openapi.yaml \
    GPUHUB_DATA_DIR=/data \
    GPUHUB_DB_PATH=/data/gpuhub.db \
    PLAYWRIGHT_NODEJS_PATH=/usr/bin/node
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/gpuhub"]
