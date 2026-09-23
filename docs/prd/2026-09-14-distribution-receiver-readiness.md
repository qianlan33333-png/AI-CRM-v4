# 分销收款准备、资格说明与付款证据恢复

日期：2026-09-14
范围：修复已注册分销员在商户分佣能力关闭、收款接收方未就绪及资格证据缺失时的真实状态与下一步。补齐既有微信支付查单对“已支付但缺付款确认时间”记录的受控恢复；为已证明全历史未外调的单一 receiver 提供管理员受控恢复入口。
不新增分账策略、不修改默认关闭状态、不自动扫描或批量重试 receiver、不执行资金操作。

## 业务判断

注册、接收方收款准备、商品购买资格及商户分佣结算能力是四个独立事实。

- `AICRM_WECHAT_PAY_PROFIT_SHARING_ENABLED=false` 只表示商户尚未启用分佣结算；它不代表用户微信账户、身份或权限异常。此时必须禁止受理 receiver-add 外部效果、生成推广凭证、归因和资金冻结，但保留注册和只读资料。
- 当前 receiver 已终态失败不能由页面隐藏或泛化错误掩盖。仅超级管理员的受控恢复会检查整个 effect 历史均未外调、receiver 当前可信 identity/app/account digest 及该 channel 当前配置 AppID 一致、当前 SDK 已真实就绪、锁/CAS、审计和稳定幂等 key；任一外调/未知结果拒绝。它保留旧 effect，并以新 intent 关联恢复事实，绝不伪造历史 failure code 或微信拒绝原因。
- 注册后的推广清单只展示策略启用、当前可售且本人有效购买资格已通过的商品。未购、退款待核验、付款确认时间缺失和资格读取不可用都不返回商品卡；只返回受控空态。策略关闭、下架或不可售商品绝不暴露。已检测到本人购买但 Payment 缺少确认时间时，不能建议重复购买。
- `payments.paid_confirmed_at` 是资格和渠道期限的原始支付确认事实，不能使用 `updated_at` 或任意后台时间补写。仅可使用签名验证的 Payment Provider 查单 `SUCCESS.success_time`，并同时校验商户单号、金额、币种和交易号。

## 已核实问题与生产证据

1. Payment 的 `PrepareProfitSharingReceiverWithin` 原先不读取分佣开关，因此开关关闭仍会受理 receiver-add effect；Provider 未装配时会形成无用的 `final_failed`。
2. Distribution 将这类底层失败聚合为 `503 unavailable`，H5 显示“请稍后重试”；商品清单直接过滤所有未通过资格的候选，空态没有原因。
3. 生产只读结果表明本次 actor 已由可信 H5/精确 scope identity 验证；唯一 receiver 为 `final_failed`，全历史 attempt 无外调标记，运行时分佣开关未配置且 SDK public key ID 缺失，无微信返回码。该事实说明当前能力不可用，但旧 effect 无持久化 failure code，不能据此自动重试。
4. 同一商品存在本人已支付、未退款订单，但 Payment `paid_confirmed_at` 缺失；当前资格正确 fail closed。既有签名 Provider 查单已返回 `SUCCESS.success_time`，但 `ReconcileWeChatPayPayment` 在 payment 已为 `paid` 时提前返回，未补齐 Payment 与 Order 的确认事实。
5. `a328` 上一次受控 receiver 恢复已正确接受新 effect、持久化 intent 与审计，却在 worker 的 `wechatpay.profit-sharing.material` 本地阶段终态失败，且两项外调标记均为 false。生产只读已确认开关、认证材料模式、intent、可信 scoped identity、account digest、canonical lineage 和 API/worker runtime snapshot 一致；因此这不是微信拒绝，也没有可安全重试的外调。根因是 Payment 读取 polymorphic provider intent 时错误用三对布尔相等判断 owner：合法的 receiver-only row 会因另两列均为空被判冲突。修复改为“恰好一个 receiver/instruction/unfreeze owner”，不改变 effect、身份或资金策略。

## 交付

### 后端稳定合同

`GET /api/v1/distribution/me` 新增：

```json
{"settlement":{"enabled":false,"reason":"merchant_settlement_disabled"}}
```

这是受限的商户能力投影，不包含配置、密钥、账户或 Provider 响应。

`POST /api/v1/distribution/receiver-preparation` 在分佣结算关闭时不写 receiver、不受理 effect，返回已有 receiver 及 `setup.state=merchant_settlement_disabled`。其他既有状态保持兼容。

`GET /api/v1/distribution/products` 新增 `empty_reason`。`items` 严格只包含当前可售、策略启用且有效购买资格已通过的商品；不合资格事实不会作为候选卡返回。合格卡的主操作是获取分销链接/二维码。Payment 结算能力或 receiver 未就绪时，合格项可有既有受控 `promotion_block_reason`，但不会给购买入口。

| 原因 | 页面下一步 |
| --- | --- |
| `qualification_purchase_required` | 提示需有有效购买资格；不注入商品购买卡或购买按钮 |
| `qualification_refund_pending` | 等待退款核验后刷新 |
| `qualification_payment_confirmation_missing` | 联系商家核验付款确认；不建议重复购买 |
| `qualification_check_unavailable` | 联系商家核验；不建议购买或收款准备 |
| `no_saleable_policy_products` | 当前没有可推广商品 |

推广凭证、归因和资金 gate 不变，仍在服务端逐点重新校验。

### Payment 窄恢复

对非历史微信支付记录，管理员已有的 reconciliation 路径仅在 Provider 已签名验证的查询为 `SUCCESS` 且订单号、金额、币种、交易号和 Order 既有 paid event 时间完全匹配时，允许 `status=paid && paid_confirmed_at IS NULL` 用返回的 `success_time` 补齐事实。查询的 transaction reference 与 digest 仅在当前字段为空时写入；既有非空值必须完全一致。Payment 更新、Order 确认事实验证、reconciliation receipt 和审计事实在一个 PostgreSQL UoW 提交。该恢复绝不改变 `profit_sharing_marked`、支付状态或金额，也不补开历史分账；待支付/未知、不匹配、查单错误或缺 success time 都不写入。

管理员沿用受鉴权、CSRF 与 `Idempotency-Key` 保护的 `POST /api/admin/wechat-pay/payments/{paymentID}/reconcile`。请求体 `{"dry_run":true}` 只执行签名 Provider 查单和上述匹配判定，不写入；省略或为 `false` 时才提交同一受控恢复。该接口不接受浏览器提供的时间、金额、交易号或 SQL 回填值。

管理员受控 receiver 恢复使用独立 Payment 路径 `POST /api/admin/wechat-pay/profit-sharing/receivers/{psrecv_id}/recover`，仅要求超级管理员的真实登录会话、CSRF、`Idempotency-Key` 和受限证据引用；它不复用普通支付业务管理员授权。它只接受当前分佣开关和 SDK 都已就绪、receiver 可信身份重验通过、旧 effect 为 `final_failed` 且全历史每次 attempt 都标为未调用 Provider 的记录。恢复以旧 effect、管理员、idempotency key 和证据引用绑定；相同请求回放同一新 effect，漂移、AppID 变更或任何未知/外调痕迹拒绝。它保留旧 effect，记录“受控复核：全历史未调用，现配置已校验”的审计，而不把本地推断写成微信侧失败。

分佣 SDK 的认证模式为显式配置：`certificate` 使用完整、有效且受信任的平台 X.509 证书并以 `CertificateVisitor` 构造，在构造时不下载证书；`public_key` 只接受真实 WeChat Pay public key 和匹配的 public-key ID。平台证书 serial 绝不充当 public-key ID，任一模式都不会静默 fallback 或切换 AutoAuth；分佣开关关闭时不新增配置或启动依赖。

Payment worker 或受控 recovery 的 receiver 状态变化会在同一 PostgreSQL UoW 通过稳定 Payment Port 投影到既有 Distribution receiver snapshot，供 `/me`、推广资格和管理端读取。投影不读取网络、不隐式建分销员；状态、AppID、引用与就绪事实均未变化时不写入、不追加审计。普通 GET 永远不写数据库。receiver preparation 同样先更新 Payment 再锁并重读 Distribution snapshot，避免与 Payment 完成回调形成反向锁序。

### 前端

复用 `web/v3/distributionCenter.ts` 的 card、button 和状态呈现：分佣结算关闭时显示准确商户原因且不显示收款准备按钮；状态操作后重新读取 `/me`；不合资格时只显示安全空态，付款确认缺失与资格读取失败只提示联系商家，不能引导重复购买；`receiver_final_failed` 只提示联系商家核验，`outcome_unknown` 仅允许刷新。无需新页面壳、组件或样式模式。

## 一致性与架构分类

- OneID：仅读取现有可信 Payment session、精确 scoped identity 与 canonical lineage；不建客户、不匹配或合并身份。
- 持久化：Payment/Order/payment reconciliation 与审计在既有 PostgreSQL UoW；Distribution 继续只经稳定 Payment/Product/Order Ports。
- 外部效果：receiver-add、分账和退款仍经既有 EER；本修复禁止 disabled 时受理 receiver effect。Provider 查单是 Payment owner 的受签名读操作，事务外执行，验证后才入库。
- 前端：按 component map 复用 public Distribution Center 和既有 CSS，不改冻结 donor。

## 参考与取舍

- [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md)：采用明确状态、可理解反馈和复用既有组件原则；不引入 Ant Design 依赖或重做页面。
- [WeChat Pay Go SDK](https://github.com/wechatpay-apiv3/wechatpay-go)：仅参考官方 SDK 的签名客户端、完整平台证书 visitor 与真实 public-key ID 两种明确认证材料；保留仓库已有 SDK 和配置校验，不自动切换到 AutoAuth。

## 验收

1. 分佣开关关闭时，receiver preparation 不创建 effect/receiver，H5 显示商户能力原因且仍能读注册资料。
2. 未购、退款待核验、已支付但确认时间缺失与资格读取不可用均被服务端从 `items` 排除，并返回精确空态；只有 eligible 商品可列出。不可售/关闭策略商品绝不暴露，确认时间缺失不提示重复购买。
3. Provider 查单恢复覆盖真实管理员会话 dry run/apply、SUCCESS 补证、reference/digest 读回、重放、已有确认时间、金额/币种/订单/交易号/Order paid event 时间不匹配和查单错误；真实 PostgreSQL 证明 Payment+Order+receipt+audit 同事务，且 payment 状态、金额与 `profit_sharing_marked` 不变。
4. receiver 恢复覆盖超级管理员权限、全历史未外调、当前身份/AppID 与 SDK readiness、CAS、同 key 回放、payload drift、已外调与 outcome unknown 拒绝；真实 PostgreSQL 证明旧 effect 未覆盖、新 intent/audit 原子提交。
5. Payment worker/recovery 就绪状态在同一 UoW 更新 Distribution snapshot；重复 ready 不重复版本或审计，且 `/me`、推广资格及管理端读回一致，无再次 prepare 或网络读取。
6. JSDOM/Chromium 验证状态刷新、严格空态/候选动作和禁用状态；完整 CI、独立审计、合并后部署与登录态只读回验分别记录。
7. 真实 PostgreSQL 覆盖被审阅恢复的完整异步链：`RecoverProfitSharingReceiver` 写入新 EER intent 后，Payment loader 必须按 effect source 读回同一 payload；真实 River EER worker 仅在本地 fake SDK 叶子调用一次 `AddReceiver`，随后 effect 为 `executed`、receiver 为 `ready`。receiver、instruction、unfreeze 三种单一 intent owner 均可读取；零个、两个或三个 owner 均 fail closed。
