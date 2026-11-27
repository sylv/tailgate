# the client and the go build run on the build machine, and go cross compiles,
# so an arm64 image doesn't need emulation
FROM --platform=$BUILDPLATFORM node:24-slim AS client
RUN corepack enable
WORKDIR /build
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY client/package.json client/
RUN pnpm install --frozen-lockfile
COPY client client
RUN pnpm --filter client build

# db-ip publishes each month's country database early on the 1st, so last month's is the
# fallback until then. GEOIP_MONTH changes the layer each month, a rebuild picks up the new one
FROM --platform=$BUILDPLATFORM golang:1-trixie AS geoip
ARG GEOIP_MONTH
RUN month=${GEOIP_MONTH:-$(date -u +%Y-%m)} && \
    { curl -fsSL "https://download.db-ip.com/free/dbip-country-lite-$month.mmdb.gz" || \
      curl -fsSL "https://download.db-ip.com/free/dbip-country-lite-$(date -u -d "$month-01 -1 month" +%Y-%m).mmdb.gz"; } | \
    gunzip > /dbip-country-lite.mmdb

FROM --platform=$BUILDPLATFORM golang:1-trixie AS builder
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=client /build/client/dist client/dist
# the tailscale admin console shows longStamp, which reads 1.106.1-ERR-BuildInfo without git info
RUN TS=$(go list -m -f '{{.Version}}' tailscale.com | sed 's/^v//') && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /tailgate \
    -ldflags="-s -w -X main.version=$VERSION -X tailscale.com/version.longStamp=$TS-tailgate -X tailscale.com/version.shortStamp=$TS" . && \
    mkdir -p /data /run/tailgate

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=builder /tailgate /usr/local/bin/tailgate
COPY --from=geoip /dbip-country-lite.mmdb /usr/share/tailgate/dbip-country-lite.mmdb
# a named volume copies these owners, so the nonroot user can write to them
COPY --from=builder --chown=65532:65532 /data /data
COPY --from=builder --chown=65532:65532 /run/tailgate /run/tailgate
ENV TAILGATE_DATA_DIR=/data
ENTRYPOINT ["/usr/local/bin/tailgate"]
