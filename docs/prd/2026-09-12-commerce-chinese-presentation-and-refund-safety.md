# 交易与优惠券中文展示、退款确认收口

已审核集成基线：`f3e453e288e26798808c02ad6adb17d396c05c61`。本项把管理员交易页中会误导人工判断的展示和退款关联收口为可审查的中文界面；不执行退款、不部署、不修改历史资金事实。

## 分类与边界

- **OneID**：订单只读取既有 Customer/Identity 已归属结果，显示买家姓名和面向业务定位的 `CID-<id>`；内部 canonical `customer:<id>` 只留在协议中，不直接展示。不创建客户、不猜测手机号或重新归属。
- **持久化与外部效果**：订单详情和优惠券列表是读取；退款确认仍使用既有 Payment 同一事务内的幂等收据和 External Effects 接受路径。本轮不增加 Provider 调用或队列，只收紧确认条件，并增加 Payment-owned 的只读收据恢复查询。
- **历史**：`record_origin=history/v1_history` 始终只读、effect-ineligible。退款记录必须由服务端按当前订单的 Payment 关联精确筛选，不能在浏览器把全局退款列表伪装为本订单历史。

## 已核对参考与取舍

- [PR #75](https://github.com/qianlan33333-png/AI-CRM-v3/pull/75) 对历史订单采用已验证 OneID 归属、冲突隔离且禁止外部效果；本项复用既有归属读取，不扩大历史订单的写权限。
- [PR #208](https://github.com/qianlan33333-png/AI-CRM-v3/pull/208) 用 V3 Order Host 保留冻结订单渲染器，并将展示时间固定为 Asia/Shanghai；本项在该边界内补足详情投影，不改 donor。
- [PR #22](https://github.com/qianlan33333-png/AI-CRM-v3/pull/22) 规定 Coupon 经 Product 稳定 Port 取得商品选项；优惠券适用商品显示该 Port 返回的中文名称，绝不读取 Product 表或猜测 `target_ref`。

## 问题和规则

1. 订单详情分为“订单信息、支付信息、买家信息、商品与金额、退款记录”。显示买家姓名、`CID-<id>` 客户编号、订单号、脱敏手机（仅服务端已给出时）、商品中文名和付款金额；不显示重复的内部 `id`、source key、digest 或同一支付事实的多种技术别名。
2. 所有管理员可见订单、退款、优惠券状态和操作反馈使用中文文案；同名状态按订单、退款、外部处理等领域分别映射。后端 API 的 enum 和错误 code 保持原协议；前端以安全中文兜底文案显示，诊断详情不把 raw enum 直接暴露在界面。
3. 可见的时间统一固定为 Asia/Shanghai，格式严格为 `YYYY-MM-DD HH:mm:ss`，不显示 `T`、`Z`、毫秒或时区后缀；列标题不加“北京时间”。API RFC3339 和数据库 UTC 不改变。日期输入、筛选、计划时间和导出可见时间都须盘点；共享 `web/v3/adminDateTime.ts` 只将带 offset/Z 的 instant 转上海、保留纯日期日历日，且只有来源合同明确为上海壁钟时才显示无时区历史字符串。上海跨日/月边界有测试。
4. 本轮微信支付退款确认只接受当前 WeChat Pay Payment 已验证的微信 `transaction_id`。商户订单号、`v3pay` 订单号或任意其他订单字段都不能替代；当前记录没有可验证的 `transaction_id` 时，明确显示“缺少微信支付交易单号，不能确认退款”，并禁止提交。WeChat Shop 沿用其既有 `order_no` 精确核对：当前没有可信 Shop `transaction_id` 来源，不能伪造验证事实或把微信支付规则迁入该渠道。
5. 优惠券一级列表以 Coupon Owner 的既有 Product Port 解析 `target_refs` 为真实中文商品名，领取时间按统一格式展示，状态中文化；未知/已删除目标须可解释，不能伪造名称。
6. 前端退款锁只持久保存 Payment scope、原幂等键、请求 SHA-256 摘要、锁定状态、服务端给出的非秘密 `actor_binding` 本地分区标识，以及在有效 `202` 收据后得到的退款收据 ID/编号；不得持久化交易单号、退款原因、请求体或原始管理员 ID。`actor_binding` 不参与授权，每次恢复/提交仍由服务端当前 session principal 鉴权；提交前会重新读取该标识。存储读取或格式异常时不覆盖已有内容，停在只读核验状态。`200` 登录页、错误/截断 JSON、非 `202` 或断流都只能进入“结果待核对”，不能重发或另起键。
7. 待核对锁通过 `GET /api/admin/refunds/recovery` 只读恢复：原键只在 header，Payment 以 provider + order 和当前 admin actor + 原键三重精确匹配。响应没有外部效果状态，避免把退款业务状态误当 Provider 效果投影。未找到仍待核对；找到的收据也必须在当前精确退款页中匹配相同 ID/编号且已终态，并在退款列表和订单详情均成功重新读取、得到新鲜可退额后，才由用户明确点击“开启新的退款申请”。任何同订单但不同收据、不同 actor/key/Payment 的记录均不能解锁。
8. 同一非历史 WeChat Pay Payment 在 Payment 行锁和同一 UoW 中，原键重放之后检查既有退款：存在 `requested`、`effect_accepted` 或 `outcome_unknown` 时拒绝不同 key 的新申请；只有该笔退款已终态后，才允许以新 key 发起后续部分退款。WeChat Shop 的 SKU 退款合同本项不改变。历史退款状态保留 `history_requested`、`history_processing`、`history_failed`、`history_closed` 四种来源事实，未知状态只标记待核对，不能猜成退款失败。

## 交付切分

- **A：订单详情与退款确认安全**：服务端按 provider + merchant order/Payment 关联筛选退款，并由 Payment 自有 Port/UoW 做 actor/key-scoped 只读收据恢复；详情渲染、退款确认、中文状态与 `adminDateTime` 上海时间；覆盖跨订单退款负例、历史只读、`transaction_id` 缺失/错误/正确以及跨日/月显示。
- **B：优惠券中文投影**：经 Product Port 的商品名、中文状态和上海领取时间；不触及 Payment 写路径。
- **后续全站接入**：从页面库存逐页接入同一最小 source-owned formatter/status mapping，覆盖显示、输入、日期筛选、计划时间和导出可见时间；不作全仓 grep 替换。

## 验收

- 订单 A 的集成测试建立两个 Payment/Order；请求一个订单只返回其退款，任何其它订单退款均不出现。
- WeChat Pay 的 `transaction_id_confirmation` 与已验证值不相等、为空、或以商户/v3pay 订单号替代时为 400；只有精确微信交易单号可通过该确认门，且测试不发起外部调用。WeChat Shop 保持既有 `order_no` 精确核对，不伪称具备微信支付交易单号。
- 退款确认仅在 `202` 和完整有效收据时显示“已受理”；登录页、坏 JSON、5xx、断流与未知错误保持同一键待核对。服务端恢复查询需证明不同 Payment、actor 或 key 均不匹配；前端在 actor 读取失败时只允许手动只读重试，在精确收据终态且退款页与订单详情都重新读取成功时才显示新的明确申请入口。
- 两个 admin actor 使用不同 key 并发提交同一 WeChat Pay Payment 的小额退款时只接受一笔；原 key 可重放，终态后可按剩余金额明确提交下一笔部分退款。
- 浏览器/adapter 测试验证分区、中文状态、客户显示、脱敏手机号、金额和退款历史只读边界；时间在 UTC 前一日/跨月时仍显示上海日期和秒。
- 优惠券测试验证真实 Product Port 名称、中文状态、无“北京时间”标题和固定上海格式；无可解析 Product 事实不伪造商品名称。
