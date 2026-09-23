# 问卷提交人群规则（仅新系统重算）

分类：OneID 只读取已归属 canonical CustomerID，不解析外部字符串、不建客/合并；持久化复用 Segment 现有定义和刷新任务。Survey、WeCom 的事实读取在评价器同一 UoW 内，经 Owner Port；无 Provider 调用、发送或旧 SQL 执行。

## 原规则与映射核对

读取目标受保护 `audience-rule-audit-complete.json` 的当前发布版本：

- 源包 14、版本 71：`questionnaire_id IN (21,37)` 且 external_userid 非空。原 SQL 无付款、联系人 active、选择题答案或首次限制；水位只控制原增量事件。原 owner 缺失时回填固定员工，不迁移此猜测值。
- 源包 38、版本 82：`questionnaire_id = 55` 且 external_userid 非空；同样按提交事实。
- 隔离库 `aicrm_cutover_rehearsal_20260911` 只读核对 `survey_migration_source_map`：21→5、37→7、55→11，三项均 published。已归属/全部提交分别 142/238、90/142、1/1。原始未归属提交不会被冒充客户。

## 新系统合同

新闭合模板 `questionnaire_submissions`：任一所选问卷已提交且提交记录 resolved，按 canonical CustomerID 去重。无答案类型条件，非首次提交也可入选，时间晚于评价时点不入选。

`require_wecom_identity=true` 时，额外要求当前配置企业 scope 内存在归属该客户的企微 profile，状态 active 或 stale 均可；conflict 不可。不要求跟进关系、有效联系人状态或特定负责人。无可信 profile 的客户不猜测。此为新系统事实重算，不复制旧 external_userid 或旧名单。

源 14 建议配置（映射必须在正式目标再核对，不能把隔离 ID 当通用 ID）：

```json
{"schema_version":1,"template_key":"questionnaire_submissions","parameters":{"questionnaire_ids":["5","7"],"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":true}}
```

源 38 同配置，questionnaire_ids 为 `["11"]`。HTTP 对所有问卷引用通过 Survey Owner 解析；任何不存在引用整次拒绝，不能忽略一项。

启用只代表现有 Segment 刷新计算，不恢复旧 SQL、发送、固定员工回填或源自动化。正式生产写入由切流负责人统一操作。

## 验证

- Survey PostgreSQL：无选择题回答仍是提交；非首次保留，未归属/未来排除；原首次选择题逻辑保持通过。
- WeCom PostgreSQL：删除跟进关系后仍能识别 active/stale profile；其他企业 scope 不匹配；conflict 不入选。
- Segment：问卷 OR、客户去重、scope Reader 缺失失败关闭、无企微 profile 排除、可关闭企微条件。
- HTTP：全部问卷引用解析及去重；一项未知拒绝；Owner 不可用拒绝。
