# syntax=docker/dockerfile:1

# --- Stage 1: build the Vue single-page app ---
FROM node:24-alpine AS web
WORKDIR /web
COPY web-ui/package.json web-ui/package-lock.json ./
RUN npm ci
COPY web-ui/ ./
RUN npm run build

# --- Stage 2: build the static Go binary (embeds the SPA) ---
# Runs natively on the build platform and cross-compiles to the target arch,
# which is fast and safe because the SQLite driver is pure Go (CGO disabled).
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Embed the freshly built SPA into the binary, replacing the placeholder.
RUN rm -rf ./internal/web/dist
COPY --from=web /web/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X moneyfly/internal/api.Version=${VERSION}" \
    -o /moneyfly ./cmd/moneyfly

# --- Stage 3: minimal runtime image ---
FROM gcr.io/distroless/static:latest
# `image.source` is what links the GHCR package page back to the repository —
# without it the package has no README and no inherited visibility. Set here as
# well as by metadata-action so a local `docker build` produces the same image.
LABEL org.opencontainers.image.title="moneyfly" \
      org.opencontainers.image.description="Self-hosted expense tracking that syncs across your devices" \
      org.opencontainers.image.source="https://github.com/antlko/moneyfly"
COPY --from=build /moneyfly /moneyfly
ENV MONEYFLY_CONFIG_DIR=/config \
    MONEYFLY_ADDR=:8080
EXPOSE 8080
VOLUME ["/config"]
ENTRYPOINT ["/moneyfly"]
