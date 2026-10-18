# syntax=docker/dockerfile:1
# DBHub 前端生产镜像：依赖按锁文件安装，构建工具不进入运行镜像。
ARG IMAGE_VERSION=node:24.12.0-bullseye-slim@sha256:553730a6761d1aea8d3dc858cc9c62e51880a96f6c2a9bbce0577f53d87bb923
FROM ${IMAGE_VERSION} AS builder
WORKDIR /app
COPY . .
# Cloud 代理 CA 可选挂载；不将会话证书或 node_modules 写进镜像层。
RUN --mount=type=secret,id=proxy_ca \
    --mount=type=cache,id=dbhub-frontend-npm,target=/root/.npm \
    --mount=type=tmpfs,target=/app/node_modules \
    if [ -f /run/secrets/proxy_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/proxy_ca; fi; \
    npm ci --strict-ssl=true --no-audit --no-fund && npm run build

FROM nginx:1.30.6-alpine@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5 AS runtime
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY security-headers.conf /etc/nginx/dbhub-security-headers.conf
EXPOSE 80
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 CMD wget -Y off -q -O /dev/null http://127.0.0.1/healthz || exit 1
CMD ["nginx", "-g", "daemon off;"]
