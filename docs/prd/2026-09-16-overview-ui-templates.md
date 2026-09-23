# 经营总览：高密度展示、完整趋势金额与本地视觉模板

## 业务判断与边界

管理员在 `/admin` 需要先读到关键经营数字，再按需查看趋势明细；数据来源说明、历史缺口提示和每段读取时间不应占据指标卡、趋势卡和分销卡的主要空间。用户可在同一份经营事实之间切换视觉模板，但模板不能改变统计范围、请求、支付记录抽屉或任何业务状态。

```text
OneID: not involved — 本项不读取、解析、Provision、关联或合并身份；只呈现既有 overview API 的受权只读投影。
Persistence: stateless / browser-local preference — 只把白名单视觉偏好保存在浏览器 localStorage；不新增数据库、汇总、任务或写入。
External Effects: not involved — 不调用 Provider，不发起队列、发送、重试或回调处理。
```

`GET /api/admin/overview`、`GET /api/admin/overview/paid-records`、Payment／Identity／Distribution 的所有统计口径、请求代际、失败重试、鉴权失效后的数据清理和支付记录抽屉分页合同维持不变。本项不把 `data_missing`、失败、无权限或真实零值混为一类：未知值仍为 `—`，失败和权限态仍保留可见文字与重试／登录入口；只有已确认的正常摘要不再显示冗长说明。

## 已核实入口、复用与参考

- 实际链路为 `/admin` → `internal/webshell/handler.go` → `Renderer.RenderOverview` → `admin_base` 单壳 → `#overview-admin-root` → `web/v3/overviewAdmin.ts` / `overview.css`。模板仅扩展 V3 Host 与其 stylesheet，不创建第二壳、第二顶栏或新接口。
- 复用 `web/v3/shared/ui/pageHeaderActions.ts` 的唯一顶栏动作宿主；模板选择器在同一顶栏中装配，范围按钮仍在原 owner 下，且不替换其 DOM 节点。
- 复用现有指标、趋势、明细表、`detailDrawer`、错误态、无权限态与日期草稿合同；不改冻结 donor、manifest 入口、Go 组合或业务 API。
- 视觉只借鉴 [Ant Design Pro](https://github.com/ant-design/ant-design-pro) 的商务 KPI/趋势层级、[Tabler](https://github.com/tabler/tabler) 的暗色 token 编排、[AdminLTE](https://github.com/ColorlibHQ/AdminLTE) 的渐变色彩用法；不复制代码，也不引入这些项目、React、图表库或新的运行时依赖。
- Product Design skill 在当前 catalog/tools 中不可用，未伪造调用；按现有前端一致性索引复用 `admin_base`、`pageHeaderActions`、`detailDrawer` 与页面自身 V3 assets。

## 可观察设计

### 信息密度与称谓

1. 指标卡只保留标题、放大的关键值，以及“查看支付记录”这个真实动作。去除红框内的来源待核实、历史数据说明、最近读取和全局读取时间等普通辅助文案；支付趋势、分销进度和待处理事项保留标题，删除副标题和卡底读取说明。
2. `ready` / `zero` 卡无状态徽标；`data_missing` 使用不占文字行的状态标记及可访问名称，金额／人数仍按现有语义显示确认的子集或 `—`。`failed`、403、401、无数据和重试按钮仍使用文字，避免把未知或失败伪装成零。
3. 本页所有面向管理员的“客户”改为“用户”，包括支付用户、新增用户、付款时用户、原因文案和共享后台壳的“用户管理后台”。字段名、URL、API、数据库、OneID 术语和历史事实不重命名；全站其余可见称谓由独立盘点任务处理。

### 三套视觉模板

| 模板 | 视觉目的 | 实现边界 |
| --- | --- | --- |
| 清爽商务（默认） | 白色面板、蓝色关键金额、紧凑 KPI 卡，适合日常管理 | 继承现有后台 token，只有 CSS variables 覆盖。 |
| 深色数据屏 | 深蓝灰底、对比更强的图表与数字层级，适合集中查看数据 | 纯 CSS variables；保留所有焦点、错误和权限状态的可读对比度。 |
| 渐变科技 | 静态蓝紫渐变、轻量高光和渐变柱形，增强视觉识别 | 静态 CSS gradient，无 canvas、WebGL、动画轮询或远程资源。 |

模板下拉或按钮组只更新 `#overview-admin-root` 的 `data-overview-theme` 与安全 localStorage 键。读写 localStorage 必须 `try/catch`，只接受 `business`、`dark`、`aurora` 三个值；异常、无痕模式或非法值都回退默认清爽商务。切换不会调用 `load` 或 `fetch`、不会改变 `state.query`、`customDraft` 或 `requestID`，也不会关闭／重建已打开的支付记录抽屉。

### 支付趋势

1. 七天及更短区间的每根有值柱上方保留完整金额。柱容器为可测量布局，实际由 `clientWidth` 与标签 `scrollWidth` 判定；标签宽度不足时增加独立的标签行/可横向阅读的图表宽度，禁止 `overflow: hidden`、`text-overflow: ellipsis` 或以 tooltip 替代金额展示。
2. 30 天趋势继续完整保留每日柱，图形区以紧凑列呈现；每日明细默认折叠，展开后显示每日期、完整支付金额与订单数。表格在窄宽度可横向滚动，不裁切金额。超过 31 天的既有稀疏数据策略不变。
3. 金额、日期、币种和多币种不可合并提示继续来自原响应；不得为了图形而补造未知日期或汇总不同币种。

## 验收与验证

- `overviewAdmin.test.mjs` 覆盖三模板白名单、本地存储异常回退、切换模板零 fetch、范围／自定义草稿／请求代际不变、支付记录抽屉不受影响，以及所有本页“用户”称谓。
- 为金额引入 Chromium 布局断言：7 天长金额的每个可见标签 `scrollWidth <= clientWidth`，金额字符串完整出现在图表或明细中，30 天明细默认折叠且展开表不截断；断言不能仅验证 SVG `<title>` 或 tooltip。
- 保留既有 ready、zero、data_missing、失败、401/403、陈旧请求、失败重试和抽屉分页回归；`data_missing` 不展示为确认零，权限变化仍清除缓存数字。
- 构建真实 Host，在后台 1440、1280 和窄宽视口进行三模板截图；覆盖 7 天长金额、30 天展开明细和自定义日期草稿。执行受影响 Node/Go shell/manifest 检查及 `python3 scripts/dev_preflight.py fast`；以最终提交的 HEAD、干净树和证据目录报告结果。

## 本地浏览器证据

2026-09-16 的真实 Host 浏览器旅程截图与命令记录保存在本工作树已忽略的 `outputs/overview-ui-templates/2026-09-16/`。该轮使用 PostgreSQL 16 的随机测试 schema 与 Chrome，覆盖清爽、深色、科技模板，7 天长金额的 1280／1440／390 视口，以及 30 天展开明细；它只证明该次本地提交前工作树的页面行为，最终提交后仍须按同一命令重新生成证据。
