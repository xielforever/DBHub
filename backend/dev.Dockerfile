ARG IMAGE_VERSION
FROM ${IMAGE_VERSION}

WORKDIR /app

# Install development tools
RUN apk add --no-cache bash git gcc musl-dev

EXPOSE 8080

# Go 模块代理（直连失败时可按需调整为 https://proxy.golang.org,direct）
ENV GOPROXY=https://goproxy.cn,direct

# For air (hot reload) or just running go
RUN go install github.com/air-verse/air@latest

CMD ["air", "-c", ".air.toml"]
