# Multi-stage, CGO_ENABLED=0, distroless static, amd64 + arm64
# (docs/10-deployment-ci.md §10.4).
#
# CGO_ENABLED=0 is only possible because the SQLite driver is pure Go. That single
# choice keeps arm64 a cross-compile rather than an emulated build.

FROM node:22-alpine AS ui
WORKDIR /ui
COPY ui/package*.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
# outDir points outside the working directory, at the Go embed path.
RUN npm run build

# 1.25, not the 1.23 the specification named: modernc.org/sqlite and goose both
# declare `go 1.25` in their go.mod, so an older toolchain cannot build them.
# Recorded in docs/02-architecture.md §2.6.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The built SPA replaces the committed placeholder before the binary is linked, so
# the image can never ship the placeholder page.
COPY --from=ui /internal/transport/rest/assets/dist ./internal/transport/rest/assets/dist
ARG VERSION=dev
ARG COMMIT=none
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/moneyapp ./cmd/moneyapp
# /config must exist in the image, owned by the runtime user: Docker initialises an
# empty named volume from the image's directory, so without this the volume lands
# root-owned and a non-root process cannot create the database. distroless has no
# shell to chown at runtime, so it is staged here.
RUN mkdir -p /out/config

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/moneyapp /moneyapp
COPY --from=build --chown=nonroot:nonroot /out/config /config
USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/config"]
# The healthcheck invokes the binary itself, so the image needs no curl and stays
# distroless.
HEALTHCHECK --interval=30s --timeout=3s --retries=3 --start-period=10s \
    CMD ["/moneyapp", "healthcheck"]
ENTRYPOINT ["/moneyapp"]
CMD ["serve"]
