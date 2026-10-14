# syntax=docker/dockerfile:1
# DBHub 前端生产镜像：多阶段构建（node:24 构建 → nginx:alpine 运行时）
ARG IMAGE_VERSION=node:24.12.0-bullseye
FROM ${IMAGE_VERSION} AS builder
WORKDIR /app
COPY package.json package-lock.json* pnpm-lock.yaml* yarn.lock* ./
RUN npm ci || npm install
COPY . .
# 构建时注入 API 代理（运行时也可通过 env.js 覆盖）
ARG VITE_API_PROXY_TARGET=http://backend:8080
ENV VITE_API_PROXY_TARGET=${VITE_API_PROXY_TARGET}
RUN npm run build

FROM nginx:1.27-alpine AS runtime
COPY --from=builder /app/dist /usr/share/nginx/html
# 单页应用回退 + API 代理示例（按需覆盖）
RUN printf 'server {\\n\
  listen 80;\\n\
  root /usr/share/nginx/html;\\n\
  index index.html;\\n\
  location / { try_files $uri $uri/ /index.html; }\\n\
  location /api/ { proxy_pass http://backend:8080; proxy_set_header Host $host; }\\n\
}\\n' > /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
