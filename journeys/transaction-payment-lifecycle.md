# Journey：交易支付与退款闭环

1. 浏览器仅提交一次性微信 `code`；内部小程序 Adapter 调用 `jscode2session` 并用 `wechat-app:<appid>` scope 构造 verified OpenID，服务端才签发 HttpOnly Payment Session，HTTP 自报 identity 字段必须拒绝。
2. 创建 checkout 与 External Effect 接受处于同一 PostgreSQL UoW；Provider 调用发生在事务外。
3. 原支付会话可轮询异步生成的短时 JSAPI handoff，其他 identity 或过期会话不得读取。
4. 微信支付支付/退款 callback 必须验签、解密、校验商户号/AppID/金额/币种并以 event digest 防重放。
5. Payment 与 Order 结算、callback receipt、审计和 Outbox 在同一 UoW 内成功或回滚。
6. 多笔退款的 requested、effect_accepted、outcome_unknown 与 completed 金额均占用可退余额；累计超额返回冲突。
7. 微信支付主动对账只查询原 merchant/refund number；微信小店主动对账只查询 Provider 返回的原 after-sale ID。
8. 微信小店退款前实时读取订单物料，商品、SKU、数量、剩余售后数量、价格任一不一致都不得创建效果。
9. 微信小店 URL 验证与退款回调使用各自官方签名字段；加密正文校验 AppID 后才可关联本地退款。
10. Provider disabled 时所有写和 callback fail closed，且 Adapter 单测证明零业务网络调用。
11. 历史迁移必须按权威 source subject 将多身份原子附着到一个 OneID；跨根命中失败关闭，缺 scope 身份只写无 PII 的隔离回执。
