# syntax=docker/dockerfile:1
# DBHub 后端生产镜像：多阶段构建（golang:1.25 编译 → alpine 运行时）
ARG IMAGE_VERSION=golang:1.25.5-alpine3.23
FROM ${IMAGE_VERSION} AS builder
WORKDIR /src
# 利用层缓存先下载依赖
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 静态编译（含 cgo 禁用，便于 alpine 运行）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/server ./cmd/server

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/server ./server
# 迁移与默认配置随镜像内置
COPY internal/db/migrations ./internal/db/migrations
EXPOSE 8080
ENV APP_ENV=production
CMD ["./server"]
