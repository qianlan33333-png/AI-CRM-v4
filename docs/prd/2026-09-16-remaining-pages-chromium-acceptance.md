# 剩余页面 Chromium 验收 PRD

日期：2026-09-16
状态：四页移动/存档证据已完成；补充后台既有页面的桌面 Chromium 证据。

## 业务判断与分类

现有 HTTP、PG 与 JSDOM 合同不足以证明最终浏览器页面。范围只补 `/admin/message-archive`、`/admin/message-archive/customers/{id}`、`/r/{public_code}`、`/c/{slug}` 和 `/shared/service-period-member-grid` 的真实 PostgreSQL + Chromium 证据。

OneID：会话存档只读取既有 canonical customer；不 Resolve、Provision、关联或合并。

Persistence：仅隔离 UTF-8 PostgreSQL fixture。Radar 允许其既有同源本地 event receipt；无生产写。

External Effects：不涉及 Provider、支付、队列、发送或跨身份行为。

### 后台补证据分类（2026-09-16）

本轮补 `/admin/coupons`、`couponForm`、`couponData`、`/admin/service-period-products`、其管理员 member-grid、`/admin/external-effects`、`/admin/channels/new`、`/admin/api-docs`、`/admin/config/releases` 与 `/admin/owner-migration` 的 1280／1440 页面证据。

OneID：仅读取既有 canonical customer／产品事实；不 Resolve、Provision、关联、迁移或合并身份。管理员 member-grid 只显示其既有受权的 Product Owner 查询结果。

Persistence：独立 UTF-8 PostgreSQL fixture 内的本地种子数据和只读页面查询；无生产数据库、无写操作或 durable job。本轮没有业务命令、收据、审计或 Outbox 的新语义。

External Effects：Provider 保持 disabled；不会触发支付、发券、发信、渠道保存、外部效果刷新或其他 Provider 调用。

## 复用与范围

GitHub 参考采用 [Playwright screenshot guidance](https://github.com/microsoft/playwright/blob/main/docs/src/screenshots.md) 与 [Chrome DevTools Protocol examples](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/skills/chrome-devtools-cli/SKILL.md)：截图绑定 SHA，并由语义、访问、网络与布局断言补足，而不是单独以像素判定成功。仓内已有的 [`admin_layout_geometry.mjs`](https://github.com/qianlan33333-png/AI-CRM-v3/blob/main/internal/webshell/admin_layout_geometry.mjs) 和 [`TestPostgreSQLAdminShellLayoutChromiumJourney`](https://github.com/qianlan33333-png/AI-CRM-v3/blob/main/cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go) 是本轮唯一的页面装配及 PG/Chromium fixture 参考；不另造平行页面或 browser harness。

管理员页使用既有 `admin_base`，公共页使用各 Owner 的既有 public host；不造新页面或组件。截图路径只经 `internal/platform/config.RemainingPagesChromiumScreenshotDirectory` 取得，test 不直接读取该环境变量。

后台补证据复用 `admin_base`、`admin_layout_geometry.mjs`、Product 的列表/Member Grid Host、Coupon/Channel/Open Platform/Runtime/Owner/External Effects 已挂载 Host。组件索引命中管理端单壳、顶栏动态操作及表格溢出操作；本次不扩展它们。当前 Skills catalog 没有可调用的 `product-design` 路由；本项为既有页面的测试证据，不涉及视觉探索或设计改动。

Radar fixture 单独创建 Media-owned 的 320×180 本地彩色 PNG，并要求 natural size 与 CSS box 可见，避免复用 1×1 目录素材导致空白“通过”。

## 证据与边界

- archive entry/detail 均在 1280、1440；公开三页均在 375、390、430；每张图名含执行 SHA。
- 断言实际 root/行/图片、无横向溢出、archive 未认证 401、coupon/radar missing 失败态、无效 grid token 无数据。
- Radar 要求同源 `/api/public/radar/{code}/events` 网络 200，随后在其 Owner `radar_events` 回读 `image_loaded`。
- coupon fixture 领取期为 `now-1h` 至 `now+24h`，显式验证当前在窗口内和非微信安全禁用态。
- Archive 单标题与 Grid unknown-total 修复由独立运行时 PR 负责；它们未合入前，相关截图仅作诊断，不能作为最终放行。
- 每个后台 route 在 1280 和 1440 分别断言真实挂载、单一页头、主操作或种子数据可见及无横向溢出。加载失败、403、空 DOM 或 API 失败不得作为数据成功；Channel 新建页只读取其现有表单和 picker 入口，绝不提交。
- Coupon 三页验证同一 fixture 券、产品目标和领取记录；service-period 列表重新取证，管理员 member-grid 验证 Product Owner 的既有读回；公共 member-grid 仍由既有 375／390／430 旅程验收，二者不互相替代。
