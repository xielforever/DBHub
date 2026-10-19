# DBHub 数据管理平台

**DBHub** 是一个定位于高端、安全、且极度高效的专业数据管理平台。它采用苹果（Apple）风格的**液态玻璃 (Liquid Glass)** 视觉体系，旨在为数据从业者提供极致的使用体验。

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/Backend-Go%201.26.9-00ADD8.svg)
![Vue](https://img.shields.io/badge/Frontend-Vue%203-4FC08D.svg)

## ✨ 核心特性

- **高端视觉体验**：采用 Fluid Glass Design，支持深色模式与细腻的微交互。
- **多源数据支持**：原生接入 MySQL、PostgreSQL、Redis；通过驱动登记、模板发布与固定修订实例接入自定义 JDBC 数据源。
- **数据工作台**：按能力显示 SQL、Redis 键值或结构浏览；SQL 工具栏保留运行、格式化。
- **报表**：个人报表使用各自账号；正式报表支持冻结版本、单级审批、受管只读执行账号、用户/分组授权、发布、下线和回滚。
- **仪表盘**：组合已审批报表，支持工作空间隔离、编排、独立审阅、版本发布/回滚、权限申请与数据时效提示。
- **通知与待办**：审批、授权及发布变化随事务通知，按工作空间展示未读消息与实时待办。
- **数据源任务与运维**：个人身份后台检查/结构采集、定时计划与检查历史；自动元数据备份、故障恢复告警及可配置留存。
- **版本兼容提示**：验证 MySQL 5.7/8.0/8.4、PostgreSQL 14–18、Redis 6.2/7.2/7.4/8.0；具体补丁版本和功能边界见[兼容说明](docs/开发指南/数据源版本兼容.md)。
- **安全优先**：内置 RBAC 权限控制，PBKDF2 密码哈希、JWT 双令牌认证，关键操作审计与持久化补写。
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
| **后端** | Go 1.26.9（pgx / MySQL / Redis 驱动，libpg_query AST） | 高性能 API 服务 |
| **数据库** | PostgreSQL 16 (元数据), Redis 7 (缓存) | 自身存储 |
| **部署** | Docker Compose、Docker CLI、Helm/Kubernetes、源码/systemd | 四种部署入口及统一生产配置 |

## 🚀 快速开始

### 前置要求
- Docker Engine >= 20.10
- Docker Compose >= 2.0

### 本地开发启动

```bash
# 1. 克隆仓库
git clone https://github.com/xielforever/DBHub.git
cd DBHub

# 2. 启动全部服务（前端 + 后端 + PostgreSQL + Redis）
docker compose up -d

# 3. 访问应用
# 前端: http://localhost:5173
# 后端健康检查: http://localhost:8080/api/health
```

开发环境默认登录账号：`admin / admin123`（仅用于本地开发，生产环境通过环境变量注入）。

### 本地裸机开发（可选）

不使用 Docker 时，需自行准备 Go 1.26.9、C 编译工具、Node 24.12.0、PostgreSQL 与 Redis：

```bash
# 后端（热重载）
cd backend && go run ./cmd/server

# 前端
cd frontend && npm install && npm run dev
```

## 📖 文档指南

生产部署支持 **Docker Compose、独立 Docker、Helm/Kubernetes、源码/systemd**，配置、命令及支持边界见[四种部署方式](deploy/README.md)。

详细文档请查阅 [docs/](./docs/) 目录：

- [系统架构总览](./docs/架构设计/系统架构总览.md)
- [前端架构设计](./docs/架构设计/前端架构设计.md)
- [后端架构设计](./docs/架构设计/后端架构设计.md)
- [数据源连接器抽象设计](./docs/架构设计/数据源连接器抽象设计.md)
- [功能命名与自定义数据源](./docs/项目管控/功能命名与自定义数据源规划.md)
- [API 接口规范](./docs/接口规范/API接口规范.md)
- [数据源授权、目录与 openGauss 验收](./docs/开发指南/数据源授权目录与openGauss验收.md)
- [报表第一阶段验收](./docs/开发指南/报表第一阶段验收.md)
- [正式报表发布与执行账号验收](./docs/开发指南/正式报表发布与执行账号验收.md)
- [仪表盘第一阶段实施与验收](./docs/开发指南/仪表盘第一阶段实施与验收.md)
- [仪表盘易用性改进与验收](./docs/开发指南/仪表盘易用性改进与验收.md)
- [生产可靠性修复与验收](./docs/开发指南/生产可靠性修复与验收.md)
- [运维、通知与数据源任务验收](./docs/开发指南/运维通知与数据源任务验收.md)
- [仪表盘第一阶段设计](./docs/项目管控/仪表盘第一阶段设计.md)
- [仪表盘设计评审问题闭环](./docs/项目管控/仪表盘设计评审问题闭环.md)
- [报表口径与权限变更契约](./docs/架构设计/报表口径与权限变更契约.md)
- [报表与仪表盘工作空间隔离](./docs/架构设计/报表与仪表盘工作空间隔离.md)
- [审计覆盖与可靠写入](./docs/开发指南/审计覆盖与可靠写入.md)
- [连接器与 JDBC 自定义数据源验收](./docs/开发指南/连接器与JDBC自定义数据源验收.md)
- [数据源版本兼容](./docs/开发指南/数据源版本兼容.md)
- [数据源检查与采集可靠性](./docs/开发指南/数据源检查与采集可靠性.md)
- [数据库模型设计](./docs/数据库设计/数据库模型设计.md)
- [UI 设计规范](./docs/界面设计/UI设计规范.md)
- [环境搭建指南](./docs/开发指南/环境搭建指南.md)
- [WSL 与 Tailscale 演示数据库部署](./docs/开发指南/WSL与Tailscale演示数据库部署.md)
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
│       ├── views/          # 登录 / 仪表盘 / 数据源 / 数据工作台 / 审计 / 设置
│       ├── stores/         # Pinia 状态仓库
│       ├── router/         # 路由与登录守卫
│       └── utils/          # Axios 封装
├── services/jdbc-runner/    # 可选 JDK 17 JDBC 服务与固定制品清单
├── docs/                   # 设计文档（文档驱动开发）
├── docker-compose.yaml     # frontend / backend / postgres / redis
└── .env                    # 本地开发环境变量样例
```

## 🤝 贡献

欢迎提交 Pull Request 或 Issue。请确保遵循[编码规范](./docs/开发指南/编码规范.md)。

## 📄 许可证

[MIT](./LICENSE)

## Cloud 完整后端

使用 `./scripts/cloud/setup.sh` 安装、编译并执行真实 PostgreSQL/MySQL/Redis/JDBC 集成测试，再用 `./scripts/cloud/start.sh` 启动。配置和服务命令见 [Cloud 开发环境](scripts/cloud/README.md)，实现与边界见 [补齐验收契约](docs/开发指南/云端后端补齐验收.md)。AI 服务可稍后配置，未配置时界面明确提示不可用。

根 `docker-compose.yaml` 用于开发；生产使用独立的 `docker-compose.prod.yaml`，构建、隔离验收、备份与回滚步骤见 [部署运维指南](docs/部署运维/部署运维指南.md)。
