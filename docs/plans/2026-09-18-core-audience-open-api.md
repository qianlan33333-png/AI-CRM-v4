# CRM 核心人群 API 标准接线

按用户提供分工文档 A1/A2 修复：五个已有业务接口进入最外层路由与既有 Operation Catalog，细分读写能力沿用 OAuth 客户端管理；不自动扩大已有生产客户端权限。

OneID: reads canonical customer via existing Segment and trusted owner ports. Persistence: push record reuses Segment transaction, receipt and audit; reads use standard operation audit. No new identity, queue, OAuth issuer or Provider writes.

GitHub reference: [existing V1 Operation Catalog](https://github.com/qianlan33333-png/AI-CRM-v3/blob/main/internal/openplatform/port/operations.go) and `cmd/aicrm/open_platform_v1.go`; reuse verified catalog/invocation implementation without a second permission system.

新增能力：audience.product.read、audience.member.read、audience.member.operations.read、audience.member.history.read、audience.push.write。REST 与 MCP 使用同一个 executor；写记录稳定 push_id、幂等键和状态版本保留。

数据范围：package_id 先校验；客户级复用可信 owner/customer 校验；成员分页逐项过滤，原游标继续可分页，不返回越权成员数据。产品目录只依包范围过滤，客户范围不能泄漏客户数据。

验收：真实 OAuth token 与最外层 Host 路由、授权目录、未授权/只读/越权拒绝、重复上报仅一条、状态更新、数据库回读。文档同步 OpenAPI 和标准控制台目录。XC R1/R2/R3 的实际地址/凭据/回流证据单独跟踪，不误报 S5。

本地证据：`/tmp/core-api-tests-final.log`、`/tmp/core-api-oauth.log`（真实组合服务＋OAuth＋PostgreSQL）、`/tmp/core-api-domains.log`、`/tmp/core-api-frontend.log`、`/tmp/core-api-openapi.log`。fast/compile 见 `/tmp/core-api-fast-final.log`、`/tmp/core-api-compile-final.log`。这些是专项证据，不替代完整 CI 或生产验收。

回归稳定性修复：CI 的素材刷新旅程复现导航期间 CDP 错误及旧 DOM 按钮误判。提交分组变更后等待新 loader 和文档就绪；只读轮询兼容导航瞬时错误，写动作不重试，原断言全部保留。本地修复前 8 次重复中复现两类错误，修复后完整旅程连续 8 次通过（`/tmp/core-api-media-repro-repeat.log`、`/tmp/core-api-media-fixed.log`）。仅测试同步，不涉及 OneID 或生产持久化。

API 兼容：保留产品列表顶层 `items`（与 `data.items` 相同）。沿用发布文档中可选的 Idempotency-Key：建议显式稳定键，缺省按 push_id/customer_id/package_id/status_version 确定性生成既有 Segment 收据键，短键确定性规范化；不引入随机键或新的防重内核。真实 OAuth 数据库旅程同时验证缺省键及短键重复不增次、同键变载荷冲突、无头状态更新仍为一次推送。

Linux 全量回归进一步发现工具栏异步重绘可发生在按钮检查与点击之间（run 35345088101）。分组按钮改为同一次页面求值中定位并点击，仅未找到按钮时等待；任何可能已点击后的错误直接失败，不重试提交。原读回断言保留。完整素材旅程修复后连续 4 次通过，证据 `/tmp/core-api-toolbar-fixed.log`。

### 慢响应下的页面状态保护

真实 Host 浏览器旅程延迟人群包分组读取，复现初始化时点击禁用按钮和列表刷新误解锁产品绑定字段的问题。旅程等待控件可见且可用后仅点击一次；列表加载仅启用自身面板及弹窗，不触碰产品弹窗。保留已绑定产品的人群包不可修改断言。此修复为前端状态隔离，不涉及新身份、持久化或外部效果。
