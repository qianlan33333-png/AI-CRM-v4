# 问卷生产快照导入 Runbook

## 边界分类

- OneID：涉及。历史 UnionID 缺少开放平台 scope，正式导入全部保持 `unresolved`；迁移器不创建 Customer、不猜测归属。新 H5 OAuth 只有 Provider 验证后才能调用 Identity Port 的 Resolve/显式 Provision。
- 持久化：涉及。定义、提交、答案、结果、审计、Outbox 和操作回执使用 PostgreSQL Unit of Work；Provider 网络调用不持有事务。
- 外部效果：涉及但生产关闭。历史结果只作为 `read_only_legacy=true,replayable=false` 的收据导入，迁移不会创建 Job、Outbound intent 或 Provider 请求。

## 正式快照

- 来源：`AI-CRM-v2` 生产 PostgreSQL，主机 `150.158.82.186`，数据库 `openclaw_wecom`。
- 一致性时刻：`2026-09-03T08:21:39.108924Z`。
- 明文 manifest/snapshot 内容摘要：`3d31252983f15db9e98014ee47d9c5389626e2f1aced97c0b89a7da68640aa96`；加密文件 SHA-256：`8bbeda2bb21fa3c8f64a5b38e52a73d0d213ca916da1cf5af31ddd5dda8620ac`。
- 快照使用 AES-256-GCM，快照与密钥分离、文件权限 `0600`；密钥不得进入 Git、日志或报告。

| 实体 | 正式快照数 |
|---|---:|
| questionnaires | 10 |
| questionnaire_questions | 57 |
| questionnaire_options | 189 |
| questionnaire_score_rules | 0 |
| questionnaire_submissions | 1,585 |
| questionnaire_submission_answers | 6,649 |
| questionnaire_external_push_logs | 715 |
| questionnaire_scrm_apply_logs | 1,211 |

## 执行顺序

1. 确认 v3 Release 已包含 migration `0018_survey.sql` 和 `bin/migrate-survey-v2`，`/readyz` 为 ready。
2. 备份目标 PostgreSQL，并记录恢复点；确认生产 `AICRM_OUTBOUND_PROVIDER_ENABLED=false` 与 `AICRM_SURVEY_OAUTH_ENABLED=false`（OAuth 后续另行配置）。
3. 将加密快照与快照密钥通过受控主机通道分别放入临时 `0600` 文件；不解密落盘。
4. 从 `/etc/aicrm/aicrm.env` 将 `AICRM_SURVEY_DATA_KEY` 安全写入另一个临时 `0600` 文件（不得回显），再执行 `validate`、`import --data-key-file ... --confirm-import`、`reconcile`。
5. 只有 reconcile 输出 `duplicates=0 silent_loss=0 wrong_oneid_bindings=0 provider_effects_created=0` 才能验收。
6. 确认导入定义全部为 `disabled`；来源启用状态仅保存在 `survey_history_imported` 审计元数据中，不因导入开放任何新 H5 入口。
7. 删除目标主机临时快照和快照密钥；保留不含 Secret/PII 的对账报告。

## 门禁与回滚

下列任一条件触发整批目标回滚：源/目标计数不等、source map 重复、记录摘要漂移、错误 OneID 绑定、历史操作可重放、明文 PII/旧 URL/请求响应体进入目标、产生 Provider 效果。

`rollback --confirm-rollback` 只删除该快照批次映射到的目标记录，不操作旧库。如果导入问卷已经产生 v3 新提交，回滚会失败关闭，必须先人工处置新数据而不能强删。

## 已完成演练

- 加密快照校验、全量导入、reconcile、重复导入均通过；重复导入后数量不增加。
- 目标批次回滚后问卷、提交、答案、批次均为 0，再次全量导入及 reconcile 通过。
- 最终演练：10 问卷、1,585 提交、6,649 答案、1,926 历史操作回执、10,416 source maps、1,444 缺失结果令牌隔离记录。
- 身份：25 anonymous、1,560 unresolved、0 错误 OneID 绑定。
- 历史缺失定义答案：4,327，全部保留并标记 `legacy_definition_missing`。
- Provider 效果：0。
