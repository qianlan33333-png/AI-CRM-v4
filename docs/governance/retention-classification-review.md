# 数据生命周期逐表分类证据复核

日期：2026-09-18。此报告审定原保护清单中 376 个未分类表及 Referral 新增 13 表，共 389 表。它是供主分支合并的显式分类差异，不是删除脚本，也不把其他已登记表的策略回退。机器可读依据见 [retention-classification-review.json](retention-classification-review.json)。

OneID：本次只读核验身份表的生命周期与引用，保留归属、来源、冲突及加密原件，不解析、建立或更改客户关系。Persistence：只读 DDL 和 Owner 存取合同；不写数据库、不调用 Provider、不新增任务状态机。真正的清理须由表 Owner 的 Port 与现有 River 调度执行，同一事务提交删除和计数。

## 依据与分类含义

`baseline_registry_commit` 为 af0923173e9d1cb68e4d5c9704b0da01e9900681；Referral 依据 10c3e0a963c88d18ca8fe09fdd6df0b11d5cbd89 的 0185 迁移与 Owner Store。每项列出首次 DDL、后续迁移、实际字段、外键及非测试 Owner 读写引用。已逐领域阅读 DDL/访问摘要，并对时效令牌、快照、运行记录、投影和外部效果关联进一步核验。策略来自这些合同，不以表名中的 run/cache/snapshot 判断可删。

| 类别 | 本次数量 | 含义 |
|---|---:|---|
| `permanent` | 350 | 永久业务事实、版本定义、审计、资金/身份/效果收据；禁通用年龄删除 |
| `protected_mixed_payload` | 19 | 业务/防重证据与候选过程字段混合；整行禁删，逐项注明字段 |
| `operational_detail_30d` | 2 | 已确认纯过程记录；本报告不启用执行器，须接 Owner 清理入口 |
| `owner_projection` | 8 | Owner 管理的当前读取投影；需完整源及替换/引用合同，禁通用年龄删除 |
| `runtime_coordination` | 7 | 游标、锁、水位、运行器注册等正确性状态；禁年龄重置 |
| `security_ttl` | 2 | 遵守现有过期/撤销合同，不延长到30天 |
| `protected_unclassified` | 1 | 明确证据不足，未知禁止删 |

`permanent` 表示生命周期工具不能以年龄删除业务事实，不取消 Owner 已有的合法业务删除/撤销/归档操作，也不把其中所有运行计数字段声明为永久有价值。混合表已明确保持保护，需先分离再执行。所有本报告条目的 `cleanup` 均为 `disabled`；这不是声称30天覆盖已完成。

## 两个已确认的过程清理入口

`config_runtime_usage` 只写消费观察、供管理页读取。自动化的配置 revision/人数上限已经在预览和运行中冻结；该表 UNIQUE 仅去重观察，不是业务执行收据。因此可按 `used_at < now()-30d` 清理。0094 存在 statement 级 append-only trigger，直接 DELETE 会失败；新迁移须保留 UPDATE/TRUNCATE 约束并仅开放已满30天的删除，不能全局禁用保护。配置版本、发布审计、command receipts、runtime applications 均保留。

`message_archive_sync_runs` 只承载执行页数、计数和错误码。消息业务防重由 `(corp_scope,msgid)` 唯一键承担，持久断点为 `sync_state.last_seq`，其他表无外键指向 run。仅 `status IN (succeeded,failed)` 且 `finished_at < cutoff` 的终态可删；running、消息、参与人、媒体、游标及未归档失败原件不动。`FinishRun` 的终态重放原本就是无操作，清理不能改变消息去重或游标推进。

两个 Owner 均须提供每批不超过1000、单并发、同一事务清理与计数的 Port，补充 cutoff 边界、非终态保留、重复清理/重放以及事务回滚验证。主任务将另行集成真实入口；本分类报告不替代实现/数据库测试证据。

## 混合表：当前禁删的具体边界

| 表 | 永久/正确性字段 | 仅供 Owner 审定的过程候选 |
|---|---|---|
| `customer_owner_handoff_preview_rows` | `preview_id`, `line_no`, `customer_id`, `expected_local_owner_version`, `relation_digest` | `source_userid_ciphertext`, `target_userid_ciphertext`, `external_identity_ciphertext`, `welcome_message_ciphertext` |
| `customer_owner_handoff_previews` | `id`, `actor_admin_user_id`, `request_digest`, `executed_batch_id` | `expires_at` |
| `hxc_dashboard_refresh_runs` | `id`, `run_key`, `request_digest`, `projection_id`, `status` | `error_code`, `source_count`, `processed_count` |
| `idempotency_receipts` | `idempotency_key`, `payload_hash`, `status`, `response` | `attempt_count`, `next_attempt_at`, `lease_owner`, `lease_expires_at`, `last_error_code` |
| `identity_link_intents` | `id`, `source_customer_id`, `consumption_fingerprint`, `consumed_evidence_id`, `consumed_identity_id`, `consumed_customer_id`, `result_candidate_id`, `result_conflict_id` | `token_hash`, `metadata_json` |
| `message_archive_ingest_issues` | `corp_scope`, `seq`, `msgid`, `stage`, `reason_code`, `payload_digest`, `protected_payload` | 未确认任何可删字段 |
| `operation_cycle_action_requests` | `request_id`, `strategy_key`, `run_key`, `action_key`, `strategy_version`, `status`, `final_result`, `idempotency_key_digest` | `lease_token_hash`, `lease_expires_at`, `failure_code` |
| `outbound_commerce_push_history_rows` | `source_id`, `source_digest`, `source_effect_job_id`, `source_effect_state`, `source_response_status`, `source_response_body_protected`, `outcome` | `source_error_message`, `source_attempt_count` |
| `outbound_material_preparations` | `id`, `operation_key_digest`, `operation_command_digest`, `source_ref`, `content_digest`, `effect_id`, `state` | `media_id`, `provider_created_at`, `expires_at`, `failure_code` |
| `outbound_material_refresh_items` | `round_id`, `cache_key_digest`, `preparation_effect_id`, `state` | `failure_code`, `source_count` |
| `outbound_material_refresh_rounds` | `id`, `local_date`, `round_kind`, `operation_key_digest`, `state`, `river_job_id` | `cursor`, `total`, `queued`, `succeeded`, `failed`, `unknown_count` |
| `outbound_sidebar_image_preparations` | `id`, `image_id`, `scope_digest`, `source_digest`, `effect_id`, `state` | `content`, `media_id`, `ready_until` |
| `outbound_sidebar_send_grants` | `intent_id`, `consumed_at`, `outcome`, `evidence_digest` | `token_digest`, `expires_at` |
| `outbox_events` | `id`, `aggregate_type`, `aggregate_id`, `event_type`, `event_version`, `idempotency_key`, `payload_json`, `occurred_at` | `claimed_by`, `claim_expires_at`, `attempt_count`, `last_error_code` |
| `payment_handoffs` | `id`, `payment_id`, `effect_id`, `payload_digest` | `payload`, `expires_at` |
| `webhook_inbox` | `id`, `provider`, `idempotency_key`, `payload_hash`, `status`, `received_at`, `processed_at` | `payload`, `attempt_count`, `next_attempt_at`, `lease_owner`, `lease_expires_at`, `last_error_code` |
| `wecom_customer_sync_runs` | `id`, `run_key`, `corp_scope`, `status`, `version` | `staff_index`, `provider_cursor`, `last_error_code` |
| `wecom_staff_directory_refresh_runs` | `id`, `run_key`, `trigger`, `state`, `directory_digest` | `attempt_count`, `last_error_code`, `discovered_count`, `created_count`, `existing_count`, `inactive_count` |
| `wecom_welcome_grants` | `id`, `callback_digest`, `value_digest`, `consumed_at`, `consumer_effect_ref` | `ciphertext`, `expires_at` |

候选不是授权删除清单。特别是 `message_archive_ingest_issues.protected_payload` 可能是未归档消息唯一原件；`wecom_staff_directory_refresh_runs.run_key/state` 是成功重放防重，不能把整行当同步日志；`webhook_inbox` 与 `idempotency_receipts` 不能因过程详情变旧丢掉原键结果；含 ciphertext/media_id 的字段还需要区分长期原件与短期 Provider 凭据。详细理由和 Owner 缺口逐项保存在 JSON。

## 其余需要明确的边界

- `payment_shop_materials` 是唯一仍未分类项：订单外键、provider_digest、snapshot 可表明业务来源，但当前仓未找到实际生产读写入口；snapshot 究竟是订单选材原件还是 Provider 读取副本尚未证实，保持禁删。
- `config_definition_import_quarantines` 与 `order_import_quarantine` 也未找到当前生产写入点，但 DDL 明确绑定导入 batch/receipt 并保存未导入来源与理由；这是防静默丢数的业务隔离证据。无运行引用不等于无价值。
- `config_definition_commerce_reviews` 与 `config_definition_commerce_revisions` 的 Owner 应由原 `config` 更正为 `configmigration`：0128/0132 声明与 `internal/configmigration/target` 实际写入一致。
- `ai_assistant_integration_nonces` 维持签名 nonce 原 expires_at；独立业务 operation_receipts 永久保留。`survey_result_tokens` 维持已有过期/撤销校验，NULL expires_at 的长期授权不能擅自按30天失效。
- Segment 历史受众、WeCom 历史参考时点观察、OperationCycle 执行快照、Survey 推送快照、Referral 关闭榜单是冻结业务结论/防重依据，不能用当前数据重新计算替代。相应最新投影、锁和水位已另作分类。

## 主分支集成与验证

只对当前仍为 `protected_unclassified` 的同名条目合入这些明确分类；Referral 13项按白名单新增。保留主分支已启用 Owner 入口、AdminOps、manual commands 和所有资源前缀登记。checker 需显式接受 `protected_mixed_payload` 并强制其 cleanup disabled；过程、TTL、projection、coordination 白名单增量见 JSON。两个过程资源只有实际 Owner 实现及测试通过后才能在运行 registry 启用。

本次验证：389表唯一且与审阅清单逐项相等；所有列举字段真实存在于 DDL；所有 Owner 引用片段与固定基线的对应文件行吻合；计数与策略逐项汇总一致；全部未授权删除；JSON 解析及 `git diff --check` 通过。本提交仅文档，没有新增运行代码，未声称完整回归、CI、部署或生产删除已执行。
