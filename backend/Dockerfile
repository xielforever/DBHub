# syntax=docker/dockerfile:1
# libpg_query uses CGO; vendored headers allow an offline build after pulling images.
ARG IMAGE_VERSION=golang:1.26.9-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c
FROM ${IMAGE_VERSION} AS builder
WORKDIR /src
ENV CGO_ENABLED=1 GOTOOLCHAIN=local GOFLAGS=-mod=vendor
RUN --mount=type=bind,target=/src,readonly --mount=type=tmpfs,target=/tmp --mount=type=tmpfs,target=/root/.cache/go-build \
    go build -p=4 -trimpath -o /out/server ./cmd/server && \
    go build -p=4 -trimpath -o /out/healthcheck ./cmd/healthcheck

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS runtime
WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/server ./server
COPY --from=builder /out/healthcheck ./healthcheck
RUN install -d -m 0700 -o 65532 -g 65532 /var/lib/dbhub/audit
USER 65532:65532
EXPOSE 8080
ENV APP_ENV=production
ENV DBHUB_AUDIT_DIR=/var/lib/dbhub/audit
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 CMD ["./healthcheck"]
CMD ["./server"]
