ARG GO_IMAGE=golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414
ARG RUNTIME_IMAGE=alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN GOPROXY=${GOPROXY} go mod download && go mod verify
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG VERSION=1.0.0
ARG REVISION=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION} -X main.buildDate=${BUILD_DATE}" -o /out/payment-exporter ./cmd/payment-exporter

FROM ${RUNTIME_IMAGE} AS runtime-base
RUN apk add --no-cache ca-certificates && \
    addgroup -g 65532 exporter && adduser -D -H -u 65532 -G exporter exporter && \
    mkdir -p /etc/payment-exporter && chown exporter:exporter /etc/payment-exporter
COPY --from=build /out/payment-exporter /bin/payment-exporter
ARG VERSION=1.0.0
ARG REVISION=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="Payment Exporter" \
      org.opencontainers.image.description="Configurable HTTP availability exporter with explicit status rules and optional mTLS / MTR" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${BUILD_DATE}"
WORKDIR /etc/payment-exporter
USER 65532:65532
EXPOSE 9106
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD ["/bin/payment-exporter", "--healthcheck"]
ENTRYPOINT ["/bin/payment-exporter"]
CMD ["--config.file=/etc/payment-exporter/config.yml", "--web.listen-address=:9106"]

# Optional image. mtr-packet needs NET_RAW in the bounding set and permission
# to acquire its file capability; do not enable no-new-privileges for this image.
FROM runtime-base AS mtr
USER 0
RUN apk add --no-cache mtr libcap-setcap && \
    chmod u-s "$(command -v mtr-packet)" && \
    setcap cap_net_raw+ep "$(command -v mtr-packet)"
USER 65532:65532

# The final/default image contains no MTR binary and needs no Linux capabilities.
FROM runtime-base AS default
