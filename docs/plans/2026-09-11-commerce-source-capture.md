# 交易切流原始证据冻结

OneID：捕获源权威身份原始证据，尚不归属、不建客。规范化时必须先审计 namespace 与 Provider 来源，并经现有 Identity Port 处理，不能按裸 UnionID 猜根。
Persistence：仅源 PostgreSQL REPEATABLE READ READ ONLY 事务，完整七张表一致快照。无目标写、无内部任务、无 Provider 效果。

`cmd/migrate-commerce-capture` 输出新0700目录的 `source.enc`（AES-GCM）及0600聚合 `evidence.json`。密钥来自独立0600 base64文件。拒绝覆盖既有证据；缺表/无权限失败，不能当成零行。源记录、密钥、Provider payload不进入stdout/stderr，输出仅计数。完整保留金额、时间、优惠券快照、退款状态、原始回执与身份映射。

```sh
# 在源受保护环境执行；数据库URL使用环境变量，不能把密码写入shell命令记录。
migrate-commerce-capture --mode capture --directory NEW_PROTECTED_DIRECTORY --key-file EXISTING_CAPTURE_KEY
migrate-commerce-capture --mode inspect --directory NEW_PROTECTED_DIRECTORY --key-file EXISTING_CAPTURE_KEY
```

环境变量 `AICRM_COMMERCE_SOURCE_URL`。工具不创建密钥、不导入目标。

## 规范化门禁（尚未满足，故不生成可apply Manifest）

- 微信支付808、退款138；成功退款101共4,983,370分，与源订单累计已退款一致；其他33失败、1关闭、1处理中、2已申请不能当成功。
- 微信小店115订单，其中4 returned但 refunded_amount_total=0 且小店退款表0；不能凭 returned 构造退款号/金额。
- 小店52订单无UnionID，必须从被审计原始身份事实经Identity Port处理，不猜归属。
- 支付宝真实表名为alipay_pay_orders，当前0；仍必须成功读取证明覆盖。
- 后续规范manifest沿用 `aicrm-production:wechat_pay_orders:<旧数字ID>`，原schema及旧source digest不改；已有订单摘要变化仍阻断，必须单独delta协议。

因此 capture成功仅表示原始证据完整冻结。输出固定 `normalized_manifest_ready=false`，不能据此宣称订单可切流。归属审计、4条returned的退款证据补齐、旧订单delta与回调连续性解决后，再实现规范化并用migrate-commerce-history严格验证。
