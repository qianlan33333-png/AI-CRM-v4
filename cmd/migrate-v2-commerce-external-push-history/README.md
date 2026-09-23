# V2 商品外推历史

`migrate-v2-commerce-external-push-history` 将冻结的 V2 商品外推证据写入 Outbound 的**只读历史账本**。它不创建当前 `outbound_commerce_push_intents`、订单付款事件、External Effect、River 任务或 Provider 调用；旧的待处理订单和投递不会被重新投递。

OneID 不涉及：快照不解析、创建或关联客户。持久化是一个串行化 Outbound 历史账本事务。商品只通过已批准的 `config_definition_import_source_maps` 读取映射，命令不写 Product 数据。

## 冻结来源合同

固定供体是 `AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`：

- `migrations/versions/0023_product_external_push.py` 的 `external_push_config`、`external_push_delivery`、`domain_event_outbox`；
- `migrations/versions/0039_external_effect_queue.py` 的 `external_effect_job`；
- `platform/external_push/repo.py` 的 delivery 字段和
  `extensions/commerce/commerce/external_push_admin.py` 的
  `transaction.paid`/`domain_event_outbox_id` 关联。

一次 `REPEATABLE READ, READ ONLY` 提取保留配置、投递、`transaction.paid` outbox 和每条投递的完整 effect-job 关联数组。供体的 `external_effect_job(target_type,target_id)` 只有非唯一索引，故这不是一对一关系；管理员修复逻辑中“最新 succeeded”仅用于修复旧投递投影，不能作为历史归属选择规则。

V2 密封快照为每条投递记录全部关联的 job ID、effect type 和状态，并将该数组纳入源行摘要。目标只读账本仍有单个 `source_effect_job_id`/`source_effect_state` 投影：零关联保留空值，单关联保留原值，多关联明确写为 `source_effect_job_id=NULL` 和 `source_effect_state=ambiguous_multiple_effect_jobs`。这不是终态或成功标记；完整关联只保存在密封快照并参与摘要/校验，绝不任取最新、最大或其他单条 job。

Webhook URL、密钥、请求/响应体和完整关联数组仅留在 AES-256-GCM 的 0600 受保护快照中，用于源摘要验证；目标账本只保存源 ID、订单来源 kind/scope/key、状态、时间、尝试次数、上述安全投影、商品映射、分类，以及旧管理页可见的响应状态和错误诊断；不保存这些敏感内容。

## 操作流程

先准备一个已有的普通 0600 无填充标准 Base64 编码的 AES-256 密钥文件，并在仅供离线命令使用的环境中设置 `AICRM_SOURCE_DATABASE_URL`。不要把 URL 或密钥放在参数或日志中。

```sh
install -m 600 /dev/null /secure/aicrm/commerce-push-history.key
# 在受保护路径写入 无填充标准 Base64 编码的 32-byte key。

AICRM_SOURCE_DATABASE_URL=... \
go run ./cmd/migrate-v2-commerce-external-push-history \
  --mode=extract --source-revision=V2_GIT_REVISION_40_HEX \
  --snapshot=/secure/aicrm/commerce-push-history.sealed \
  --snapshot-key-file=/secure/aicrm/commerce-push-history.key

go run ./cmd/migrate-v2-commerce-external-push-history \
  --mode=inspect --snapshot=/secure/aicrm/commerce-push-history.sealed \
  --snapshot-key-file=/secure/aicrm/commerce-push-history.key

go run ./cmd/migrate-v2-commerce-external-push-history \
  --mode=dry-run --snapshot=/secure/aicrm/commerce-push-history.sealed \
  --snapshot-key-file=/secure/aicrm/commerce-push-history.key \
  --manifest-sha256=EXACT_DIGEST

go run ./cmd/migrate-v2-commerce-external-push-history \
  --mode=apply --confirm-apply \
  --snapshot=/secure/aicrm/commerce-push-history.sealed \
  --snapshot-key-file=/secure/aicrm/commerce-push-history.key \
  --manifest-sha256=EXACT_DIGEST

go run ./cmd/migrate-v2-commerce-external-push-history \
  --mode=verify --snapshot=/secure/aicrm/commerce-push-history.sealed \
  --snapshot-key-file=/secure/aicrm/commerce-push-history.key \
  --manifest-sha256=EXACT_DIGEST
```

`apply` requires the exact sealed-snapshot digest and writes one receipt for that immutable snapshot. A re-run with the same snapshot returns the prior receipt. A later snapshot from the same V2 code revision may add source rows; an overlapping source row retains one global history identity and any changed source-row digest (including its complete effect-job array) fails closed. `verify` recomputes each protected row digest and checks target state, timestamps, safe relationship projection, mapping, outcome and read-only fact before marking the batch reconciled.

V1 sealed snapshots remain readable with their original authentication data and digest semantics. New extraction emits V2 snapshots; it does not reinterpret an existing V1 history row.

## 订单来源坐标

旧 external_push_delivery.order_id 是 V2 wechat_pay_orders.id：供体在创建投递时把该 V2 ID 写入 delivery，且读取/投递都经 get_order_by_id 查询同一张 V2 订单表。提取器因此保存 wechat_pay_order / commerce-history / decimal(V2 order_id)，从不把它当作 V3 orders.id。只有另一个已冻结 commerce-history manifest 的 source_key 恰好等于这个十进制 V2 ID 时，Order 的稳定读 Port 才会显示该投递；否则历史订单页保持 pending。例如仓库测试用完整 manifest 的 source_key 是 wechat-pay-order-001，不能从该字符串推断数值 V2 ID。
