# Survey OneID Full Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 AI-CRM-v3 中交付完整可用的问卷前后端、OneID 归属、可靠后续动作和 `150.158.82.186` 一次性一致性快照历史数据迁移；不切流、不停写、不持续同步或退役旧系统。

**Architecture:** 保留 `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e` 已验证的前端交互和兼容 HTTP 形状，在 v3 新建 Survey-owned Go 领域及 PostgreSQL 表。所有身份解析经 `internal/identity/port`，Provider 写经 `internal/outbound` 和 `internal/externaleffects/port`，历史导入由独立 `cmd/migrate-survey-history` 完成且不进入运行时依赖。

**Tech Stack:** Go、PostgreSQL 16、pgx、River、OpenAPI、TypeScript、Node 24、现有 v3 web shell。

---

## 0. 计划约束

- 对应 PRD：`docs/06-PRD-问卷全能力与历史数据迁移.md`。
- 对应 ADR：`docs/adr/0002-survey-oneid-history-and-effects.md`。
- 每个 PR 只交付一个用户可观察能力；每个 PR 从最新 `main` 创建独立 worktree。
- 当前迁移最高号为 `0016`，计划从 `0017` 开始。若执行时 `main` 已前进，只重编号尚未合并的新迁移，绝不修改已部署 migration。
- 冻结前端资产目前与 v2 逐字节一致。除修复经 characterization test 证明的缺陷外，不重写视觉和交互。
- Provider 默认 disabled；历史效果只读导入，绝不重放。
- 生产操作必须在代码、CI、staging、迁移演练和业务 Owner 签字后另行执行。

## 1. PR 拆分与退出门

| PR | 用户可观察能力 | 退出门 |
|---|---|---|
| S0 | 冻结问卷供体与当前缺口 | manifest、Behavior Contract、36 路径台账通过 |
| S1 | 后台完整管理普通/测评问卷 | CRUD、复制、启停、版本冲突、删除保护真库通过 |
| S2 | 公开发布、H5、OneID 与提交 | 四题型、两展示模式、身份/匿名/冲突 Journey 通过 |
| S3 | 结果、分析、导出和客户/侧边栏读取 | 历史 snapshot、敏感权限、customer scope 通过 |
| S4 | 完成动作、Outbound、External Effects | 同 UoW accept、worker、unknown/reconcile 通过 |
| S5 | 历史导入器和 rehearsal | 一次性快照/replay/4327 unresolved question/53 identity baseline 通过 |
| S6 | 生产快照导入与验收 | 目标备份、幂等导入、对账、零 Provider 调用和只读验证通过 |

## 2. S0：冻结供体和行为合同

### Task 1：登记供体清单和 byte manifest

**Files:**

- Create: `docs/migration/survey/contract-audit.md`
- Create: `docs/migration/survey/donor-manifest.yaml`
- Create: `docs/migration/survey/donor-sha256.txt`
- Create: `scripts/check-survey-donor-manifest.sh`
- Modify: `.github/workflows/ci.yml`

**Steps:**

1. 在 `contract-audit.md` 按 BEHAVIOR/PORT/ADAPTER/DISCARD 分类 `internal/survey`、36 条 OpenAPI 路径、前端模板/TS/CSS、migration 和 importer。
2. 把当前 v3 已存在且与 donor 相同的问卷前端文件写入 manifest；基线必须固定为完整 SHA `6bfbe581...`。
3. 写失败测试：篡改任意 manifest 文件或 donor SHA 时脚本必须非零退出。
4. 实现校验脚本：clone donor 到 `mktemp -d`、checkout 固定 SHA、计算 SHA-256、检查不允许的 Go/runtime 文件未被复制。
5. 在 CI 中运行脚本，再运行 `npm run typecheck && npm test && npm run build`。
6. Commit：`docs(survey): freeze donor behavior and frontend assets`。

### Task 2：冻结 API 和 Journey characterization

**Files:**

- Create: `docs/migration/survey/behavior-contract.md`
- Create: `web/scripts/survey-editor-characterization.mjs`
- Create: `web/scripts/survey-public-characterization.mjs`
- Modify: `package.json`

**Steps:**

1. 写明列表、编辑、复制、启停、删除、发布、H5、提交、结果、分析、导出、运营、效果、侧边栏和 unresolved history 的输入输出。
2. 记录 v2 明确未完成项：assessment/F02、真实 Provider delivery 和 production migration。
3. 为现有 DOM、文案、按钮、请求方法和错误展示写 characterization test。
4. 运行：`npm test`；预期现有冻结资产全通过。
5. Commit：`test(survey): freeze donor journeys before backend rewrite`。

## 3. S1：后台完整问卷管理

### Task 3：建立 Survey domain、Port 和定义 schema

**Files:**

- Create: `internal/survey/domain/questionnaire.go`
- Create: `internal/survey/domain/assessment.go`
- Create: `internal/survey/port/service.go`
- Create: `internal/survey/port/identity.go`
- Create: `internal/survey/port/outbound.go`
- Create: `internal/survey/domain/questionnaire_test.go`
- Create: `internal/survey/domain/assessment_test.go`
- Create: `migrations/0017_survey_definitions.sql`

**Steps:**

1. 先写表驱动失败测试：四题型、selection/text/mobile 校验、other 选项、score rule、dimension/type/level/overall/recommendation。
2. 运行：`go test ./internal/survey/domain/...`；预期因类型和校验器不存在而失败。
3. 实现纯领域值对象；不 import HTTP、pgx、Identity app/store 或 External Effects。
4. 写 migration：`survey_questionnaires`、不可变定义版本、questions、options、score rules、operation receipts；定义状态和 version CHECK 完整。
5. 写 migration contract test，验证表 Owner、索引、删除保护和 down guard。
6. 运行：`go test ./internal/survey/domain/...`；预期 PASS。
7. Commit：`feat(survey): add versioned questionnaire domain`。

### Task 4：实现 PostgreSQL repository 和原子管理服务

**Files:**

- Create: `internal/survey/app/questionnaires.go`
- Create: `internal/survey/app/questionnaires_test.go`
- Create: `internal/survey/store/postgres.go`
- Create: `internal/survey/store/postgres_integration_test.go`
- Create: `internal/survey/module.go`

**Steps:**

1. 写失败测试：create/update/duplicate/enable/disable、actor-key 重放、payload drift、version CAS、Event append 失败全回滚。
2. 写删除保护测试：无历史草稿可删除；有 submission/effect 时只能 retire。
3. 实现 Store，所有写入先调用 `postgres.RequireTransaction`。
4. 在一个 UoW 中写 definition、children、receipt、audit 和 `survey.definition.changed` Outbox/Event。
5. 真实 PG16 运行：`go test ./internal/survey/store/... -run Integration -count=1`；预期 PASS。
6. 运行 race：`go test -race ./internal/survey/...`；预期 PASS。
7. Commit：`feat(survey): persist atomic questionnaire management`。

### Task 5：实现后台兼容 API 并挂载冻结前端

**Files:**

- Create: `internal/survey/http/admin.go`
- Create: `internal/survey/http/admin_test.go`
- Create: `internal/survey/ui.go`
- Modify: `api/openapi.yaml`
- Modify: `cmd/aicrm/composition.go`
- Modify: `cmd/aicrm/main.go`
- Modify: `internal/webshell/handler.go`
- Modify: `internal/webshell/handler_test.go`
- Modify: `web/src/api/capabilities.ts`

**Steps:**

1. 先把 donor 管理路径和 DTO 加到 OpenAPI，生成客户端，确认第二次生成无 diff。
2. 写 HTTP 失败测试：session、`questionnaires.read/write`、same-origin CSRF、Idempotency-Key、稳定错误码。
3. 实现 Handler，只调用 Survey app/port；兼容 `PUT/PATCH` 但归一为一个 command。
4. 将 `/admin/questionnaires`、new/detail/operations 的真实页面交给 Survey UI；移除“入口已预留”描述。
5. 只有 HTTP→service→PG Journey 通过后，把对应 capability 从 placeholder/backend_blocked 改为 real。
6. 运行：`node scripts/validate-openapi.mjs && npm run typecheck && npm test && go test ./internal/survey/... ./internal/webshell/... ./cmd/aicrm/...`。
7. Commit：`feat(survey): serve complete questionnaire admin workflow`。

## 4. S2：公开发布、H5、OneID 和提交

### Task 6：发布不可变公开定义

**Files:**

- Create: `internal/survey/app/publishing.go`
- Create: `internal/survey/app/publishing_test.go`
- Create: `internal/survey/http/public.go`
- Create: `internal/survey/http/public_test.go`
- Modify: `internal/survey/store/postgres.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 写失败测试：publish、republish、disable、slug 唯一、expected version、public snapshot 不含后台配置/Secret。
2. 实现 immutable definition version；公开读取永不拼装当前可编辑 rows。
3. 支持四题型和两种显示模式；旧版本已有提交时永久可读但不可继续提交。
4. 运行 focused + PG16 测试；预期 PASS。
5. Commit：`feat(survey): publish immutable public definitions`。

### Task 7：实现可信 H5 identity session

**Files:**

- Create: `internal/survey/app/identity_session.go`
- Create: `internal/survey/app/identity_session_test.go`
- Create: `internal/survey/http/h5_identity.go`
- Create: `internal/survey/http/h5_identity_test.go`
- Create: `internal/survey/provider/wechat_oauth.go`
- Create: `internal/survey/provider/wechat_oauth_test.go`
- Create: `cmd/aicrm/survey_adapters.go`
- Modify: `cmd/aicrm/composition.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 写测试证明 query/body 不能构造 verified fact，openid 无 App scope、UnionID 无平台 scope、external_userid 无 corp scope 均拒绝。
2. Provider adapter 完成 code exchange/签名验证后才调用 `identitydomain.NewVerifiedFact`。
3. adapter 先 `Resolve`，只有 verified not-found 才显式 `ProvisionVerifiedIdentity`；conflict 不 provision。
4. 签发短时、purpose-bound、一次性 survey session；token 不包含原始外部 ID。
5. 手机号只作为 declared evidence；未有 Customer 时不 Attach、不 Provision。
6. 运行：`go test -race ./internal/survey/... ./internal/identity/... ./cmd/aicrm/...`。
7. Commit：`feat(survey): bind H5 sessions to canonical OneID`。

### Task 8：实现原子提交、评分和结果 token

**Files:**

- Create: `migrations/0018_survey_submissions.sql`
- Create: `internal/survey/app/submissions.go`
- Create: `internal/survey/app/submissions_test.go`
- Create: `internal/survey/app/scoring.go`
- Create: `internal/survey/app/scoring_test.go`
- Create: `internal/survey/http/submissions.go`
- Create: `internal/survey/http/submissions_test.go`
- Modify: `internal/survey/store/postgres.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 写失败测试：四题型 normalize、other、测评分数/类型/等级/建议、匿名/linked/pending/conflict。
2. 写 idempotency 测试：同 key 同 payload 返回原 submission；同 key 异 payload 409；并发只能提交一次。
3. migration 建 immutable submission/answer/receipt 和 identity reconciliation case；answer 保存题目/选项/分值/标签 snapshot，不强制当前 definition FK。
4. 在同一 UoW 写 submission、answers、result、receipt、audit 和 `survey.submitted` Outbox。
5. result token 只持久化 HMAC；公开查询使用 POST body，响应不回显 token。
6. Event 或 receipt completion 失败时全部回滚。
7. 运行真实 PG16、HTTP 和 race 测试；预期 PASS。
8. Commit：`feat(survey): commit immutable scored submissions`。

## 5. S3：结果、导出和客户读取

### Task 9：管理端结果、分析和安全导出

**Files:**

- Create: `internal/survey/app/results.go`
- Create: `internal/survey/app/results_test.go`
- Create: `internal/survey/http/results.go`
- Create: `internal/survey/http/results_test.go`
- Modify: `internal/survey/store/postgres.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 写分页、准确 total、选择聚合、测评分布和历史 snapshot 测试。
2. 默认 analysis/export preview 明确断言无手机号、外部身份、自由文本和 raw token。
3. 敏感 CSV 要求独立 capability、`no-store`、审计和字段白名单；响应不能落服务器临时文件。
4. 用 100 万 submission/2,000 万 answer synthetic EXPLAIN 验证无 Seq Scan 热路径。
5. 运行 focused、PG16 和 OpenAPI test；预期 PASS。
6. Commit：`feat(survey): expose safe results and audited exports`。

### Task 10：客户详情和企微侧边栏读取

**Files:**

- Create: `internal/survey/port/customer_answers.go`
- Create: `internal/survey/app/customer_answers.go`
- Create: `internal/survey/app/customer_answers_test.go`
- Create: `internal/survey/http/sidebar.go`
- Create: `internal/survey/http/sidebar_test.go`
- Create: `cmd/aicrm/survey_customer_adapter.go`
- Modify: `cmd/aicrm/composition.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 写测试：只按 canonical `customer_id` 查询；declared phone/external hint 不能扩大范围。
2. Sidebar context 先由现有 OneID bridge 解析 customer，再调用 Survey read Port。
3. 返回安全选择题摘要、分数和时间；手机号/自由文本默认掩码或不返回。
4. 对无 customer、identity conflict、跨 customer 请求稳定拒绝。
5. 运行：`go test -race ./internal/survey/... ./cmd/aicrm/...`。
6. Commit：`feat(survey): show canonical customer questionnaire history`。

## 6. S4：完成动作与可靠外部效果

### Task 11：建立 Survey operations 和 transactional Outbound Port

**Files:**

- Create: `migrations/0019_survey_operations.sql`
- Create: `internal/survey/app/operations.go`
- Create: `internal/survey/app/operations_test.go`
- Create: `internal/survey/http/operations.go`
- Create: `internal/survey/http/operations_test.go`
- Create: `internal/outbound/port/survey.go`
- Create: `internal/outbound/survey_intent.go`
- Create: `internal/outbound/survey_intent_test.go`
- Modify: `internal/externaleffects/port/port.go`
- Modify: `cmd/aicrm/composition.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 写 operations CRUD、actor-key replay/payload conflict、opaque config reference 校验。
2. 扩展 External Effects kind，但 owner 保持 `outbound`；不要增加 `OwnerSurvey`。
3. Outbound Port 必须提供 caller-transaction variant；保存受限 outbound intent、accept effect、River row 与 Survey submission 同事务。
4. EER 继续只存四 digest，不存 Customer、答案、URL、Secret、请求或响应。
5. 写事务断裂测试：Outbound adapter 若开新事务，测试必须失败。
6. 运行 PG16 integration 和架构检查。
7. Commit：`feat(survey): accept post-submit intents atomically`。

### Task 12：执行、完成回执和人工 reconcile

**Files:**

- Create: `internal/outbound/survey_provider.go`
- Create: `internal/outbound/survey_provider_test.go`
- Create: `internal/survey/app/effect_results.go`
- Create: `internal/survey/app/effect_results_test.go`
- Create: `internal/survey/http/effects.go`
- Create: `internal/survey/http/effects_test.go`
- Modify: `internal/externaleffects/worker.go`
- Modify: `cmd/aicrm/composition.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 测试 Provider disabled 时零网络调用并返回可辨识状态。
2. 使用本地 test receiver 测 accepted→queued→attempted→executed，验证 stable key 重放不重复调用。
3. 测 timeout after dispatch→`outcome_unknown`；任何自动换 key 或重试都必须失败。
4. 完成 artifact 通过 composition sink 写入 Survey-owned result projection；不跨表读 EER。
5. reconcile 要求 actor、CSRF、Idempotency-Key、generation/fence 和 evidence digest。
6. 企微标签效果复用 Outbound 现有 provider；Survey 不调用 WeCom client。
7. Commit：`feat(survey): execute and reconcile reliable effects`。

## 7. S5：全量历史导入

### Task 13：定义脱敏快照格式和 source exporter

**Files:**

- Create: `docs/runbooks/问卷历史数据迁移生产执行.md`
- Create: `cmd/migrate-survey-history/snapshot.go`
- Create: `cmd/migrate-survey-history/snapshot_test.go`
- Create: `scripts/migration/export-survey-source.sh`

**Steps:**

1. 固定九类 source table allowlist、column allowlist、schema fingerprint、事务 snapshot 和 high-water mark。
2. exporter 只接受受限只读 URL；禁止把连接串、PII 行或 URL token 输出到 stdout。
3. 输出逐表 NDJSON/CSV、manifest、count 和 SHA-256；目录权限 0700，传输加密，导入验收后按 runbook 销毁临时快照。
4. 写测试：缺列、多列、类型漂移、digest 漂移、source 更新中途发生时失败关闭。
5. 文档不得放真实 credential、host key、快照路径或原始样本。
6. Commit：`feat(migration): freeze secure survey snapshot format`。

### Task 14：实现可重放 Survey importer

**Files:**

- Create: `migrations/0020_survey_history_import.sql`
- Create: `internal/survey/port/import.go`
- Create: `internal/survey/app/import.go`
- Create: `internal/survey/app/import_test.go`
- Create: `internal/survey/store/import_postgres.go`
- Create: `internal/survey/store/import_postgres_integration_test.go`
- Create: `cmd/migrate-survey-history/main.go`
- Create: `cmd/migrate-survey-history/main_test.go`
- Modify: `.github/workflows/ci.yml`

**Steps:**

1. migration 建 runs、receipts、source maps、unresolved cases 和 legacy effect safe projection；down 对有数据表失败关闭。
2. 写 `inspect`：只校验文件、schema、关系、PII policy、count 和 digest。
3. 写 `dry-run`：在 target 回滚事务中生成 imported/unresolved 计划。
4. 写 `apply --confirm-apply --run-id`：source ID 永不作为 target PK；每行 source/payload/target digest 和 disposition 原子入账。
5. 写 `reconcile`：比较 count、canonical digest、父子关系、时间范围、identity coverage 和 effect history。
6. 对 4,327 类旧 question id 不要求当前 definition FK；答案照常导入并标 resolution status。
7. 对 53 类 identity baseline 不写 Customer；只建 unresolved case。正式数字以新快照为准。
8. 旧 push/SCRM 日志只导入业务关联、状态、次数、时间、安全失败分类和 digest；URL、请求体、响应体和原始用户标识不导入也不归档，且绝不 enqueue。
9. 运行：`go test -race ./cmd/migrate-survey-history ./internal/survey/...` 和独立 PG16 全量 fixture。
10. Commit：`feat(migration): import all survey history with receipts`。

### Task 15：staging rehearsal 与破坏性负例

**Files:**

- Create: `journeys/survey-full-closure.sh`
- Create: `journeys/survey-migration-rehearsal.sh`
- Modify: `journeys/README.md`
- Modify: `docs/runbooks/问卷历史数据迁移生产执行.md`

**Steps:**

1. 用脱敏结构等价快照演练 inspect→dry-run→apply→replay→reconcile。
2. 覆盖 10/57/189/1,585/6,649/715/1,211 当前基线规模，正式数量以 snapshot manifest 为准。
3. 注入 source schema drift、缺父行、重复 token、identity conflict、Event 冲突、EER unknown、worker crash。
4. 验证失败时 source 未改、target 无半条 aggregate、Provider 零调用。
5. 人工浏览器验证所有冻结页面和 H5 真机；截图不得含真实 PII。
6. Commit：`test(survey): rehearse snapshot migration failures`。

## 8. S6：生产快照导入

### Task 16：生产只读快照、导入与对账

**Files:**

- Update: `docs/runbooks/问卷历史数据迁移生产执行.md`（只登记 count/digest/run ID，不登记 PII）

**Steps:**

1. 确认 source DB 只读凭据、已验证 host key、目标备份和业务 Owner 授权。
2. 保持 source 原路由和写入不变，v3 Provider disabled。
3. 在单个 repeatable-read 事务中生成一致性快照，记录 `snapshot_at`，并在目标运行 `inspect`、`dry-run`、`apply`、`reconcile`。
4. 核对最终快照的新 counts；当前 53 unresolved 和 4,327 definition-unresolved 只能作为预期基线，不可硬编码。
5. 只读比较列表、10 份问卷、提交分页、抽样答案、分析和客户侧边栏。
6. 任一未解释差异即停止并回滚本次 target batch；不对 source 采取任何动作。

### Task 17：生产验收和关闭项目

**Files:**

- Create: `docs/migration/survey/closure-review.md`
- Update: `docs/06-PRD-问卷全能力与历史数据迁移.md`
- Update: `web/src/api/capabilities.ts`

**Steps:**

1. 汇总真实 release SHA、CI、migration run、count/digest、unresolved、browser、worker 和 provider 证据。
2. 明确区分 local commit、effect accepted、Provider executed 和业务 delivery/reconciled。
3. 复查没有新 identity matcher、queue、worker、retry/reconcile kernel 或跨领域 Store import。
4. 只有完整 Journey 通过后把剩余 questionnaire capability 标为 real。
5. 记录 OneID、Persistence、External Effects 和 no-duplication 结论。
6. 明确 `snapshot_at` 之后 source 新增数据不在本期范围，不将 v3 描述为已切流。
7. Commit：`docs(survey): close snapshot import with production evidence`。

## 9. 每个 PR 的统一验证命令

```bash
node scripts/validate-openapi.mjs
npm run typecheck
npm test
npm run build
gofmt -w $(find cmd internal -name '*.go' -type f)
GOWORK=off go vet ./...
GOWORK=off go test ./...
GOWORK=off go test -race ./internal/survey/... ./internal/identity/... ./internal/externaleffects/... ./internal/outbound/... ./cmd/aicrm/...
python3 scripts/check-architecture.py
make check
```

预期：全部 PASS；OpenAPI 第二次生成无 diff；`git status --short` 只包含当前 PR 计划内文件。

## 10. 执行交接

计划完成后，在最新 `main` 的独立分支从 S0 开始，严格按 S0→S6 顺序执行。不得并行实现会同时修改 `api/openapi.yaml`、`cmd/aicrm/composition.go`、External Effects 枚举或 migration 序号的 PR；一次性生产快照导入必须最后执行，且不包含切流。
