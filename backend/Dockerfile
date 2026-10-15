# syntax=docker/dockerfile:1
# libpg_query uses CGO; vendored headers allow an offline build after pulling images.
ARG IMAGE_VERSION=golang:1.25.5-bookworm@sha256:d9132cce84391efab786495288756d60e1da215b1f94e87860aeefc3d4c45b6d
FROM ${IMAGE_VERSION} AS builder
WORKDIR /src
COPY . .
ENV CGO_ENABLED=1 GOTOOLCHAIN=local GOFLAGS=-mod=vendor
RUN --mount=type=tmpfs,target=/tmp --mount=type=tmpfs,target=/root/.cache/go-build go build -p=4 -trimpath -o /out/server ./cmd/server

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS runtime
WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/server ./server
USER 65532:65532
EXPOSE 8080
ENV APP_ENV=production
CMD ["./server"]
