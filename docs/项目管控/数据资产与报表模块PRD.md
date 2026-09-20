# DBHub 数据资产 · 报表中心 · IA 收口 PRD

> 版本：v1.0（2026-09-16，依据《数据资产与报表模块设计方案》与经评审通过的 7 屏高保真图制定）
> 关联文档：
> - 设计方案：`docs/界面设计/数据资产与报表模块设计方案.md`
> - 高保真原型：`docs/界面设计/mockups/`（png 为评审基线，HTML 可交互静态稿）
> - 竞品依据：`docs/项目管控/竞品全模块深度调研报告.md`
> - API 契约：`docs/接口规范/API 接口规范.md`（每个里程碑交付时同步）

## 1. 背景与问题

当前 DBHub 已完成鉴权、数据源管理（共享资源）、SQL 工作台闭环、审计、仪表盘。存在四类缺口：

1. **资产不可见**：元数据接口只做实时透传、不落库；无法浏览全库表清单、无 Owner/业务说明/分级/标签，无查询热度。
2. **查询结果不能沉淀**：工作台图表结果关掉即失；无法保存为报表、组合为仪表盘、对外只读分享。
3. **IA 有红线瑕疵**：顶栏全局搜索输入框无绑定；通知铃铛无功能；系统设置在侧栏与头像下拉重复；未来新增模块缺少导航位置。
4. **表数据修改无受控通道**（预研）：双击改数、批量增删没有事务、预览与风险校验，且须受 RBAC 约束。

## 2. 目标与非目标

### 2.1 目标（本期交付）

| 编号 | 目标 | 里程碑 |
| --- | --- | --- |
| G1 | 信息架构收口：分组导航、真实可用的全局搜索、去重入口、按角色显隐 | M1 |
| G2 | 元数据落库：手动「同步字典」，快照表/视图/列/索引/键/DDL/估算行数 | M1 |
| G3 | 数据资产目录：资产树、筛选、列表（Owner/标签/分级/热度/收藏） | M2 |
| G4 | 资产详情：字段/索引/外键/DDL/数据预览，表级与列级业务标注、敏感分级 | M2 |
| G5 | 工作台结果一键转图表并另存为报表（ChartCard 组件三处复用） | M3 |
| G6 | 报表中心：我的/共享/收藏、报表 CRUD、放入仪表盘 | M4 |
| G7 | 仪表盘：12 栅格布局、KPI/图表组合、只读免登录分享链接 | M4 |
| G8 | 表级血缘：解析平台查询历史 SQL，缓存表级依赖图 + 影响分析 | M5 |
| G9 | 表数据行内编辑（预研落地）：受控 DML、SQL 预览、风险校验、单事务 | M6 |

### 2.2 非目标（明确不做）

- 不做 DataHub/Dataphin 级深度治理（术语库/标准集/质量规则/Profiling/数据合约），见竞品报告定位结论。
- 不做定时报表订阅邮件/IM 推送（P2）。
- 分享链接 v1 不支持外部传参（白名单参数 P2）。
- 血缘 v1 不做字段级、跨源血缘，不扫描数据库审计日志/不接 binlog（P3）。
- 不做 Online DDL、结构变更工单、审批流（后续独立模块；M6 仅数据 DML）。
- 不做报表级行级权限动态脱敏（标注的 sensitivity 字段为该能力预留，P2 实现）。

## 3. 角色与权限

| 能力 | admin | developer | readonly |
| --- | --- | --- | --- |
| 浏览资产目录/详情/血缘/报表/仪表盘 | ✅ | ✅ | ✅ |
| 同步字典 | ✅ | ✅ | ❌ |
| 编辑标注（Owner/说明/标签/分级）/收藏 | ✅ | ✅（本人创建的） | ❌（收藏除外：所有登录用户可收藏） |
| 另存为报表、建仪表盘、分享链接 | ✅ | ✅ | ❌ |
| 表数据编辑（M6） | ✅ | ✅（read_write） | ❌（开关禁用） |
| 用户管理/审计查看 | ✅ | ❌ | ❌ |

权限由现有 `middleware.RequireRoles` 与前端路由 `meta.roles` 双层执行；写操作全部进审计中间件。

## 4. 信息架构（G1）

### 4.1 导航（侧栏）

```
概览
  仪表盘
数据
  数据资产★      /assets
  数据源管理      /connections
  SQL 工作台     /query
  报表中心★      /reports          （M4 上线时出现，无页面不进导航）
安全（仅 admin）
  用户与权限      /users
  操作审计        /audit
```

- 侧栏底部「系统设置」删除；头像下拉保留「系统设置」（仅 admin 可见）与「退出登录」。
- 顶栏铃铛在通知中心上线前移除（不放置无功能控件）。
- 每条导航必须与真实路由一一对应；新增模块页面未就绪时，导航项不得出现。

### 4.2 全局搜索（保真图 01 标注 ①）

- 顶栏输入框真实可用，输入 ≥2 字符防抖 250ms 调 `GET /api/v1/assets/search?q=`。
- 分组展示：表/视图、字段、报表（M4）、查询历史；每组带计数，点击跳转：
  - 表/视图 → 资产详情；字段 → 资产详情并定位列；报表 → 报表编辑/查看；历史 → 工作台回填 SQL。
- Enter 跳转资产目录并带关键字参数。
- 弹层为暗色玻璃（`rgba(16,22,40,.97)` + blur + 1px 边框），与规范一致；键盘 ↑↓/Esc/Enter 可操作。

## 5. 功能需求（FR）与验收标准

> 验收统一要求：后端参数化 SQL；前端 TS 严格无报错；暗色玻璃规范；移动端 375px 可用；文案简体中文；所有写接口记审计；交付前无头验证（后端 `go build ./... && go vet ./...`，前端 `vue-tsc && vite build`）。

### M1 元数据底座与 IA 收口

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| M1-FR-01 | 迁移脚本 `0007`：`sys_meta_snapshots`、`sys_asset_annotations`（DDL 以设计方案 §4.1/4.2 为准） | 全新库启动自动迁移；重复执行幂等；表/索引/约束齐全 |
| M1-FR-02 | `POST /api/v1/assets/sync`（admin/developer）：对一个连接采集全部库→schema→表/视图，快照列/索引/键/DDL/注释/估算行数，upsert 入库 | shop 库 4 表采集成功；再次同步不产生重复行；连接不可达返回中文错误且不产生半成品数据；审计可查 |
| M1-FR-03 | 同步支持 PostgreSQL 与 MySQL；Redis 连接返回「不适用」提示 | PG 全字段；MySQL 经 `SHOW CREATE TABLE` 取 DDL、information_schema 取列/估算行数 |
| M1-FR-04 | IA 按 §4 收口，搜索框/铃铛/设置重复入口全部修复 | 导航与路由一一对应；readonly 不见安全组；铃铛移除；侧栏无设置 |
| M1-FR-05 | 全局搜索接口 `GET /assets/search` 返回分组结果 | 表名/列名/注释/业务说明命中；空关键字返回空；响应 ≤500ms（100 表规模） |
| M1-FR-06 | 数据源连接增加「环境」字段（dev/test/prod），资产树节点带环境徽标 | 迁移补字段、连接表单可选、默认 dev；M2 资产树展示徽标 |

### M2 数据资产前端

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| M2-FR-01 | `GET /assets/overview`：纳管数据源数、表/视图数、Owner 覆盖率、敏感/机密字段数 | 数值与快照一致 |
| M2-FR-02 | `GET /assets/tree`：连接→库→schema→表/视图树；不可达连接标记 | 树可展开收起；当前选中态；环境徽标、不可达样式 |
| M2-FR-03 | `GET /assets/tables`：列表 + 过滤（连接/库/schema/关键字/类型/分级/无Owner/已收藏） | 对照保真图 01 列：Owner、标签、分级、行数、近30天热度、同步时间 |
| M2-FR-04 | 热度由 `sys_query_history` 近 30 天聚合（正则词边界匹配表名），不建热度表 | 热度数值可与历史记录数核对 |
| M2-FR-05 | 资产详情 `/assets/table`：Tab 字段/索引/外键/DDL/数据预览 | 保真图 02 布局；库内注释与业务说明分行；只读预览复用 `/data/preview`（≤200 行） |
| M2-FR-06 | `PUT /assets/annotations`：表级与列级 Owner/业务说明/标签/分级（admin/developer）；收藏 `POST /assets/star`（全员） | 重新同步字典不覆盖人工标注；非授权角色 403；标签可增删；分级三档枚举校验 |
| M2-FR-07 | Owner 为系统用户，下拉来自用户列表（admin 接口授权扩展：资产标注需要用户名簿，提供只读 `GET /api/v1/users/brief`，登录可用） | readonly 也能在详情看到 Owner 实名与头像 |

### M3 ChartCard 与工作台转图表

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| M3-FR-01 | 抽取业务无关组件 `ChartCard.vue`（props：类型/维度/指标/排序/结果集），工作台、报表卡片、仪表盘三处复用 | 三处渲染一致；单元覆盖类型 table/bar/line/pie/metric |
| M3-FR-02 | 工作台结果区「图表」Tab 可视化配置（保真图 04 ⑪） | 切换类型即时渲染；维度/指标下拉来自结果列；柱状/折线/饼/数值可用 |
| M3-FR-03 | 「另存为报表」`POST /reports`（保真图 04 ⑩）：固化 SQL + 图表配置，默认私有 | 保存后在报表中心可见；SQL 执行复用 `/query/execute` 全部只读约束 |

### M4 报表中心 / 仪表盘 / 分享

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| M4-FR-01 | `sys_reports`/`sys_dashboards`/`sys_share_tokens` 迁移（设计方案 §4.3–4.5） | 同 M1-FR-01 要求 |
| M4-FR-02 | 报表 CRUD：列表（我的/共享给我/收藏）、编辑（改 SQL 后重跑）、删除、共享切换 | 对照保真图 05；非 owner 不可改他人私有报表；执行受 1000 行/单语句/只读拦截/审计约束 |
| M4-FR-03 | 仪表盘 CRUD：12 栅格拖拽布局（保真图 06 ⑭，栅格吸附不做自由画布）、卡片增删、刷新 | 布局 JSON 落库；刷新重跑全部报表；窄屏单列堆叠 |
| M4-FR-04 | 分享：`POST /shares` 生成免登录只读链接（保真图 06 ⑮），有效期 1/7/30 天/永久、访问次数、吊销 | token 仅存 SHA-256 哈希；独立中间件仅允许 GET 固化报表/仪表盘；不接受任何外部参数；吊销即时生效；无连接串泄露 |
| M4-FR-05 | 公开页 `/s/:token`：无导航外壳的只读看板 | 过期/吊销返回 404 中文页；页面水印（账号标识）列入 P2，本页不做 |

### M5 表级血缘

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| M5-FR-01 | 血缘解析任务：扫描 `sys_query_history.sql_text`（成功语句），PG 用 pg_query_go、MySQL 用 vitess/sqlparser，提取表级读写依赖，写入 `sys_meta_lineage`（带 SQL 指纹与解析时间） | 含 JOIN/子查询/CTE/INSERT…SELECT；解析失败计数与样例可查，不阻断同步 |
| M5-FR-02 | 血缘合并外键关系（实时快照）与 SQL 依赖，详情页「血缘」Tab 出图（保真图 03） | 三类边（外键实线/SQL 依赖虚线/仪表盘引用点线）；层级 1/2/全部 |
| M5-FR-03 | 影响分析：上游表数、下游对象（表/报表/仪表盘）数与清单，级联风险提示 | orders 演示库可得到 customers/products→orders→order_items→报表→看板链路 |
| M5-FR-04 | 重新解析按钮 + 每日定时（容器内定时任务，v1 可手动） | 幂等覆盖缓存；字段级血缘入口不出现（P3） |

### M6 表数据行内编辑（预研落地）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| M6-FR-01 | 资产详情「数据预览」Tab 增编辑模式开关（保真图 07 ⑯）；read_only 禁用并提示 | RBAC 与后端双重校验，后端拒绝只读角色写请求 |
| M6-FR-02 | 双击单元格编辑、新增行、勾选删除；仅前端标记（琥珀/蓝/红高亮），不触库（⑰） | 原值删除线对照；取消全部还原；预览仍 ≤200 行 |
| M6-FR-03 | 浮动变更条汇总改动（⑱）：预览 SQL、放弃、提交 | 参数化 INSERT/UPDATE/DELETE；UPDATE/DELETE 必须主键定位，无主键表禁止编辑并提示 |
| M6-FR-04 | 提交前安全校验（⑲）：无 WHERE 全表变更阻断；外键级联影响提示；影响行数阈值（默认 10，可配置）需填变更原因；单事务失败整体回滚 | 每条校验有对应测试；全部写操作记审计（含变更原因、影响行数、SQL 模板） |
| M6-FR-05 | 生产环境（connection.environment=prod）默认二次确认弹窗 | dev/test 不弹；配置项可关闭 |

## 6. 数据模型摘要

详见设计方案 §4，里程碑落地顺序：

- M1：`sys_meta_snapshots`、`sys_asset_annotations`；`sys_connections` 增列 `environment VARCHAR(10) NOT NULL DEFAULT 'dev'`。
- M4：`sys_reports`、`sys_dashboards`、`sys_share_tokens`。
- M5：`sys_meta_lineage`（缓存表，建表脚本随 M5 迁移给出：edge_id、上游/下游 (connection,db,schema,table)、edge_kind(fk/sql/report)、sql_fingerprint、updated_at）。
- 热度不建表；报表执行历史复用查询历史。

## 7. API 契约摘要（前缀 `/api/v1`，信封 `{code,message,data}` 不变）

| 方法 | 路径 | 权限 | 里程碑 |
| --- | --- | --- | --- |
| POST | /assets/sync | admin,developer | M1 |
| GET | /assets/search?q= | 登录 | M1 |
| GET | /assets/overview | 登录 | M2 |
| GET | /assets/tree | 登录 | M2 |
| GET | /assets/tables | 登录 | M2 |
| GET | /assets/table?connection_id&database&schema&table | 登录 | M2 |
| PUT | /assets/annotations | admin,developer | M2 |
| POST | /assets/star | 登录 | M2 |
| GET | /users/brief | 登录 | M2 |
| GET/POST/PUT/DELETE | /reports… | 登录（写：admin,developer） | M4 |
| GET/POST/PUT/DELETE | /dashboards… | 同上 | M4 |
| POST/GET/DELETE | /shares… | 同上 | M4 |
| GET | /public/s/{token}（**免登录**，独立中间件，不走 /api 鉴权链） | 匿名 | M4 |
| GET | /assets/lineage?…&depth= | 登录 | M5 |
| POST | /data/changes | admin,developer | M6 |

完整请求/响应体在各里程碑编码时同步写入 `docs/接口规范/API 接口规范.md`（文档驱动检查单要求）。

## 8. 前端结构影响

```
src/
  components/business/chart/ChartCard.vue          # M3，业务无关
  components/layout/GlobalSearch.vue               # M1
  views/assets/AssetsView.vue                      # M2
  views/assets/AssetDetailView.vue                 # M2（M5 加血缘 Tab，M6 加编辑态）
  views/reports/ReportsHomeView.vue                # M4
  views/reports/ReportEditView.vue                 # M4
  views/reports/DashboardView.vue                  # M4
  views/public/SharedView.vue                      # M4（免登录）
  api/assets.ts reports.ts shares.ts               # 随里程碑
```
视觉令牌一律取自 `src/style.css`；弹层遵循第四轮暗色玻璃要求；图表交互遵循第五轮「要动画不要静态图」（ChartCard 带入场/悬浮过渡）。

## 9. 非功能需求

- **安全**：参数化查询；分享 token 仅存哈希；标注/报表/变更全部审计；编辑通道默认事务 + 阈值 + 阻断规则。
- **性能**：资产列表分页（默认 50）；同步接口 20s 超时/连接、表级串行采集；1000 张表目录首屏 ≤1.5s（列表只取快照摘要，详情才取 JSONB）。
- **兼容**：现有 6 连接、PGlite 开发环境可一键重建（scripts/dev-pglite）；MySQL 5.7+/PG 12+。
- **容器化**：开发/生产均走 docker-compose；迁移启动时自动执行。
- **可观测**：同步成功/失败、血缘解析失败计数打 slog；失败不静默。

## 10. 里程碑顺序与交付物

| 里程碑 | 内容 | 交付标志 |
| --- | --- | --- |
| M1 | IA 收口 + 环境字段 + 元数据迁移 + 同步采集 + 全局搜索后端/前端接线 | 可在资产页出现前先通过搜索找到表（或 M1/M2 合并交付） |
| M2 | 资产目录页 + 详情页 + 标注/收藏/热度 | 对照保真图 01/02 无头截图核验 |
| M3 | ChartCard + 工作台图表增强 + 另存为报表 | 保真图 04 核验 |
| M4 | 报表中心 + 仪表盘 + 公开分享 | 保真图 05/06 核验；匿名链接可查 |
| M5 | 血缘解析 + 血缘 Tab + 影响分析 | 保真图 03 核验 |
| M6 | 表数据编辑 | 保真图 07 核验 + 安全用例测试 |

原子提交粒度：迁移、后端、API 文档、前端分别成提交；每个里程碑结束做一次全量无头验证并更新本文档验收勾选。

## 11. 风险与对策

| 风险 | 等级 | 对策 |
| --- | --- | --- |
| 大库同步慢/超时 | 中 | 表级串行+单连接超时；v1 手动触发；P2 后台任务+增量 |
| 热度 ILIKE 误匹配（如表名 user） | 中 | PG 正则词边界 `\m…\M`，并按 connection/database 限定 |
| SQL 解析器 vendoring 体积/兼容 | 中 | M5 才引入；先在离线任务内隔离依赖，解析失败降级为仅外键血缘 |
| 分享链接泄露 | 高 | 仅哈希、有效期、访问次数、一键吊销、水印 P2、v1 不传参 |
| 误改生产数据 | 高 | M6 全套：RBAC、主键定位、阈值、原因、事务回滚、审计；prod 二次确认 |
| IA 变动影响老用户 | 低 | 路由路径保持不变；仅分组与入口收口 |
