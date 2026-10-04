# 商品前端与公开入口只读审查

审查时间：2026-10-04  
基线：`94d4b845f9d9ddcc30e7e05432032ba1677fac5a`。工作树中同时可见主线未提交修复；涉及其改动时，我会注明是当前工作树状态，不把它算入基线。只写入本报告，没有改产品代码，没有调用生产或支付 Provider。

## 结论

商品图片前端会把“空数组”作为显式清空发送；问题发生在 Product app 对空切片的复制/归一化边界，不是表单吞掉了最后一次删除。主线当前工作树已在服务期 app 改用保留 nil/empty 语义的克隆，根任务另做 PostgreSQL 收据验证。

发现两项应跟进的兼容问题：

1. **P2，需按旧 iOS/WebKit 支持范围决定优先级：**公开商品 checkout、服务期身份状态读取和商品素材元数据读取都无条件构造 `AbortController`。超时只有 abort 信号，没有独立的 Promise deadline；较旧嵌入式 WebKit 无此 API 时在进入 try 之前就失败，且不响应 abort 的 transport 可能令界面永久等待。近期 Survey 已用 guard、独立 deadline、响应体纳入截止时间和页面恢复重查解决同类问题。
2. **P2，HTTP 文档/生成客户端契约不完整：**`/api/v1/wechat-pay/checkouts` 的 OpenAPI schema 写了 `additionalProperties: false`，但遗漏了公开购买页实际发送且 handler 结构体允许的支付方式、渠道、联系方式级别、收件地址和推广上下文字段；OpenAPI 也没有 Alipay checkout 路由。当前手写浏览器请求能被 handler 接收，但合同验证器或生成客户端会认为有效浏览器请求非法/无法表达。

没有发现商品/服务期表单自身复现了 Survey 的“历史恢复输入但没有 input 事件”问题：checkout 点击时从当前 DOM 直接读取手机号和地址；商品编辑保存也直接从 DOM 读取主要文本字段；图片草稿按显式数组保存。

## 页面到 handler/app 的实际链路

| 用户入口 | 实际挂载 | Handler 与应用边界 |
|---|---|---|
| 商品后台：`/admin/wechat-pay/products`、`/admin/wechat-pay/products/new`、编辑别名；周期商品 `/admin/service-period-products`、`/new`、编辑和会员数据页 | `cmd/aicrm/composition.go` 的 Product admin API/UI 挂载到 `productBindings.Products` / Product UI；`internal/product/ui.go:55-178` 映射规范路径、旧别名、query 校验和 SSR 模板 | `internal/product/http/handler.go` 普通商品到 Catalog/Product lifecycle，周期商品到 ServicePeriod app。V3 adapter 包装冻结 donor controller，不接管存储。 |
| 普通商品详情 `/p/{code}`、支付页 `/pay/{code}`、只读公开 API `/api/public/products/{code}` | `mountPublicProduct` -> `producthttp.PublicHandler` | PublicHandler 从 Product catalog 读取已启用商品；支付页再调用 Payment HTTP。公开展示图经 Product/Media 绑定读取，不把任意图片 ID 当公开资源。 |
| 服务期详情 `/s/{code}`、购买 `/s/{code}/pay`、身份/权益读 `/api/h5/service-period-products/{code}` | `mountPublicServicePeriod` -> `ServicePeriodPublicHandler` | 周期商品来自 ServicePeriod app；`SetTrustedPublicState` 注入 Payment session reader 与 Order entitlement reader。页面只读已认证会话权益，不能由浏览器提交 customer ID 或自己认领身份。支付仍交给 Payment handler。 |
| 问卷入口 `/q/{slug}`；`/h5/{auth,all,one,result,done}.html`；`/api/public/questionnaires/{slug}[ /submissions]`、OAuth/session/result API | `mountSurveyUI` 提供 H5 资产；`mountSurveyAPIs` 和主 router 将 `/q/`、survey APIs 交给 Survey | `internal/survey/http/handler.go` -> Survey definition/submission/OAuth app。已解析身份来自 HttpOnly `__Host-aicrm_survey_identity` cookie；提交 DTO 是版本、submission key、答案（及可选来源元数据），不是客户身份声明。 |
| 服务期“立即报名”对应真实购买；分销报名 `/distribution`；裂变活动 `/referral` | Distribution/Referral 是独立公共 shell/API，分别经 `mountDistribution`、`mountSecuredReferral` | Distribution 注册只发送服务器当前协议版本，登录与可信 Payment session 通过 bridge；Referral 免费参加走 participation API，购商品资格先建产品上下文再回到 `/p` 或 `/s`。服务期页面不使用 H5 registry 的 S 系列 mock 页面。 |

`web/src/h5/registry.json` 仍展示 signup/active/expired/pay/qr 示例；`web/src/h5/controller.ts:501-503,547-576` 明确将这些演示动作拦截为“后端能力未就绪”，不发请求。不能把该 S 系列模板当成真实报名、购买或服务期 HTTP 契约。

## `images` 的真实字段契约

- Donor controller `web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/admin/controller.ts:1558-1567` 中，`null` 表示沿用加载值；数组（含 `[]`）表示草稿的明确值。删除最后一张图会把草稿置为 `[]`。
- 保存处 `controller.ts:1793-1798` 把草稿放进 `input.images`。API adapter `web/src/api/admin.ts:1513-1528,1537-1549` 写 `images: input.images || []`；JavaScript 中 `[]` 为真值，`JSON.stringify` 会把它发成 JSON 数组 `"images":[]`，不是 `null` 或省略。周期产品生成请求也携带同一数组。
- Product handler 的普通/服务期 create/update DTO 都是 `Images []string json:"images"`（`internal/product/http/handler.go:1658-1703`）；OpenAPI 商品和周期商品 create/update schema 要求 `images` 数组，允许长度为零（`api/openapi.yaml:6456-6621`）。
- 普通商品更新 app 以 `slices.Clone` 保持 nil 与 empty 的区别，并将 nil（旧 caller 未提供）解释为沿用旧值。服务期基线曾以 `append([]string(nil), command.Images...)` 复制更新数组：输入 `[]` 会被压成 nil，随后 fallback 到旧图片；这是主线已报告的“移除最后一张后旧图仍在”的数据库根因。当前工作树 `service_period.go:873-875` 已改为 `slices.Clone`。同类 copy path 曾以 nil slice 复制无图商品，导致 PostgreSQL 的 `products.images` JSON 形状约束收到 null；当前主线在 copy path 改用克隆以保住 `[]`。
- 仍有一个低风险类型设计隐患：`ProductWriteInput.images?: string[]` 声明为可省略，而 update adapter 会把遗漏字段默认成 `[]`，从而表示清空；当前 donor 保存路径始终传数组，因此我没有证据证明正常 UI 会误清。最小防护是把写入 DTO 的 images 设为必需，或只在 create 默认空数组、update 对 undefined 保持省略。主线修复之后还应有一条 UI 合同测试覆盖“单图 -> 移除最后一图 -> PUT body 是 `[]` -> readback 也是 `[]`”；现有素材排序测试仅覆盖混合删除/保留及上传。

## HTTP 契约检查

### 公开支付

前端 `internal/product/http/public.go:429,433-434` 先 GET `/api/v1/wechat-pay/checkout-session`、GET `/api/v1/wechat-pay/purchase-status?product_type=...&product_id=...`，再按 payment route POST 微信或支付宝 checkout；checkout body 包含 `product_kind`、`beneficiary_selection:'payer_self'`、券、付款绑定、provider/channel、contact_collection_level、手机号、收件地址及可选 promotion_context。

`internal/payment/http/handler.go:180-212,895-936` 的路径 switch 与请求结构体接受这些字段；它从 HttpOnly Payment session cookie 得到付款身份，校验 checkout binding 后传给 Payment app。OpenAPI `api/openapi.yaml:4125-4190` 正确表达 checkout session 和 purchase-status（前端查询参数 `product_type` 与 schema 对齐），但 checkout schema 只列 product_id/product_kind/beneficiary/coupon/binding/mobile 且禁止其他字段，没有 address/contact/provider/channel/promotion_context；`/api/v1/alipay/checkouts` 未声明。故这是文档/SDK 的真实合同不一致，不是当前浏览器代码到 Go handler 的字段名不匹配。最小修复：抽取共用 checkout request schema 补齐 handler 接受的所有字段，并显式声明 Alipay checkout 与各自 path-provider 约束；或删掉未支持字段，但这会改变现行收集地址/渠道/推广行为，不建议。

### Survey 与报名身份

Survey 完成 callback 将解析身份放入 Secure/HttpOnly 会话 cookie；服务端 `requireResolvedSurveySession` 再给问卷 GET 和 POST 提交注入可信 identity。客户端的 `X-Aicrm-Survey-Authorization-Context` 是草稿跨身份续接保护值，不是认证凭据。问卷提交成功保留顶层 `result_token` 兼容旧结果查询消费者，同时 nested receipt 不带 token；这点在 handler 注释和客户端验证中一致。

Distribution browser 注册 DTO 仅含 `agreement_version`，session bridge 负责 trusted Payment session；Referral 的付费活动上下文保持服务端签名/opaque token，活动购买 completion 不靠客户端客户 ID。当前静态检查未见身份字段漂移。

## 近期 Survey 旧 WebKit 根因迁移

我回看了 `9d86ffb`（native/history-restored input 与独立 deadline）和 `973ed2e`（旧 WebKit trusted click）及其回归测试：

- **输入状态：**Safari/WebKit 可恢复 input.value 而不派发 input/change，Controller 的 Map 因而陈旧；如果随后翻页、选项重绘、重新授权恢复或提交，就可能丢失可见答案。Survey 新增在这些 destructive boundary 前读取当前可见 text/mobile input，并持久化草稿。商品 checkout 当前在用户点击时直接从 DOM 组装手机号和地址 payload，所以这条具体状态漂移修复没有在商品入口复现；产品 admin controller 保存时也读取 DOM 值。后续如改成受控表单或异步验证，需增加相同的“无 input event 的 history-restored value”用例。
- **点击事件相位：**旧 WebKit 的可信触摸点击可因祖先 capture listener 先禁用提交按钮而不执行按钮 target listener。Survey Host 已移除 capture-phase 的提前 disable，让业务 owner 自己在收到 click 后同步进入 submitting。商品 V3 的 `rememberActionClicks` capture listener 只记住 source element，不阻断/禁用事件；公开购买按钮在本地 target listener 内同步 disable。因此未见相同事件丢失。V3 file input 去重 capture 用 `preventDefault/stopImmediatePropagation`，它只处理重复文件 change，不是商品保存/购买按钮提交。
- **运输等待：**Survey 用 feature detection、独立 deadline 包裹请求和 response body、BFCache/前台恢复时复核时限，仍保留原幂等 key，避免超时后误重发。公开商品 `requestJSON` 在构造处直接 `new AbortController()`，12 秒只调用 abort；服务期 `service_period_identity.js:56-63` 也在 try 前构造并只发 abort；素材元数据读取 `productAdapter.ts:2323-2345` 同样直接构造。建议先把 Survey 的 transport 语义复用于这三处，不要把支付未知结果自动重试：checkout 要继续用原 checkpoint/idempotency key 来人工安全恢复。

## 已执行检查与既有测试

本次成功运行四个纯 JS/JSDOM 合同测试：

- `node web/v3/productAdapter.sp_material.test.mjs` — PASS
- `node web/v3/productAdapter.material_order.test.mjs` — PASS
- `node web/v3/surveySubmissionResume.test.mjs` — PASS（包含无 AbortController、响应/响应体停滞、前台恢复、history-restored input 值）
- `node web/v3/surveyPublicHost.test.mjs` — PASS（明确断言 Survey Host 在 Owner target listener 前不禁用按钮）

相关已有测试/旅程与运行方法：

- 商品管理：`node web/v3/productAdapter.material_order.test.mjs`、`node web/v3/productAdapter.sp_material.test.mjs`、`node web/v3/productAdapter.save_recovery.test.mjs`、`node web/v3/productAdapter.periodic_contact_collection.test.mjs`、`node web/src/api/admin.test.ts`（需要项目对应 TS runner；根入口另有 `npm test`）。
- 后端契约：`go test ./internal/product/http`、`go test ./internal/product/app`；PostgreSQL 真实变更由主线的 `internal/product/store/image_mutation_postgres_integration_test.go` 与 `cmd/aicrm/product_image_mutation_postgres_integration_test.go` 覆盖，具体运行依赖其预设 DB harness。
- 公开页面旅程文件：`node internal/product/http/public_checkout_collection_matrix.mjs`、`node internal/product/http/public_alipay_journey.mjs`、`node internal/product/http/public_payment_completion_journey.mjs`、`node internal/product/http/service_period_public_journey.mjs`、`node internal/product/http/service_period_identity_journey.mjs`。这些依赖本地运行中的 app，不是本次运行。
- Survey 回归：`node web/v3/surveySubmissionResume.test.mjs`、`node web/v3/surveyPublicHost.test.mjs`；历史 WebKit 真实输入证据在 docs 的 2026-10-03 Survey 收据中，本次没有独立重跑旧版 Safari/WebKit。

本次没有运行 Go/PG 集成，也没有向任何 provider 发请求。JSDOM 通过仅是 DOM 级逻辑证据，不是 Safari/旧 WebKit 验收；主线另跑的 localhost Chromium 旅程不并入本报告的 Product Design 截图审计结果。

## Product Design 审计与未覆盖范围

已按 `product-design:index` 路由到 `product-design:audit`。该审计要求先捕获并检查流内实际截图；本次 CUA 只有 Chrome 浏览器条目但没有可用的目标页，IAB 不可用且浏览器策略读取失败，没有可按技能要求控制并截图的 Browser 会话，也没有可调用的 Browser skill。故我没有捏造截图或 UX 视觉发现；这里是代码/HTTP 契约检查，不是已完成的 Product Design 视觉审计。

本报告已审文件集中于 composition/route、Product admin/public handler、商品与问卷 H5 控制器及适配器、Payment/Survey/Distribution/Referral HTTP handler 和对应 OpenAPI。未完整逐页查看管理后台所有商品 tab/会员 grid、优惠券领取与支付每个状态页面，也没有覆盖订单退款/到账、支付 callback/reconciliation 或所有 external-push/完成动作 UI；未运行真实微信 OAuth、真实支付 provider、iOS 旧 WebKit或完整 `/s/{code}` -> `/s/{code}/pay` 真浏览器链路。报告中的“通过”仅指列出的四个 JSDOM 文件，静态链路判断不代替上述入口的端到端或视觉验收。


## 文件清单与审查深度

以下是本次实际读过或对关键区段逐项检索的源码文件；“定向区段”指按路由、handler、images/AbortController/OAuth 字段搜索并读取对应代码，不代表逐行审完整个文件。

**商品、支付和挂载：**
- `cmd/aicrm/composition.go`（Product/SvcPeriod composition、route registration、H5/public mounts）；
- `cmd/aicrm/distribution_routes.go`、`cmd/aicrm/referral_routes.go`（public/API 前缀和安全 wrapper）；
- `internal/product/ui.go`（后台页面路径映射/query/渲染）；
- `internal/product/http/handler.go`（普通/服务期商品 HTTP 请求 DTO、route switch）；
- `internal/product/http/public.go`（公开详情、支付页、checkout JS、图片与 checkout DTO）；
- `internal/product/http/service_period_public.go`、`internal/product/http/service_period_template.go`、`internal/product/http/service_period_identity.js`（周期详情/支付入口、trusted entitlement 读、浏览器身份状态）；
- `internal/product/app/catalog.go`、`internal/product/app/service_period.go`、`internal/product/app/local_lifecycle.go`（Images nil/empty、更新/复制语义；后两者工作树含主线修复）；
- `internal/payment/http/handler.go`（checkout/session/purchase-status route、请求 DTO、strict JSON decode）；
- `api/openapi.yaml`（product images 与 Payment checkout schemas）。

**商品浏览器侧和问卷/身份侧：**
- `web/src/api/admin.ts`；活动 donor `web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/admin/controller.ts`；
- `web/v3/productAdapter.ts`、`web/v3/actionFeedback.ts`（素材草稿/保存委派、异步 action 按钮/捕获相位）；
- `web/src/h5/controller.ts`、`web/src/h5/registry.json`、`web/v3/surveyPublicApi.ts`（问卷页面能力、placeholder signup 动作、输入恢复与读写 deadline）；
- `internal/survey/http/handler.go`（公开问卷/OAuth/session/submission/result 路由和 cookie 身份绑定）；
- `web/v3/distributionCenter.ts`、`internal/distribution/http/handler.go`、`web/v3/referralCenter.ts`、`internal/referral/http/handler.go`（报名/活动路由、前端字段及可信 session 边界的定向区段）。

**本次执行过的测试文件：**
`web/v3/productAdapter.sp_material.test.mjs`、`web/v3/productAdapter.material_order.test.mjs`、`web/v3/surveySubmissionResume.test.mjs`、`web/v3/surveyPublicHost.test.mjs`，四项均 PASS。

## 具体未覆盖项

- 没有对商品后台普通商品列表、服务期列表、会员数据 grid、购买/完成动作、标签/渠道/外推配置的所有页面 tab 逐屏做截图和键盘/辅助技术审查；只看了商品表单保存素材所在的关键区段和入口映射。
- 没有完整追踪公共优惠券 `/c/{code}` 领券、`/api/h5/coupons/available` claims，以及公开购买页 coupon selection -> checkout 的独立 handler/app/PG 契约；只确认 checkout 前端加载优惠券及 route mounted。
- 没有逐一验证服务期会员 grid 分享/访问所有公开和管理员子路由；也未完成服务期详情 -> checkout -> 付款完成 -> 权益回读的真实浏览器端到端链。
- 没有深入验证支付 OAuth start/callback、微信/支付宝 provider response/callback、订单恢复与完成动作的每一种 provider/状态组合；支付处理器/API 部分只针对前端使用的 session/status/checkout 字段核对。
- Survey 检查聚焦公开入口、OAuth session、答题/提交/结果 HTTP DTO 和旧 WebKit 修复；没有审问卷后台每个操作、完成目标配置与 QR/外推结果页的全链路。
- Referral/Distribution 仅看了公开 mount、关键浏览器调用、注册/参与 DTO 和身份边界；未完整读全部业务 app/store，亦未重跑实际 Chromium。主线报告的六条 PostgreSQL+Chromium 组合旅程另属主线验证，不由本报告认领。
- 无旧 WebKit/Safari 真实浏览器或真手机复验；无 Product Design 截图证据。没有修改代码，测试也没有提供 last-image UI 清空的专门断言。

## 主线集成追加（上述为最初只读审查阶段）

随后授权该审查agent仅修改 public requestJSON、service-period身份读取和初始素材元数据读取，并新增实际源码transport回归。集成候选为 `cf259f0`：缺AbortController时正常响应仍成功；fetch/JSON body停滞触发独立deadline；支付创建只发送一次且保留传入原幂等key，409已有购买语义保留。测试对实际TS函数用已有esbuild转译，未增加新依赖。三个生产文件之外只新增回归与既有消费者gate注册，未做视觉重构。

主线从 `git show 94d4b845` 提取三个原始文件到独立临时fixture执行同一新测试，得到exit1；当前候选执行exit0。真实日志在工作区外 `commerce-evidence/transport-before.txt` 与 `transport-after.txt`。Node24.18下v3 TypeScript和素材/保存恢复回归已通过，最终PG与Chromium的准确commit及结果以 final-receipt.json 为准。旧WebKit/真手机依然未执行，初始报告的截图审计限制没有被这些代码测试消除。
