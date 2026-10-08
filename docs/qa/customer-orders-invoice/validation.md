# 本地验证收据

2026-10-08；独立 worktree `codex/customer-orders-invoice`；国内准确 base `4155b95e8d540d3e5c9f98c51b0a7707292d6c2c`（提交前重新 fetch，国内 main 未变化）。候选准确 head/tree 以 Git 提交和工作台交接消息为准。

环境：macOS、Go 1.26.6、PostgreSQL 16.13；Node 24.21.0 / npm 11.19.0。仓库固定 Node 24.18.0 / npm 11.12.1，本地前端检查是这台机器的结果，不能称固定工具链 CI 或 Linux 安装验收。数据库仅显式本机 `aicrm_test_invoice`，每个 PG Journey 从完整迁移模板克隆隔离库；未读取、写入生产数据库。真实外部 Provider 调用为 0。

## 相关检查

| 命令/范围 | 结果与直接消费者 |
|---|---|
| `go test ./internal/invoice/... -count=1 -v`；新增 OA 签名单独重跑 | Invoice 服务 3 个、官方 SDK Provider 4 个、OA 签名 1 个通过。金额按实付分摊、拒绝猜测税务；CSRF/无会话拒绝；SDK请求、签名解密、回调摘要、文件域名及 SM3；未知提交不能重试；付款读取错误不误称已提交。OA 复用当前支付 Provider 的 stable_token 合同，不强制刷新，票据缓存和 JS 签名已验证。port 无用例不计作测试通过。 |
| `go test ./internal/payment/h5oauth ./internal/payment/http ./internal/order/store ./internal/platform/config -run 'TestOrdersOAuth|TestH5OAuthState|TestSelfOrders|TestInvoiceProvider' -count=1 -v` | 5 个通过：OAuth 状态绑定/过期/重放、精确订单回跳与拒绝身份/跳转参数、授权失败回订单页、限定归属与字面关键词、Provider 默认关闭与配置不完整拒绝。 |
| `go test ./internal/externaleffects -run 'TestClosedDigestOnlyEnvelopeAndStates|TestStaleAttemptCompletionProjectionKinds|TestInvoiceKindsRequireTheirOwnerAndDigestOnlyPayload|TestPaymentV1KindsUsePaymentOwnerWithoutRawPayload' -count=1 -v` | 4 个通过：摘要封装、发票专属 Owner、崩溃未知结果投影及既有 Payment 类型合同。 |
| `DATABASE_URL=<本机隔离库> go test ./cmd/aicrm -run '^TestInvoicePostgreSQL' -count=1 -v`；修复后只重跑 AtomicReplay/RefundRaces | 3 个真实 PostgreSQL Journey 通过：付款/受益归属、本人票据权限、合并身份、真实会话过期、日期/字面关键词/精确单号/游标/历史时间缺失；插入任务及接受效果后失败均完整回滚；8 个并发点击只有一个申请；幂等键不能跨订单；未知/过期 attempted 不重复 Provider 写；重复及变体回调、同一待执行任务唤醒、有效他人会话 IDOR 拒绝；开票前、途中、全额及部分退款、已签名结果与迟到重试乱序、失败退款恢复、查询暂不可用恢复同票号、确认不符停止、开票 4 次重试及冲红上限。 |
| `node web/v3/customerOrders.test.mjs` + 单文件 strict TypeScript | 通过：组合筛选、单选金额、客户端不发送客户身份/金额、字符串安全、首次及提交中会话过期、Provider 关闭。 |
| `node scripts/validate-openapi.mjs` / `node scripts/check-openapi-route-parity.mjs` | 通过：OpenAPI 3.1.0 共 495 paths；54 active operations 和 160 显式 ServeMux registrations。本次新增合同均在检查清单。既有 format 提示不是失败。 |
| `npm run build --silent && node scripts/build-v3-host-adapters.mjs` | 通过：订单 Host/样式纳入现有哈希资源，不注入其他业务页面。主 JS `customerOrdersHost-GYN54TT4.js`，CSS `customerOrdersStyles-U4ROTWD6.css`。 |
| `node scripts/stage-new-shell-ui.mjs web/dist <本机临时副本>` | 通过：42 个私有后台文档、147 个递归运行资产；订单 JS/CSS 均纳入 release_files 字节与哈希闭包。本机文件复制不是共享预发安装。 |
| `go build -o /tmp/aicrm-invoice-check ./cmd/aicrm` | 当前源码编译通过；应用未安装。 |
| Product Design 真实挂载页 | passed：390/320/1024 CSS px，筛选、日期校验、单选、申请读回、状态及空态；截图和限制见根目录 `design-qa.md`。 |
| 改动生产 Go 文件领域导入及环境读取 | 针对 24 个改动生产文件检查通过，只跨领域导入 port/domain；无业务包环境读取。 |
| `git diff --check` | 通过。 |

本次共 20 个 Go 用例通过，另有前端/合同/构建/浏览器检查。相关 EER 新增投影与 Invoice 迟到 completion 的问题均已修复并复验。OpenAPI 只从当前 V4 canonical 文件物化，source-index/source-lock 更新匹配内容 blob/hash/bytes，230 个当前派生视图验证成功；未读取旧仓，也未提交生成的忽略文件。

全仓架构脚本在既有 `cmd/aicrm/automation_fixed_content_chromium_postgres_integration_test.go` 的环境读取处失败。该文件不在本次 diff，未扩大到无关修复；本次生产领域边界已单独检查。此报告是相关范围验证，不称完整全仓回归。

## 安装后短清单

使用最终安装构件和隔离验收会话，按现有 stage-evidence 格式分别记录：

- `customer-orders-authorized-filters`：固定页/资源、无会话拒绝、本人付款及受益归属、组合筛选、已合并身份；实际返回字段与展示一致。
- `invoice-application-readback`：本人既有申请的状态和金额读回、他人 ID 拒绝、未开通清晰提示。开票未开通时不伪造真实出票。
- `invoice-refund-state-readback`：最终包中的申请/退款及冲红状态展示和查询合同。自动动作可靠性以本次 PG Journey 为代码证据，真实 Provider 单列验收。

## 未验证项与授权

尚未安装共享预发/生产；Linux 构件及安装包冒烟由唯一发布工作台执行。migration 0224 已在本机完整迁移模板执行；生产应用前按现有发布规则备份数据库。未申请或读回真实服务商资格、商户税务授权、公众号/JS 域名/卡券绑定、财务配置和凭据；默认关闭发票 Provider。

真实微信 OAuth、JS SDK 官方开放标签和抬头小程序跳转、真实出票、发票 PDF 下载、卡包插入、全额/部分退款后的整票冲红及卡券作废均未验收；合成受理/排队/回执不计为真实成功。

用户授权范围：实现本份已批准 PRD、必要修复、独立候选和国内源码交付、按 AGENTS 路由消息到唯一发布工作台。本开发任务没有用户“部署到生产机”的新授权，不自行安装或启用真实 Provider。
