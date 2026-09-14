# DBHub 数据管理平台

**DBHub** 是一个定位于高端、安全、且极度高效的专业数据管理平台。它采用苹果（Apple）风格的**液态玻璃 (Liquid Glass)** 视觉体系，旨在为数据从业者提供极致的使用体验。

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/Backend-Go%201.25-00ADD8.svg)
![Vue](https://img.shields.io/badge/Frontend-Vue%203-4FC08D.svg)

## ✨ 核心特性

- **高端视觉体验**：采用 Fluid Glass Design，支持深色模式与细腻的微交互。
- **多源数据支持**：统一管理 MySQL, PostgreSQL, Redis 等多种数据源。
- **安全优先**：内置 RBAC 权限控制，PBKDF2 密码哈希、JWT 双令牌认证，全链路操作审计。
- **智能辅助**：AI 驱动的 SQL 补全与性能优化建议（规划中）。
- **云原生架构**：基于 Docker 容器化设计，一键部署，开箱即用。

## 🛠 技术栈

| 模块 | 技术选型 | 说明 |
|------|----------|------|
| **前端** | Vue 3, TypeScript 5.9, Vite 7 | 核心框架 |
| **UI 库** | Element Plus 2.13, TailwindCSS v4 | 液态玻璃风格实现 |
| **前端工程** | Vue Router 4, Pinia 3, Axios | 路由、状态管理、请求封装 |
| **可视化** | ECharts 6 | 数据图表渲染 |
| **动画** | GSAP, CSS Transitions | 微交互 |
| **后端** | Go 1.25（标准库，零第三方运行时依赖） | 高性能 API 服务 |
| **数据库** | PostgreSQL 16 (元数据), Redis 7 (缓存) | 自身存储 |
| **部署** | Docker, Docker Compose | 全生命周期容器化 |

## 🚀 快速开始

### 前置要求
- Docker Engine >= 20.10
- Docker Compose >= 2.0

### 本地开发启动

```bash
# 1. 克隆仓库
git clone https://github.com/your-org/dbhub.git
cd dbhub

# 2. 启动全部服务（前端 + 后端 + PostgreSQL + Redis）
docker compose up -d

# 3. 访问应用
# 前端: http://localhost:5173
# 后端健康检查: http://localhost:8080/api/health
```

开发环境默认登录账号：`admin / admin123`（仅用于本地开发，生产环境通过环境变量注入）。

### 本地裸机开发（可选）

不使用 Docker 时，需自行准备 Go 1.25+、Node 22+、PostgreSQL 与 Redis：

```bash
# 后端（热重载）
cd backend && go run ./cmd/server

# 前端
cd frontend && npm install && npm run dev
```

## 📖 文档指南

详细文档请查阅 [docs/](./docs/) 目录：

- [系统架构总览](./docs/架构设计/系统架构总览.md)
- [前端架构设计](./docs/架构设计/前端架构设计.md)
- [后端架构设计](./docs/架构设计/后端架构设计.md)
- [API 接口规范](./docs/接口规范/API接口规范.md)
- [数据库模型设计](./docs/数据库设计/数据库模型设计.md)
- [低保真 UX 线框图](./docs/界面设计/wireframes/README.md)
- [UI 设计规范](./docs/界面设计/UI设计规范.md)
- [环境搭建指南](./docs/开发指南/环境搭建指南.md)
- [编码规范](./docs/开发指南/编码规范.md)
- [Git 工作流规范](./docs/开发指南/Git工作流规范.md)
- [部署运维指南](./docs/部署运维/部署运维指南.md)

## 📁 项目结构

```
dbhub/
├── backend/                # Go 后端
│   ├── cmd/server/         # 服务入口
│   └── internal/           # config / auth / middleware / health / server / db migrations
├── frontend/               # Vue 3 前端
│   └── src/
│       ├── layouts/        # 主框架布局（导航栏 + 侧边栏 + 顶栏）
│       ├── views/          # 登录 / 仪表盘 / 数据源 / SQL 工作台 / 审计 / 设置
│       ├── stores/         # Pinia 状态仓库
│       ├── router/         # 路由与登录守卫
│       └── utils/          # Axios 封装
├── docs/                   # 设计文档（文档驱动开发）
├── docker-compose.yaml     # frontend / backend / postgres / redis
└── .env                    # 本地开发环境变量样例
```

## 🤝 贡献

欢迎提交 Pull Request 或 Issue。请确保遵循[编码规范](./docs/开发指南/编码规范.md)。

## 📄 许可证

[MIT](./LICENSE)
