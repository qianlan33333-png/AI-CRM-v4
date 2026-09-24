# 支付宝公开结算读回与创建恢复缺陷 PRD

## 业务判断流程

```mermaid
flowchart TD
  A[用户从配置的 H5 地址打开并选择支付方式] --> O{精确 POST /api/v1/alipay/checkouts 且 H5 Origin 匹配?}
  O -->|否| R[外层 Origin 防护拒绝]
  O -->|是或微信路由| B[校验已有可信付款会话]
  B -->|首次请求返回无写入 401| C[清除空 checkpoint 并重新授权]
  B -->|会话有效| D[保存冻结请求与幂等键]
  D --> E{本地是否已有商户订单号}
  E -->|没有| F[向冻结的 Provider 路由提交同一请求]
  F -->|订单创建成功| G[保存商户订单号]
  F -->|首次确定性拒绝或 Origin 403| H[清除空 checkpoint，显示明确失败]
  F -->|超时、断连或响应不明| U[保留原键和冻结请求，提示结果待核对]
  U --> F
  E -->|有| I[只读查询该 Provider 的原订单状态]
  G --> I
  I -->|已支付| J[显示服务端确认的完成状态]
  I -->|支付宝付款链接就绪| K[展示原订单 WAP/Page 链接]
  I -->|微信支付参数就绪| L[调用微信支付桥]
  I -->|结果未知或暂不可用| M[保留订单与幂等身份，提示稍后核对]
```

## 缺陷与目标

页面已有支付宝选项与 WAP/Page 渠道字段，但调用和读回仍硬编码到微信路由；服务端 `GetCheckout` 也固定读取微信付款记录及微信 prepay effect。支付宝支付无法从公开页面读回自己创建的订单/付款链接。另一个页面恢复问题是：下单前已保存 key 的 checkpoint 没有商户订单号时，价格区域显示“待确认”，混淆了订单创建状态与付款金额；确定性业务拒绝和响应不明也曾统一显示成待核实。

目标是让创建、状态查询、effect 读回及支付宝完成动作都以已冻结的 Provider/Channel 为准，明确区分首次提交的确定性业务拒绝与超时/响应不明：前者清除无订单 checkpoint 并恢复可编辑状态；后者保留同一个请求键与冻结请求安全重试，且旧 checkpoint 在没有服务端订单读回前不会被一次新的拒绝响应清除。没有商户订单号时显示创建状态，不显示付款金额待确认。

## 外部参考

- [支付宝官方：通知页与返回页的职责](https://help.alipay.com/support/help_detail.htm?help_id=397355)：同步 return 只负责把交易信息带回商户，最终业务状态应由服务端通知/查询核实；WAP/Page 返回值是用户继续支付所需的链接。
- [Stripe 官方：Idempotent requests](https://docs.stripe.com/api/idempotent_requests)：网络错误重试必须复用原幂等键，避免重复创建对象。
- [Alipay EasySDK 官方 GitHub 文档](https://github.com/alipay/alipay-easysdk/blob/master/APIDoc.md)：将 WAP 与 Page 作为不同网页支付入口处理，按设备场景构造对应导航 URL。

## 仓库复用与边界

- 复用 `internal/payment/http` 已存在的 `/api/v1/wechat-pay/checkouts` 与 `/api/v1/alipay/checkouts`；按路由 Provider 校验请求，读回只授权对应 Provider 的订单。
- 外层跨站防护仅将精确 `POST /api/v1/alipay/checkouts` 绑定到配置的 H5 Origin。相邻路径、尾随斜线、其他方法及其他 Origin 不继承此例外。
- 复用 `Payment Service`、`Payment Store`、`external_effects.Reader`、`payment_handoffs` 和现有产品页 checkpoint / 支付宝链接区域；不新增身份匹配器、订单表、任务队列、重试状态机或 Provider。
- 支付宝旧 intent 缺少 `subject` 时，由 Provider material loader 在同一 Unit of Work 内通过 `Order CheckoutSnapshotReader` 读取冻结标题与金额；Payment Store 不读取 `order_items`。新 intent 若已保存 `subject`，必须校验其格式并与 Order 冻结标题一致，不接受不一致或无效标题。
- WAP/Page 对应 `alipay_wap_pay_v1` / `alipay_page_pay_v1`；微信仍使用 `wechat_pay_prepay_v1`。客户端只认服务器状态，不将打开付款链接或点击“我已支付”当作成功。

## 数据、身份与外部效果分类

- **OneID：涉及既有可信付款会话。**从 Payment session 取得 payer 身份并复用当前授权；不接受浏览器自报客户 ID，不解析/创建/合并新的支付宝身份。
- **Persistence：本地 PostgreSQL 事务。**Order、Payment、幂等收据及 Provider intent 继续由现有 Payment/Order Unit of Work 原子持久化；付款链接由 Payment owner 持久化到 handoff。
- **External Effects：复用 Payment effect。**effect 类型由 Provider 与 Channel 决定。读取 effect 状态不发起 Provider 调用。集成验收用确定性的虚拟 Provider 链接，不访问支付宝、不扣款。
- **Owner 与事务边界：**Order owner 持有订单；Payment owner 持有 Payment、intent、handoff。创建期间保持当前同一 Unit of Work，Provider 处理仍在事务外；effect 完成与 handoff 写入沿用现有完成事务。

## 验收标准

1. `/api/v1/alipay/checkouts` 只能创建支付宝订单；WAP 与 Page 分别生成匹配的 effect kind。错误 Provider、未知 Provider 和无效 Channel 失败关闭。
2. 配置 H5 Origin 的精确支付宝 checkout POST 可通过外层 Origin 防护；canonical/无关 Origin 和尾斜线路由在进入 Payment handler 前返回 403。
3. `/api/v1/alipay/checkouts/{merchant}` 只读回原支付宝订单、金额、effect 状态和有效 handoff；错误 Provider 路径不能读取另一渠道的订单。微信控制组行为保持不变。
4. 完整迁移序列上的隔离 PostgreSQL 16 HTTP 旅程分别覆盖 Alipay WAP、Page、未知 Provider、错误 Channel 和微信路由误读控制；WAP/Page 必须由 worker 生成 `.test` 虚拟链接，并核对订单、支付、intent、effect 与 handoff。另以现有 `TestPostgreSQLPublicCheckoutResponseLossRejectsRenewedSessionReplay` 做微信创建响应丢失及终态回读对照；该对照不启动微信 Provider worker。所有旅程不得触发真实 Provider 支付。
5. 首次 POST 的已知业务拒绝、无写入 401 和 Origin 403 清除无订单 checkpoint，恢复授权/表单流程；网络超时/断连/响应丢失保留 checkpoint。其后重试沿用同一 key、冻结请求与 Provider 路由；旧的不确定 checkpoint 不因后续拒绝或新授权而被误清除。无商户订单号时金额区不显示“待确认”，且同 key 重放不得多建订单。
6. 浏览器状态恢复、支付宝“我已支付”读回与刷新均使用原 Provider 路由；只有服务器 `paid` 状态显示支付成功。
7. 对生产旧 intent 的兼容仅适用于 `subject` 键不存在的记录；冻结 Order 快照缺失、金额不一致、标题无效、已有但错误的 `subject` 都失败关闭，不调用支付宝。

## 回滚与风险

无数据库迁移。回滚为恢复页面与 Payment 读回逻辑；保留已创建订单和其幂等 checkpoint，不生成替代订单。主要风险是错误 Provider 路径导致他渠道的订单泄露或状态查询错误，因此服务端必须用 URL Provider 与持久化 Provider 双重匹配，并继续执行原 payer session 授权。
