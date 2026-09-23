# PRD：客户激活与客户列表

## 1. 文档信息

- 状态：已批准开发
- 范围：仅客户激活、客户列表、本板块详情、一次性手机号绑定
- 行为供体：`AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`
- 产品 Owner：CRM
- 数据 Owner：Identity / WeCom / Customer（见数据表归属）

## 2. 背景与目标

开发前分类：

```text
OneID: provisions customer（verified external_userid）+ reads canonical customer + attaches declared phone；仅通过 Identity Port。
Persistence: internal durable job + Provider read + local transaction；不涉及 Provider write/external effect。
```

企微同步轮次是 WeCom 拥有的业务进度；任务投递、lease 和失败重试必须复用 `internal/platform/jobqueue`，不在 WeCom 再造一套队列。全程仅读 Provider，所以 External Effects 不涉及；不引入 `outbound` 写通道。

v3 必须用自身 PostgreSQL 和 OneID 交付可对账的企微客户激活与目录。旧仓库只用于冻结行为和验收样例，不能成为运行依赖、数据回退源或双主写。

本期的业务目标：

1. 空库能从企微全量目录显式创建 OneID，重放时不重复建客。
2. 后台客户列表、筛选、游标翻页和详情完全由 v3 数据驱动。
3. 将授权的一次性生产快照按 `(corp_id, external_userid)` 精确附着 declared 手机号，每行有唯一、可重放、可对账的结果。
4. 手机号默认脱敏；Admin/SuperAdmin 在详情页直接查询完整手机号时写入不可变审计，页面无需填写理由。

## 3. 范围

### 3.1 本期交付

- 企微客户首次全量、每日全量对账、已有回调增量、后台手动重拉。
- 同企业 verified `wecom_external_userid` 解析或显式 `ProvisionCustomerFromVerifiedIdentity`。
- 同步轮次、成员/cursor 进度、逐项收据、企微资料快照、Outbox 与目录投影。
- 客户列表、本板块详情、同步状态、手机号精确搜索和受控揭示。
- `cmd/migrate-phone-identities` 的 `inspect -> dry-run -> apply -> reconcile -> rollback`。

### 3.2 明确不做

- 负责人关系、标签、阶段、问卷、聊天、时间线、会员权益或 Customer 360。
- 通过手机号建客、自动合并 Customer 根、将源表手机号伪装成 verified。
- v3 长期连接 `150.158.82.186`。
- 企微备注、标签或其他 Provider 写操作。

## 4. 角色与权限

| 能力 | SuperAdmin | Admin | Viewer |
|---|---:|---:|---:|
| 查看列表/详情/同步状态 | 允许 | 允许 | 允许 |
| 手动创建全量轮次 | 允许 | 禁止 | 禁止 |
| 查询完整手机号 | 允许 | 允许 | 永久禁止 |
| 运行一次性导入 CLI | 受控运维 | 受控运维 | 禁止 |

所有不安全 HTTP 方法均要求同源校验和 CSRF。完整手机号查询使用固定审计 purpose，响应必须为 `Cache-Control: no-store`。

## 5. 用户旅程

### 5.1 企微客户激活

1. SuperAdmin 点击“重新拉取企微客户”并提交幂等键。
2. API 在无活动轮次时创建 `queued`；幂等重放返回原轮次，另一活动轮次则返回 409。
3. 平台唯一 River runtime 从 customer-directory 队列领取 typed job；每页提交 cursor 后继续推进，崩溃时由 River 重投并从已提交阶段/cursor 恢复。
4. Provider 网络请求在事务外完成；每页的 OneID、资料快照、收据、审计、Outbox 和 cursor 在同一事务提交。
5. Worker 崩溃后从已提交 cursor 续传，不回首页。
6. 全部成功且投影数对账后才标记未出现资料 `stale` 并令轮次 `succeeded`。

### 5.2 查询客户

1. 用户进入 `/admin/customers`，先看最近同步摘要。
2. 可按名称/OneID、11 位中国大陆手机号和 Customer 状态筛选；页面不展示国家码、assurance 或内部激活状态。
3. 列表按固定 watermark 和 `(updated_at DESC, customer_id DESC)` 翻页，新写入不造成重复/跳页。
4. 详情只显示本期范围字段，不返回 raw external_userid、UnionID 或完整手机号。

### 5.3 手机号快照导入

1. 在源环境使用受限只读账号检查 schema、数量、唯一性和空值。
2. 产生最小 JSONL/CSV 快照：`schema_version, source_row_id, corp_id, external_userid, phone, source_updated_at`，同时生成 SHA-256 manifest。
3. `inspect` 只验证格式；`dry-run` 在不写数据的前提下分类所有行。
4. `apply` 仅对已存在的同 corp verified external identity 附着 encrypted declared `phone:cn11` 手机号。
5. `reconcile` 校验输入数与所有结果桶之和；不等时失败。
6. 对账与受控抽验通过后，安全删除明文快照和临时访问凭据，仅留摘要、run ID、计数和非 PII 错误码。

## 6. OneID 与数据规则

- `customers.id` 是唯一渠道中立业务主键。
- 企微 Adapter 验证数据来自已鉴权 Provider 后才构造 verified fact。
- external_userid scope 固定为 `wecom-corp:<corp_id>`。UnionID 只有在显式配置开放平台 scope 时才可提交 OneID；未配置时丢弃该字段。
- 导入手机号使用 `kind=phone, scope=phone:cn11, assurance=declared, source=phone_import`，Identity 保存 HMAC 检索摘要和独立 AES-GCM 密文。
- 只接受严格 11 位大陆手机号；`+86`、分隔符、全角数字和非大陆号码均拒绝。
- 已属于其他 Customer、输入内一号多客、非法号码、跨 corp 和未解析 external identity 都只写冲突/失败收据，不覆盖、不建客、不合并。

## 7. 同步状态机与失败策略

`queued -> listing_staff -> fetching_profiles -> ingesting -> reconciling -> succeeded`

- 限流、5xx、网络超时和临时数据库错误进入 `failed_retryable`，保留已提交 cursor。
- 配置缺失、权限不足、稳定 Provider 契约违反进入 `failed_terminal`。
- 部分失败不得将未出现客户批量 stale/closed。
- Provider disabled 时必须零网络请求，创建轮次前 readiness 明确返回 `provider_disabled`。
- 每日 02:30 Asia/Shanghai 由外部 timer 启动 oneshot 仅创建幂等同步轮次；实际任务由平台 River runtime 执行，不引入进程内 ticker。

## 8. API 契约

- `GET /api/admin/customers`：`keyword, phone, status, cursor, limit`；手机号输入为不带 `+86` 的 11 位中国大陆手机号，默认 50，最大 200，精确计数上限 10,000。
- `GET /api/admin/customers/{customer_id}`：返回基础客户、企微资料、脱敏手机号、OneID 摘要和同步源。
- `POST /api/admin/customers/{customer_id}/phone-reveal`：Admin/SuperAdmin + CSRF，无请求体；响应为不带 `+86` 的页面展示号码、`no-store`，并以固定 purpose 写审计。
- `POST /api/admin/customer-sync-runs`：SuperAdmin + CSRF + `Idempotency-Key`。
- `GET /api/admin/customer-sync-runs`、`GET /api/admin/customer-sync-runs/{run_id}`：仅返回阶段、进度、聚合计数和非 PII 错误码。

OpenAPI 文件是 HTTP 契约唯一可机检基线，任何 Handler 变更须先更新 `api/openapi.yaml`。

## 9. 验收标准

### 9.1 自动验收

- Provider disabled 零调用，成员枚举、多页、限流、超时、非法响应有契约测试。
- 同 external_userid 并发只有一个 Customer；无开放平台 scope 不绑 UnionID。
- declared phone 不建客、不合并、不升 verified。
- 事务失败时身份、资料、收据、审计、Outbox 和 cursor 全部回滚。
- 游标篡改、筛选漂移、CSRF、RBAC、`no-store` 与揭示审计全部覆盖。
- `make check`、指定包 race test 和 `govulncheck ./...` 通过。

### 9.2 生产对账恒等式

```text
发现唯一客户数 = activated + already_linked + conflict + terminal_failed
手机快照输入行数 = attached + already_linked + conflict + unresolved + invalid + duplicate_input
```

并需验证：无孤儿 Customer、无重复 active identity、无未消费同步 Outbox/收据、无 raw PII 结构化日志；列表数与成功激活 Customer 数一致。

## 10. 上线、回滚与阻断门

1. 先部署 migration/API/UI，Provider 保持 disabled。
2. 在预发使用可重放 fixture 完成崩溃恢复、权限和导入回滚演练。
3. 验证企微客户联系 Secret 权限后启用首轮全量，完成收据与投影对账。
4. 获得 `150.158.82.186` 受限只读通道后才可导出快照；不得将密码、私钥或快照提交到仓库。
5. 生产只允许 `inspect -> dry-run -> apply -> reconcile`，对账失败立即停止。
6. 应用回滚时停止外部 timer、关闭同步开关并回退版本；数据库保持前向，不删除 OneID、审计和导入证据。

红线阻断：双主写、身份错误归属、Provider 效果可能重复、鉴权绕过、Secret/PII 泄漏、跨领域写表、不可逆数据破坏或迁移静默丢数。
