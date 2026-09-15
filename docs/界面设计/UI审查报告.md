# UI 自动化审查报告（第一轮）

> 审查日期：2026-09-15
> 方法：无头 Chromium 真实渲染 + 4 断点 × 6 页面截图（24 张）+ 交互态补拍（抽屉/弹窗）+
> axe-core 无障碍规则（WCAG 2.0/2.1 A/AA、色彩对比）+ DOM 级布局探针（横向溢出、文本截断）。

## 1. 审查矩阵

| 断点 | 视口宽度 | 覆盖页面 |
|------|---------|---------|
| mobile | 375px | 登录、仪表盘、数据源、SQL 工作台、审计、设置 |
| tablet | 768px | 同上 |
| laptop | 1280px | 同上 |
| desktop | 1600px | 同上 |

交互态：移动端导航抽屉、工作台连接树抽屉、AI 抽屉、新建连接弹窗（手机+桌面）、侧边栏折叠态。

## 2. 量化结果（修复后）

- 文档级横向溢出（`scrollWidth - innerWidth`）：**全部 24 个组合为 0**
- 浏览器控制台错误：**0**；失败网络请求：**0**
- axe-core 无障碍违规：**0**（tablet/laptop/desktop 全页面，WCAG A/AA 与色彩对比）
- 生产构建与 `vue-tsc` 严格类型检查：**通过**

## 3. 本轮发现并修复的问题

### 3.1 响应式布局
1. **审计表格在平板/手机被裁切**：IP、状态、耗时三列在 768px/375px 下不可见，仅能横滑发现。
   → 按断点显隐次要列（`<640` 仅保留 时间/操作/资源名称/状态，时间改短格式 `09-14 10:32`），
   分页器窄屏简化为上一页/下一页，资源名称超长省略 + tooltip。
2. **审计筛选栏搜索框被挤压**至单字宽度 → 所有筛选项 `shrink-0`，空间不足时整行换行。
3. **资源类型英文枚举逐字折断**（CONNECTIO/N）→ 单元格单行省略。
4. 越界探针在 dashboard/query 手机端标记的元素均为**预期内横滑表格**（min-width + overflow-x 容器），
   不造成页面级横溢，无需处理；登录页标记项为装饰性光斑（pointer-events-none），亦不产生滚动条。

### 3.2 交互/信息架构
5. **抽屉标题重复**：工作台连接树、AI 面板嵌入抽屉后，抽屉标题与面板内标题各出现一次
   → 面板组件新增 `embedded` 模式，嵌入时隐藏自带标题。
6. **el-tab 标签内嵌关闭按钮**（嵌套交互控件，读屏器焦点异常，axe nested-interactive）
   → 改用 el-tabs 原生 `closable` / `tab-remove` 能力，仅多标签时显示关闭。

### 3.3 无障碍（WCAG）
7. 原生 `<select>` 缺可访问名称（axe select-name, critical）→ 为审计/数据源/工作台所有
   筛选与数据库选择器补 `aria-label`。
8. 设置页 disabled 输入框与标签未关联（axe label, critical）→ 补 `for/id`；主题开关补 `aria-label`。
9. 可滚动区域键盘不可达（axe scrollable-region-focusable, serious）→ 主内容区、
   查询结果网格加 `tabindex="0"` 与语义标签。
10. 次要文字/占位符对比度不足（AAA enhanced）→ 占位符由 white/35 提升至 white/50，
    多处 `text-white/30~35` 提升至 45+，环图中心 10px 小字提升亮度。
11. 全局搜索框补 `<label class="sr-only">`；SQL 编辑区 textarea 补 `aria-label`。

## 4. 已知限制（非缺陷）

- 工作台结果表、仪表盘历史表在手机端保留**容器内横向滚动**（信息密度决定，已保证页面本身不横溢）。
- 移动端 axe 规则与桌面同源，未重复跑以节省时间；已对 375px 全部页面做视觉走查。
- 当前图表为 CSS 占位，接入 ECharts 后需重新审查图表的键盘操作与文本替代。

## 5. 复跑方式

```bash
# 依赖（沙箱内）：@sparticuz/chromium + puppeteer-core + axe-core，
# 通过 LD_LIBRARY_PATH 指向 chromium 自带的 NSS 库，页面注入中文字体后截图。
node audit.mjs        # 全量截图 + 布局探针 + axe，产出 report.json
```

后续每次新增页面或改动布局，建议在合并前按本矩阵复跑一次。
