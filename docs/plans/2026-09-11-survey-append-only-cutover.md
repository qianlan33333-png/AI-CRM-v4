# 问卷历史追加迁移

OneID：读取既有 canonical customer；显式确认 Open Platform scope 后，通过 Identity Port 校验历史已解析归属，不创建、不合并、不改写身份。
Persistence：离线本地 PostgreSQL 串行化事务；导入批次、映射和新增历史事实原子提交。无内部任务和 Provider 调用，不重放推送。

## 行为边界

- 原有无参数导入及对账保持严格行为。
- `import --append-only` 只接受包含完整旧历史的新版加密快照；任何旧映射遗漏、摘要改变，或定义/问题/选项/计分规则增加均在事务提交前失败。
- 新增提交、答案、历史外部操作收据依旧使用原有导入逻辑；旧映射、客户归属和加密答案保持原值。外部操作仅只读历史收据，永不执行。
- `reconcile --append-only` 对整个 source_system 的映射对账，每条隔离记录使用其原批次；不能用新批次掩盖旧记录。
- 已解析历史提交默认仍拒绝。只有显式传入真实核实的 `--confirmed-unionid-scope wechat-open-platform:…`，且原始证据摘要未变、Identity Port 当前唯一归属与现有 customer_id 相同、状态原因符合历史 scope 确认流程时才通过。错误 scope、错客户、摘要漂移均拒绝。
- 不支持源记录更新/删除、不支持目标问卷定义修改/启用后的豁免；发现这些差异必须停下另行处理，不可用新 source_system 绕过。

## 执行顺序

1. 备份目标库和当前 survey data key，旧系统短暂停止问卷写入；提取完整加密快照并执行原 `validate`。
2. 在目标备份的隔离恢复库上运行以下命令；`TARGET_URL` 等仅从保护文件读取，避免密码进入命令记录。

```sh
migrate-survey-v2 import --snapshot SNAPSHOT.enc --snapshot-key-file SNAPSHOT.key --target-url TARGET_URL --data-key-file EXISTING_SURVEY_DATA.key --confirm-import --append-only
migrate-survey-v2 reconcile --snapshot SNAPSHOT.enc --snapshot-key-file SNAPSHOT.key --target-url TARGET_URL --data-key-file EXISTING_SURVEY_DATA.key --append-only --confirmed-unionid-scope VERIFIED_SCOPE
```

3. 对账零漂移，核对新增行数、原 resolved 客户归属、旧答案/结果令牌、无新增 Provider 效果。任一失败禁止生产导入。
4. 生产在同样暂停窗口下使用完全相同快照和当前数据密钥导入、对账，再经业务读路径检查。不要旋转数据密钥或刷新源快照后沿用旧对账结果。
5. 回滚只针对新增批次（已有 rollback 命令和防护）；回滚旧基线批次会受追加提交依赖保护而失败。切流后新提交存在时不得粗暴恢复全库备份。

## 验证

`TestPostgreSQLAppendOnlySurveySnapshot` 使用真实隔离 PostgreSQL schema 覆盖跨批次追加和重放、原批次隔离记录、源修改/遗漏、新定义拒绝、事务回滚、已解析归属保留、错误 scope 和错客户拒绝。

## 已审计启用的导入问卷

若管理员仅启用原 version 1 的导入问卷，对账可显式使用 `--allow-audited-enabled-definition`。限定问卷壳 version=2、status=published，存在相同问卷 ID、管理员 updated_by、updated_at、expected_version=1、status=published 的 `definition_enable` 审计。名称、slug、active definition version、定义摘要及所有冻结字段仍逐项严格校验；无审计或编辑了定义仍失败。此标志不修改问卷，不启用问卷，默认行为不变。
