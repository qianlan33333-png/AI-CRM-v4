# Payment

Payment 是支付、退款、Provider 回调、主动查询对账和支付会话的唯一 Owner。当前实现包含：

- `/api/v1/wechat-pay/sessions` 用一次性小程序 code 经 Provider `jscode2session` 换取带 AppID scope 的 verified OpenID，再签发 HttpOnly、Secure、SameSite=Strict Payment Session；浏览器不能自报 OneID。
- 微信支付 JSAPI prepay、短时 actor-bound handoff、支付/退款回调验签解密和原单号主动对账。
- 微信小店订单物料实时校验、退款 External Effect、加密回调和原 after-sale key 主动对账。
- 微信支付不确定结果和微信小店回调都在同一 UoW 写防重放/效果事实并进入 River `payment-reconciliation` 持久任务；Worker 在事务外用原商户单号、原退款单号或原 after-sale key 精确查询。
- Provider 明确返回支付终态失败时，Payment 与 Order 在同一 UoW 结束待支付状态；退款终态失败会释放累计退款预留，不推进订单退款金额。
- 管理端退款列表、真实 External Effects 时间线、CSRF/RBAC/幂等和累计可退金额锁定。
- Payment 与 Order 在同一 PostgreSQL UoW 内提交结算事实、收据、审计、Outbox 和 EER 完成投影。

两个 Provider 开关彼此独立且默认关闭。关闭时管理端写和公开回调返回不可用，并保证不发网络请求；历史交易始终只读且不会生成 External Effect。`accepted/queued/executed` 均不等于支付或退款成功，只有验签回调或精确查询对账可以推进终态。`outcome_unknown` 禁止换幂等键重试。

## 开发前分类

```text
OneID: verified scoped identity only; no implicit customer creation from HTTP claims
Persistence: shared PostgreSQL UoW + durable External Effects queue
External Effects: WeChat Pay prepay/refund and WeChat Shop refund; Provider reads stay outside transactions
```
