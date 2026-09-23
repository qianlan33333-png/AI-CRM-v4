# 历史订单切流增量

分类：涉及 OneID，历史身份仍走现有 Identity Port；本增量禁止改变已持久化客户归属（包括 nil 到客户）。归属补齐须先通过现有历史 attribution 并复核。涉及持久化，Order/Payment/Refund 写入由迁移 Composition Root 复用同一个 PostgreSQL Unit of Work；不调用 Provider，不创建外部效果。

## 执行边界

先部署 0127、0129 并在隔离库演练。正常 full Manifest 仍需完整身份、微信支付、退款、小店、支付宝 coverage。所有原始退款状态须保留，处理中/失败/关闭不计成功退款；Provider 实查与源状态证据应分开保存。

新增参数：

```
migrate-commerce-history --mode=apply --snapshot=FULL_MANIFEST --manifest-sha256=EXACT_SHA --history-delta-preconditions=PROTECTED_CAS_FILE --confirm-apply
migrate-commerce-history --mode=reconcile --snapshot=FULL_MANIFEST --manifest-sha256=EXACT_SHA
```

CAS 文件必须为 0600 常规文件且绑定当前 Manifest SHA；其结构为 `{"manifest_sha256":"...","orders":{"SOURCE_KEY":{"source_digest":[32个字节整数],"version":目标版本}}}`。只读查询目标 `orders.source_row_digest/version`，并校对原 `order_import_receipts`，得到前置证据。不要重新计算或猜测原摘要。文件也应保存于受保护备份。省略该参数仍严格拒绝源摘要变化。

- 原订单必须是 `commerce-history`、history、effect_eligible=false，版本及原摘要必须匹配，且有已持久化 import receipt。
- 冻结 Provider、商户单号、金额/币种、商品明细、创建时间、客户归属；非空交易号不得替换。
- 允许源更新时间/新增交易号及非回退状态，已付款可转部分/全额退款；已关闭/已失败不得自行变已付款。累计已退款不得减少。
- 0129 追加不可修改 before/after 摘要、版本、状态及金额。`orders.source_row_digest` 更新为当前事实摘要；旧 import receipt 与原始状态/audit 永久保留。
- 同一个事务导入全部业务订单及支付/退款。任何 Payment/Refund 冲突回滚本次全部订单增量。原有 Payment 不能覆盖：遇唯一键冲突即阻断，需要独立 Payment delta 协议。
- 一个新 run 完成后，原 run 的原始 receipt 仍可审计；当前事实应使用新 run 完整对账。对账验证最新 delta、其旧 receipt、初始状态及本次 status/audit/outbox。

## 当前未解决的外部输入门禁

本工具不替代源规范化：小店退货缺少退款事实、无可信身份、旧订单商业字段变化，均不能靠 delta 伪造补齐。客户端须提供完整经审计 Manifest。没有完整 Manifest、归属未齐或真实在途交易接管未解决时，不能据代码通过宣称可切流。
