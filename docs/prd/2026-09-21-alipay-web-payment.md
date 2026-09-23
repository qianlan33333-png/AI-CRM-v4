# 支付宝手机网站支付与电脑网站支付接入 PRD

## 目标

在当前 AI-CRM-v3 的商品 checkout 中增加支付宝手机网站支付（`alipay.trade.wap.pay`）和电脑网站支付（`alipay.trade.page.pay`），复用现有 Payment/Order/External Effects/OneID 可靠性边界，不改变现有微信支付行为。

## 已确认范围

- 手机网站支付与电脑网站支付；
- RSA2 普通公钥模式首发，保留证书模式扩展位；
- 支付异步通知、同步回跳、主动查单、退款、退款查询；
- 订单号、金额、币种、应用身份、通知验签和防重放校验；
- 支付宝身份仅在 Provider 验证事实成立后通过 OneID 关联，不接收 HTTP 自报身份；
- 开放平台“接口内容加密方式”属于应用级可保留配置：本期支付不依赖手机号授权或 AES 业务字段；若平台已启用，可通过受保护的 `AICRM_ALIPAY_CONTENT_ENCRYPTION_KEY_PATH` 加载密钥，运行时只记录是否已加载，不记录密钥内容；
- 沙箱测试与真实小额支付验收分别记录，不能互相替代。

## 不在本期

- App 支付、当面付、预授权、周期扣款、分账和转账；
- 支付宝登录/授权作为独立产品；
- 新建支付队列、重试表、回调状态机或客户主键。

## 架构分类

```text
OneID：读取 canonical customer / 解析已验证支付宝身份；不隐式建客、不自动合并
Persistence：本地 Order/Payment 事务 + External Effect 持久任务 + Provider 读取/写入
External Effects：支付宝下单、退款属于 Provider 写入；查单属于 Provider 读取；回调必须验签并幂等落库
```

## 业务规则

1. 创建 checkout 时生成稳定 merchant order number 和支付宝 effect idempotency scope；同一业务意图重放不得生成新订单。
2. Provider 调用在数据库事务外执行；业务状态、effect 绑定、审计和 callback receipt 按现有 Unit of Work 原子提交。
3. 同步回跳只用于展示/恢复页面；支付成功只由验签后的异步通知或可信查单确认。
4. 异步通知必须校验签名、`app_id`、订单号、金额、币种和终态；重复通知返回成功但不重复结算。
5. `outcome_unknown` 只能原订单号查单或重放可信回调，不得换幂等键盲目重试。
6. 退款金额不得超过可退余额；退款请求和退款查询沿用现有 Payment 退款幂等语义。

## 官方文档核对后的配置契约

- `ALIPAY_ENABLED`
- `ALIPAY_APP_ID`
- `ALIPAY_SIGN_TYPE=RSA2`
- `ALIPAY_GATEWAY`（生产固定 `https://openapi.alipay.com/gateway.do`，沙箱由受保护部署配置）
- 应用私钥安全引用
- 支付宝公钥安全引用（后续可扩展证书三件套）
- `ALIPAY_NOTIFY_URL`
- `ALIPAY_RETURN_URL`
- 可选：`AICRM_ALIPAY_CONTENT_ENCRYPTION_KEY_PATH`（仅当平台接口内容加密已启用且查单/退款响应需要解密时配置）
- 请求超时与环境标识

按支付宝官方 `llms-full.txt` 的接入准备要求，还必须在开放平台完成：应用与商家账号绑定、对应支付产品开通、应用上线、接口加签，以及公网应用网关配置。支付结果通知地址仍由具体支付 API 的 `notify_url` 传入；应用网关不能替代 `notify_url`。

官方服务端 SDK 当前列出的语言不包含 Go；本仓库是 Go 项目，因此采用经过审查的 Go SDK 作为 Provider 内部实现，业务代码不直接依赖 SDK，未来可替换而不改变 Payment Port。

密钥内容不得进入配置快照、结构化日志、Git 或 API 响应。

## 验收

- 单元：RSA2 配置、URL 构造、通知验签、金额/身份校验、重复通知、退款边界；
- PostgreSQL：checkout/effect/receipt/审计原子性与并发幂等；
- 浏览器：手机网站支付和电脑网站支付跳转、回跳、状态恢复；
- Provider：沙箱下单/查单/退款与异步通知；
- 发布：当前 tree 的完整本地验证、预发布部署、认证读回、真实小额支付和观察窗口。

## 参考与复用评估

- 支付宝官方网页支付接口：`alipay.trade.wap.pay`、`alipay.trade.page.pay`、RSA2、`notify_url`；
- `smartwalle/alipay/v3`：支持普通公钥/证书、公钥验签、WAP/Page/Query/Refund；
- `go-pay/gopay`：提供统一支付抽象和通知解析参考；
- 复用当前仓库 `internal/payment`、`internal/externaleffects`、`internal/identity/port`；不复制旧仓支付实现。
