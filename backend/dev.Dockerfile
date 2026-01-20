ARG IMAGE_VERSION
FROM ${IMAGE_VERSION}

WORKDIR /app

# Install development tools
RUN apk add --no-cache bash git gcc musl-dev

EXPOSE 8080

# For air (hot reload) or just running go
RUN go install github.com/air-verse/air@latest

CMD ["air", "-c", ".air.toml"]
