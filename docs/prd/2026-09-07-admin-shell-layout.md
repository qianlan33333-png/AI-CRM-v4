# 后台壳层布局回归修复

基线：`5291366b9742030957f48ebf7464a040a3ab46db`。本 PR 只修复管理员壳层和 V3 Host 的展示与路由接线，不改变冻结 donor 文件、业务状态、身份、事务或 Provider 行为。

## 设计判断

```text
OneID: not involved — 本项不读取、解析或写入客户身份。
Persistence: stateless — 只改服务端 HTML 壳层、V3 CSS/Host 适配和浏览器验收；不写数据库、不创建任务、不调用 Provider。
```

## 问题与规则

`web/src/admin/sections/labs.css` 的冻结 `.stage.rich` 在业务 Host 所在主区再次施加 `22px 26px` 内边距。多数冻结页面本身把 52px 白色页内工具栏作为第一行；因此标题栏被缩进为悬浮卡片，不能与左侧导航和右侧顶部齐顶。自动化运营页走 Webshell 原生 `admin-topbar`，所以没有该问题。漏斗页没有原生顶部栏，只有内容内的 crumb/page-head，因而缺少标准顶栏。

布局分类：

| 类别 | 壳层职责 | 路由/别名覆盖 | 预期 |
| --- | --- | --- | --- |
| 原生顶栏 | `admin-topbar` + `admin-page` | `/admin/automation-conversion`、`/admin/automation-conversion/packages/*`、`/admin/customers*`、`/admin/message-archive*` | 左侧品牌与右侧白色标准顶栏同起点；业务内容在顶栏下有统一留白。 |
| 冻结页内顶部栏 | V3 壳只移除外层 stage 留白，保留 donor 首行 header/actions/tabs | 运营闭环及详情、群运营及详情、渠道及编辑、普通/周期商品及编辑/会员表、订单及详情、券及详情、标签、素材三库、内容雷达及详情、自动化 Agent、问卷及详情/运营、AdminOps/文档、AI 助手、负责人迁移、外部效果 | donor 页内顶部栏贴住主区上沿；无第二个 `admin-topbar`；操作和标签页保留。 |
| V3 动态内容页 | Webshell 标准顶栏，内容 Host 在其下布局 | `/admin/hxc-dashboard`；运行时配置、Open Platform 等已有 V3 host 逐页复核 | 顶栏唯一；内容标题/刷新等页内 action 保留，不把数据/权限 Host 替换成占位页。 |
| 新壳与别名 | 由实际 Composition 路由或 `web/dist` Host 提供，不把 alias 落回无业务静态页 | 所有上述 canonical 路由及已公开 `.html` / detail 别名 | 每个入口保留正确 Host、API 权限和 donor 冻结边界。 |

## 后台导航与详情入口矩阵

`internal/webshell/contract.go` 的 `ADMIN_NAV_GROUPS` 有 22 个真实管理员入口。真实 PostgreSQL Composition preflight 登录后逐一请求这些入口；强制 Linux Chromium 在同一最终 staged `web/dist` 中逐页进入并测量壳层。`embedded` 行还要求运行后的 `#stage` 具有可见 workspace 与可见标题，并以实际页内栏的 bounding box 验证其与主区顶/左连续；因此空 Host 或仅返回 HTML 不能通过。

| 导航入口 | 实际 owner / 模板类型 | 布局断言 | 代表详情或兼容入口 |
| --- | --- | --- | --- |
| 自动化运营 `/admin/automation-conversion` | Webshell 原生 audience | 唯一 `admin-topbar` | `/packages/*` 原生详情 |
| 运营闭环 `/admin/operation-cycles` | 冻结周期模板 + V3 Host | embedded 页内栏 | `/admin/operation-cycles/cyclesDetail.html`; `cycles.html` 仍标准 404 |
| 群运营 `/admin/automation-conversion/group-ops/ui` | 冻结 GroupOps + V3 Host | embedded 页内栏 | `/admin/groupops.html`、`groupopsDetail.html` |
| 渠道码 `/admin/channels` | 冻结 Channels + V3 Host | embedded 页内栏 | `/admin/channels/new`、`/admin/channels/{id}/edit`、旧 `.html` |
| AI 助手 `/admin/cloud-orchestrator/plans` | 冻结 AI workspace + V3 Host | embedded 页内栏 | `/plans/{id}`；保留计划操作绑定 |
| 客户 `/admin/customers` | Webshell 客户 Host | 唯一 `admin-topbar` | `/admin/customers/{id}` |
| 漏斗 `/admin/hxc-dashboard` | V3 动态 HXC Host | 唯一 `admin-topbar`，隐藏重复内部标题，保留刷新/滚动 | 无静态 donor 回退 |
| 问卷 `/admin/questionnaires` | 冻结 Survey + V3 Host | embedded 页内栏 | `questionnaireDetail.html`、`questionnaireOps.html` |
| 内容雷达 `/admin/radar-links` | 冻结 Radar + V3 Host | embedded 页内栏 | `radarDetail.html`、`radarForm.html` |
| 企微标签 `/admin/wecom-tags` | 冻结 Tags + V3 Host | embedded 页内栏 | 无 `tags.html` 静态降级 |
| 交易 `/admin/orders` | 冻结 Order + V3 Host | embedded 页内栏 | `orderDetail.html?id=<merchant>`；历史外推回执只读 panel |
| 商品 `/admin/wechat-pay/products` | 冻结 Product + V3 Host | embedded 页内栏 | `products.html`、`productForm.html?id=` |
| 周期商品 `/admin/service-period-products` | 冻结 Product + V3 Host | embedded 页内栏 | `spProducts.html`、`spProductForm.html?id=`、会员表 |
| 优惠券 `/admin/coupons` | 冻结 Coupon + V3 Host | embedded 页内栏 | `couponForm.html`、`couponData.html` |
| 图片素材 `/admin/image-library` | 冻结 Media + V3 Host | embedded 页内栏 | 现有库内详情路由 |
| 小程序素材 `/admin/miniprogram-library` | 冻结 Media + V3 Host | embedded 页内栏 | 现有库内详情路由 |
| 附件素材 `/admin/attachment-library` | 冻结 Media + V3 Host | embedded 页内栏 | 现有库内详情路由 |
| 自动化话术 `/admin/automation-agents` | 冻结 Automation + V3 Host | embedded 页内栏 | `agents.html`、`agentEdit.html` |
| 负责人迁移 `/admin/owner-migration` | V3 Owner Handoff Host | 唯一 `admin-topbar`；状态与负责人选择控件在其下 | `ownerMig.html`; `?contact_history=1` 保持只读历史入口 |
| 配置 `/admin/config` | 冻结 Config + V3 Host | embedded 页内栏 | `config.html`、`configDetail.html`; `/admin/config/releases` 保留 V3 runtime Host，并单独验证实际标题、侧栏连续和无双顶栏 |
| OneID `/admin/oneid` | Webshell 原生 OneID | 唯一 `admin-topbar` | 既有客户/冲突 detail API，不改身份规则 |
| API 文档 `/admin/api-docs` | 新壳 Open Platform Host | 303 到 `/admin/apidocs.html` 后加载 V3 Host | 该 Host 由既有 Open Chromium journey 继续验收 |

外部效果 `/admin/external-effects?view=external-effects` 会 canonicalize 到 `campaigns.html?view=external-effects`。它不是侧栏 22 项之一，但使用唯一 `admin-topbar`；只隐藏重复的本地 crumb/title，保留历史链接和刷新操作，并由 Chromium 代表截图和 Composition preflight 覆盖。

## JSSDK 边界

本轮矩阵中的管理员页面不加载或调用 `jweixin` / `wx.config` / `wx.agentConfig`；仓库检索结果仅在 Sidebar 模板与 `web/v3/sidebar/*` 出现这些调用。运营闭环、群运营、渠道、商品和 HXC 的错位都可由实际后台壳层的 `.stage.rich` 几何复现，因此不以更换企业微信 JSSDK 版本处理后台 CSS。Sidebar 的独立 SDK 修复由其 Owner 负责，且不在本 PR 触碰。

## 验收矩阵

| 证据 | 必须证明 |
| --- | --- |
| 渲染器/路由单测 | 每一类页面输出明确 V3 layout marker；周期、普通、详情 alias 仍选原业务 renderer；`cycles.html` 保持 404 而导航写 canonical。 |
| 真实 Composition + PostgreSQL preflight | 登录后的 outer HTTP 页面引用完整 release artifact、业务 Host、正确路由；不得以单模块 mux 或静态 dist 替代。 |
| Linux Chromium 强制旅程 | 实际 Access 登录，导航、刷新、滚动和关键业务 Host 请求完成；采集 sidebar、main、topbar/页内栏 bounding-box 与 computed style；断言主区首栏 `left == sidebar.right`、`top == 0`（内建顶栏）或紧随唯一全局顶栏、无双 header、无横向溢出。 |
| 截图 | 每个代表类别写入合成夹具截图，供 CI/审核复看，不含真实客户或凭据。 |
| 既有业务回归 | 商品、周期商品、运营闭环、群运营、渠道、漏斗刷新等仍调用实际组合 HTTP；权限和 donor manifest 检查不放宽。 |
| 发布装配 | `npm` build、V3 Host adapter 构建、release 资产闭包与 CI 的强制 Chromium 步骤均覆盖本项。 |
| 去重后等价门禁 | `run-donor-view-consumers.sh check` 仍依次执行 `npm test`、最终 `npm run build`、Host adapter 和三段 release stage；随后同一最终产物运行合并的 Sidebar + Admin Layout 强制 Chromium。这样不恢复已被主线收敛的重复阶段，也不遗漏前端重建后的最终 Host/制品验证。 |

## 非目标

不改业务字段、身份/权限规则、数据库 schema、事务、任务、External Effects 或 Provider 配置；不更改 `web/src` donor 字节、manifest 哈希或将业务 Host 替换为静态新壳。
