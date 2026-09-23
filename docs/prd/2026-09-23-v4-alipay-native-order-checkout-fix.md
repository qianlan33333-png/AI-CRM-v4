# 支付宝收银台卡在“待确认”缺陷合同

## 业务判断与根因

用户选择支付宝后，应在同一笔订单的幂等边界内创建原生订单、支付记录和外部效果意图，然后取得可跳转的支付宝支付入口。当前支付模块已识别支付宝 WAP/Page 渠道，但 Order 的服务、领域校验和 PostgreSQL 约束仍禁止原生支付宝订单，导致创建阶段返回冲突，页面只剩“待确认”。

## 范围与复用

- 只放开 `alipay` 原生订单的三道旧限制；复用现有 Order 原子事务、Payment 意图、回调验签、幂等收据和对账流程。不新增 Provider 调用或重试机制。
- 保留原生订单必须有付款人与受益客户、`effect_eligible=true`、无历史摘要的约束。历史支付宝订单继续不可发起外部效果。
- OneID：读取已解析的 canonical payer/beneficiary；不新建身份、不改变归属。
- Persistence：Order/Payment/效果意图在现有 PostgreSQL Unit of Work 内；支付宝支付是 Provider 写入，沿用现有 Payment External Effects 合同。
- 数据 Owner：`orders` 表由 Order 领域拥有。迁移只调整其来源与效果形状约束。

## 验收与边界

1. 同一支付宝创建命令得到原生订单；同键重放仍为同一订单，载荷漂移冲突。
2. PostgreSQL 迁移后允许合规的原生支付宝订单，仍拒绝缺客户或错误效果资格的订单。
3. 微信渠道行为及历史支付宝订单不可产生效果的约束不变。
4. 本地专项测试证明合同；预发布要用当前 V4 main + PR head 构建、安装并完成支付页旅程。虚拟 Provider 旅程只证明跳转合同，不等同真实支付宝扣款。生产真实小额付款、回调和订单读回由发布观察阶段另行验证。

## 参考与回滚

- 仓库现有 `internal/payment/app/service.go` 的支付宝 WAP/Page 意图和 `migrations/0202_alipay_web_payment.sql` 是直接复用点。
- GitHub 参考检索：[`guidao/gopay`](https://github.com/guidao/gopay)、[`milkbobo/gopay`](https://github.com/milkbobo/gopay) 均有微信/支付宝多渠道支付抽象。本缺陷是本仓 Order 资格约束错配，不引入外部 SDK。
- 回滚：发布前可撤回候选；安装后不得直接恢复禁止原生支付宝的约束，因为可能已有合法新订单。若需停用新支付，使用既有 `alipay.provider_enabled` 开关并只读对账。
