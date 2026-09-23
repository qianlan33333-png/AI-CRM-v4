# 治理成效：可审计事件周期与明确覆盖的指标

## 业务判断与参考

OneID：不涉及，没有客户、渠道身份或归属。持久化：AdminOps 自有 PostgreSQL 事务；正常巡查周期变更与 issue/通知接受同一 UoW，人工归因与幂等收据、审计同一 UoW。没有新队列、cron、Provider 读写或修复业务的权限。

用户要确认治理是否持续减少故障和排查成本。重复告警次数不等于故障次数，采集 unknown/uncovered 不等于线上缺陷，确认部署进程存在也不等于正式成功发布。采用 [DORA 的发布与故障关联定义](https://dora.dev/guides/dora-metrics/) 和 [Google SRE 复盘实践](https://sre.google/workbook/postmortem-culture/)；参考 [fourkeys 数据模型](https://github.com/dora-team/fourkeys/blob/main/METRICS.md)，不安装已归档的平台，不复制其部署分母假设。

## 事实与边界

1. 每个 issue fingerprint 每次异常→新鲜 ok 为一个 episode。相同异常持续仅推进 last_observed_at；恢复后再次发生新建 episode，健康间隔不进入恢复耗时。开始时已存在的问题只标 first_observed_existing_issue，不能回填为历史起点。最初观察状态及检测版本永久保留，最新观察类别单列。
2. 仅巡查成功观察关闭 episode；人工归因不能关闭或修改检测事实。归因采用固定枚举，不接受自由文本、原始日志、URL、正文或个人信息。首次归因默认 unclassified。
3. 超级管理员+CSRF，通过版本 CAS、Idempotency-Key、不可变审计受权确认 classification、escaped_defect、change_failure、root_cause、remediation、fault_started_at、fault_start_basis、effort_minutes。变更故障 yes 必须关联已验证成功发布的 sequence；起点不得晚于首次检测，发布不得晚于起点/首次检测。归因不替代外部业务修复。
4. 指标按首次检测时间半开区间 [from,to) 的 episode cohort，最大366天。MTTD 是 confirmed_defect 中有真实人工确认起点的 detected_at-fault_started_at；缺失起点是 missing，不填零。检测→确认恢复时长独立命名，开放周期不进入均值；另列 fault→recovery，避免歧义称为全量 MTTR。每项提供 sample_count/missing_count，零样本均值 null。
5. 逃逸率只用明确 yes/no 的 confirmed_defect 样本；未归因缺陷和非缺陷分开。重复故障是同 fingerprint 之前存在已恢复 confirmed_defect 的新 confirmed_defect；只覆盖本机制观察到的历史。人工排查投入是显式记录分钟的样本，未填写不是零。
6. 正式发布桥接只读取 root 成功收据，输出 sequence/release_sha/succeeded_at/receipt_digest/state，不开放原收据权限。AdminOps 永久保存最小事实；同序号冲突拒绝，root 撤销只可单向标 revoked。observed release projection 永不进入分母。当前 root 收据不包含所有失败部署，并可能缺少历史序号；仅输出 verified-success-cohort 的 confirmed-change-failure ratio，不声称全量 DORA CFR。所有未归因及证据覆盖缺口显式显示。
7. 永久数据仅是事件周期/结构化归因审计/正式发布最小事实，无排障详情。因此720小时清理不会删除这些治理结论，也不能借它们恢复已过期排障详情。

## HTTP 与接线合同

独立 leaf `NewGovernanceOutcomesHandler(service, InspectionSecurity)`，root 负责挂载、API规范及UI。所有 GET/PUT 均超级管理员，PUT 再校验 CSRF。

- GET `/api/admin/ops-governance/outcomes?from=<RFC3339>&to=<RFC3339>`：聚合，包含 cohort、denominators、unknown；未传默认最近30天。
- GET `/api/admin/ops-governance/episodes?from=...&to=...&before_id=<id>&limit=50`：倒序分页，上限100，has_more。
- GET `/api/admin/ops-governance/episodes/{id}`：最小永久事实。
- PUT `/api/admin/ops-governance/episodes/{id}/attribution`：JSON为 port.IncidentAttributionCommand，必须 expected_version；需 Idempotency-Key。返回 episode_id/version/action_id/replay，不返回原始key/actor信息。重放固定原收据，key内容冲突409，CAS冲突409。非法字段400、无权限403、缺记录404、证据不可用503。

`NewGovernanceOutcomesService(pool, uow, reader, GovernanceOutcomesOptions{Now})`；reader 是 platform.GovernanceReleaseReader，可为空（部署覆盖 unavailable）。`hostmaintenance.NewGovernanceReleaseReader()` 读取固定 `/var/lib/aicrm-maintenance/governance-releases.json`。Metrics 读取并验证摘要后在短事务投影永久最小事实；没有新的调度。root 在成功收据最终持久化后执行固定 root bridge，在已有安装锁内序列化；bridge 本身也验证/独占该锁，支持传入已验证的继承锁FD。

## 验收

真实PG：多次复发独立关闭；unknown不算缺陷；首次机制观察不伪造历史；事件+问题同回滚；CAS竞争只有一个成功；同key并发/重放/改载荷；非法时间、无正式发布引用拒绝；多个故障同发布分子去重；零样本为null；序号缺口和撤销不假正常；>720小时永久事实仍在。HTTP：匿名/普通管理员/CSRF拒绝、严格小JSON、无自由文本。平台：固定文件信任链、符号链接/权限/伪造与未来摘要拒绝。只宣称实际专项证据，root 另行完整CI及真实部署验收。

## 指标显示与部署补充

- `effort_minutes_total/known_count/missing_count` 覆盖整个事件 cohort（包括排查后确认非缺陷的投入）；人工声明的依据枚举不是系统已校验原始 Provider 凭据。
- 恢复均值均为当前事实下、首次检测落在窗口内的 confirmed_defect cohort；不把开放样本的当前年龄混入均值。复发比例仅表示本机制历史内已确认的相同 fingerprint 复发。
- 发布摘要 `generated_at` 是分母证据截止时间；长期不发布不要求伪造新成功收据。当前版本不匹配、正在发布未完成、来源冲突或无法读取时 `evidence_state=unavailable`、ratio=null。已导入的历史最小事实可以展示，但不是完整分母。
- 固定桥接一次最多2048个收据文件、摘要不超过1MiB；超过上限明确失败，不截断或输出全量成功。继续增加发布频次前应扩展有界分页协议；应用表中的历史事实不会因此被删除。撤销序号只作为收据撤销，不擅自认定为失败部署。
- root 集成：0193 安装/就绪校验、实际 Host 旅程、API 和共享治理页接入；源码权威索引绑定该 API。在成功收据完成后调用桥接，`--lock-fd 9` 继承原锁。Python bridge 测试由现有 preflight 自动发现，Go 专项正常自动发现；治理成效 DOM 接入 frontend lane。接线不等于验证或部署完成，证据分别留存。
