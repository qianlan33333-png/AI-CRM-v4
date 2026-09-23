# 支付回调与支付完成动作读取最新配置 PRD

## 业务判断

支付成功是一个必须在同一 PostgreSQL 事务内完成的业务事实：支付回调验签、Payment/Order 结算、幂等回执以及已支付后的既有业务消费者不得拆成多个独立提交。微信支付会在非 2xx 或超时后重试，因此回调必须区分验签拒绝、业务冲突和暂时不可用，并保留原有幂等键；不能通过伪造支付状态或更换幂等键“修复”订单。

支付完成时的商品执行配置取支付完成瞬间的 Product 当前版本。订单创建时的 checkout snapshot 继续作为金额、商品、资格和审计事实，但不能决定支付完成后的最新动作。已支付事件重放必须使用同一已落库动作收据，不能重复外部效果。

## 前置调研与复用评估

- 微信支付官方 Go SDK 的回调流程要求先验签、解密，再依据回调结果返回成功；官方文档强调使用平台证书/APIv3 密钥验证回调。
- GitHub Webhook 最佳实践要求快速返回 2xx、支持重试与幂等；本项目沿用已有 callback receipt、UoW 和 reconciliation，不新增队列或重试内核。
- 复用 `internal/payment/provider.CallbackVerifier`、`payment/app.ApplyVerifiedCallback`、Order `SettlePaymentWithin`、现有 `PaidEventConsumer`、`internal/platform/jobqueue` reconciliation 和 Product `PaidPurchaseActionStore`。不引入第二套支付回调、身份匹配或外部效果执行器。

## 架构分类

OneID：不涉及。回调只校验 Provider 返回的 appid/mchid 并关联已有 Payment/Order，不解析、建立或合并客户身份。

Persistence：Provider 写入/外部效果 + 本地事务。回调在同一 PostgreSQL UoW 内写入支付状态、Order paid 事实、幂等 receipt 和既有 paid-event intents；不新增 durable job。Provider 查询仅复用现有 reconciliation Port。

## 范围与成功标准

1. 合法的微信支付成功回调返回 HTTP 200/SUCCESS，并将对应 Payment/Order 置为 paid；同一回调重试不重复结算或外部效果。
2. 非法签名、appid/mchid/currency 不匹配仍返回 401；金额、订单或已结算事实冲突按既有冲突合同处理，不被错误转为成功。
3. 回调应用失败有固定、可操作的诊断阶段，暂时不可用时由既有 Provider reconciliation 继续恢复，禁止吞掉错误或人工标记已支付。
4. 首次支付完成事件读取并冻结 Product 当前动作配置，而不是订单创建时的动作 snapshot；历史 snapshot 只用于审计和校验。已落库动作重放保持幂等。

## 测试与上线

- 增加回调验证、应用结算、重复回调和错误分类回归测试。
- 增加 Product 当前配置在下单后修改、支付完成后读取新动作、旧动作不会执行的 PostgreSQL 集成测试。
- 预发布使用虚拟支付验收：pending→paid、幂等重放、历史 snapshot 保留和当前动作读回；不连接真实商户扣款。
- 生产部署后独立读回 release SHA、`/readyz`、服务状态、认证管理端和支付/商品动作真实数据；真实支付回调需单列为 Provider 验收，不能用构建或健康检查替代。

## 回滚与风险

若预发布回调事务、schema、receipt 或当前动作读回失败，停止晋级。生产观察到回调 5xx、重复效果或版本/tree/package 不一致时回滚到已验证 release；不得直接改写支付状态。外部微信商户配置若与代码配置不一致，标记为 `external_config_unavailable` 并保留证据。
