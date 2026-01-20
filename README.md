# DBHub 数据管理平台

**DBHub** 是一个定位于高端、安全、且极度高效的专业数据管理平台。它采用苹果（Apple）风格的**液态玻璃 (Liquid Glass)** 视觉体系，旨在为数据从业者提供极致的使用体验。

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/Backend-Go-00ADD8.svg)
![Vue](https://img.shields.io/badge/Frontend-Vue.js-4FC08D.svg)

## ✨ 核心特性

- **高端视觉体验**：采用 Fluid Glass Design，支持深色模式与细腻的微交互。
- **多源数据支持**：统一管理 MySQL, PostgreSQL, Redis 等多种数据源。
- **安全优先**：内置 RBAC 权限控制，全链路数据加密，操作审计。
- **智能辅助**：AI 驱动的 SQL 补全与性能优化建议。
- **云原生架构**：基于 Docker 容器化设计，一键部署，开箱即用。

## 🛠 技术栈

| 模块 | 技术选型 | 说明 |
|------|----------|------|
| **前端** | Vue 3, TypeScript, Vite | 核心框架 |
| **UI 库** | Element Plus, TailwindCSS v4 | 液态玻璃风格实现 |
| **可视化** | ECharts | 数据图表渲染 |
| **后端** | Go (Golang) | 高性能 API 服务 |
| **数据库** | PostgreSQL (元数据), Redis (缓存) | 自身存储 |
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

# 2. 启动服务 (前端 + 后端)
docker compose up -d

# 3. 访问应用
# 前端: http://localhost:5173
# 后端健康检查: http://localhost:8080/api/health
```

## 📖 文档指南

详细文档请查阅 [docs/](./docs/) 目录：

- [系统架构总览](./docs/架构设计/系统架构总览.md)
- [API 接口规范](./docs/接口规范/API接口规范.md)
- [数据库模型设计](./docs/数据库设计/数据库模型设计.md)
- [环境搭建指南](./docs/开发指南/环境搭建指南.md)
- [部署运维指南](./docs/部署运维/部署运维指南.md)

## 🤝 贡献

欢迎提交 Pull Request 或 Issue。请确保遵循 [开发规范](./docs/development/coding-standards.md)。

## 📄 许可证

[MIT](./LICENSE)
