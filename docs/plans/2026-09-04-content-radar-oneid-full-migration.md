# Content Radar OneID Full Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 AI-CRM-v3 中完整交付内容雷达管理端、公开查看、严格 UnionID/OneID 归因、真实统计、CSV 与历史承接，同时保持 donor 前端业务文件冻结。

**Architecture:** 新建 v3-owned `internal/radar` 模块和 PostgreSQL 表；公开授权由微信 Provider read Adapter 产生 scoped verified UnionID fact，经 Identity Port Resolve/显式 Provision；冻结 donor UI 由独立 v3 host Adapter 接入。Radar 不保存原始外部身份、不跨领域查表、不产生 Provider write。

**实施状态（2026-09-04）：** R1-R6 代码、冻结供体 Adapter、严格 UnionID/OneID 流程、管理/公开 API、真实统计、CSV、可重放迁移器与上线门禁均已实现；实时企微业务写不涉及。历史定义导入固定为 `disabled + unionid_required`，旧事件只进 legacy 投影，不伪造成实时 OneID 事件。

**Tech Stack:** Go modular monolith、PostgreSQL 16、OpenAPI、React 18 donor UI、TypeScript host Adapter、现有 webshell、Identity/Customer/Media stable Ports、Go/Node/browser tests。

---

## 执行规则

- 从当前 `main` 建立 `codex/content-radar-oneid` 工作树；不得混入当前工作区其他未提交改动。
- 每个任务先写失败测试，再做最小实现，再跑定向测试和全量门禁。
- 每个 PR 只交付一个用户可观察能力；下游 PR 不掩盖上游缺口。
- v2 只读基线固定为 `6bfbe5816bb89913c70adaca87d6a486260e016e`。
- 已基于开工时主线最大编号 `0048` 将 Radar migration 分配为 `0049-0051`；合并前仍需重新检查主线是否新增冲突编号。
- 雷达身份最终值必须是 Provider-verified、带开放平台 scope 的 UnionID；缺失时不得 fallback 到 OpenID。
- 不在本计划内加入消息、标签、任务、自动化或任何 Provider 写入。

## 完成证据分类

```text
OneID decision: involved; use internal/identity/port to Resolve and explicitly Provision from verified scoped UnionID
Persistence decision: local PostgreSQL transactions plus Provider read outside transaction
External Effects decision: not involved; no Provider write or Outbound intent
No-duplication evidence: no Radar identity matcher, customer key, queue, Worker, provider writer, retry or reconciliation kernel
```

## PR R0：冻结行为合同和供体边界

### Task 1：建立 Radar donor manifest

**Files:**

- Create: `docs/donor-manifests/radar-v2-6bfbe581.sha256`
- Create: `scripts/check-radar-donor-manifest.sh`
- Modify: `Makefile`
- Test: `scripts/check-radar-donor-manifest.sh`

**Steps:**

1. 从只读 v2 checkout 列出 Radar 前端、API client/type、后端测试和 migration 叶子文件。
2. 为所有供体文件写 SHA-256 和 commit 元数据。
3. 先写测试：篡改临时 fixture 后校验脚本必须失败。
4. 实现 manifest checker，禁止路径缺失、额外覆盖或 hash 漂移。
5. 运行：

   ```bash
   bash scripts/check-radar-donor-manifest.sh
   ```

6. 提交：`test(radar): freeze v2 donor baseline`。

### Task 2：冻结可观察 Behavior Contract

**Files:**

- Create: `docs/migration/radar/behavior-contract.md`
- Create: `docs/migration/radar/capability-matrix.md`
- Create: `internal/radar/contract/fixtures/*.json`
- Create: `acceptance/radar_donor_contract_test.go`

**Steps:**

1. 将列表、表单、启停、分享、二维码、详情、事件、CSV、`/r/{code}` 行为逐条列入矩阵。
2. 明确标记 v2 的真实能力、占位能力和 v3 新增闭环，不能把零统计写成已完成。
3. 先写会失败的 fixture parser/contract test。
4. 保存脱敏请求/响应 fixture 和 UI 文案/错误态合同。
5. 运行：

   ```bash
   go test ./acceptance -run RadarDonorContract -count=1
   ```

6. 提交：`test(radar): freeze behavior contract`。

## PR R1：Radar 领域、迁移与管理 CRUD

### Task 3：定义稳定领域模型和 Port

**Files:**

- Create: `internal/radar/model.go`
- Create: `internal/radar/port/repository.go`
- Create: `internal/radar/port/service.go`
- Create: `internal/radar/port/projections.go`
- Create: `internal/radar/model_test.go`
- Modify: `modules/registry.yaml`

**Steps:**

1. 先写状态机测试：draft → enabled → disabled；非法转换失败。
2. 写类型测试：link/image/pdf 字段约束、HTTPS URL、不可变 public code。
3. 定义 `RadarID`、`PublicCode`、`LinkVersion`、`ContentType`、`AuthPolicy`、`Status`。
4. 定义仓储和查询 Port；不得引用 Identity/Customer/Media 的 store/app 包。
5. 运行：

   ```bash
   go test ./internal/radar/... -count=1
   python3 scripts/check-architecture.py
   ```

6. 提交：`feat(radar): define v3 domain contracts`。

### Task 4：创建 PostgreSQL schema

**Files:**

- Create: `migrations/0050_radar_core.sql`
- Create: `migrations/0051_radar_sessions_events.sql`
- Create: `migrations/0052_radar_legacy_import.sql`
- Modify: `cmd/migrate-platform/main_test.go`
- Create: `internal/radar/store/postgres_integration_test.go`

**Steps:**

1. 先更新 migrator 测试，使新表缺失时失败。
2. 建 `radar_links`、`radar_link_versions`、receipts、audit、outbox。
3. 建 OAuth state、view session、events，使用唯一幂等约束和必要索引。
4. 建 legacy import/source map 表，与实时事件物理/逻辑隔离。
5. 添加约束：内容类型、状态、版本、code 唯一、身份字段 nullable；禁止外部身份原值列。
6. 在 PostgreSQL 16 运行 up/down/up 和并发唯一性测试。
7. 提交：`feat(radar): add owned persistence schema`。

### Task 5：实现管理命令 UoW

**Files:**

- Create: `internal/radar/app/commands.go`
- Create: `internal/radar/app/commands_test.go`
- Create: `internal/radar/store/postgres.go`
- Create: `internal/radar/store/postgres_uow_test.go`

**Steps:**

1. 测试相同 idempotency key + digest 返回原 receipt。
2. 测试相同 key + 不同 digest 返回冲突。
3. 测试配置、版本、receipt、audit、outbox 任一步失败时全部回滚。
4. 测试 CAS 并发编辑只允许一个提交。
5. 实现 create/update/enable/disable，删除只保留为不支持或软停用。
6. 运行：

   ```bash
   go test ./internal/radar/app ./internal/radar/store -count=1
   ```

7. 提交：`feat(radar): implement transactional lifecycle`。

### Task 6：管理 API 与 OpenAPI

**Files:**

- Modify: `api/openapi.yaml`
- Create: `internal/radar/http/admin_handler.go`
- Create: `internal/radar/http/admin_handler_test.go`
- Create: `internal/radar/http/problem.go`
- Modify: `cmd/aicrm/composition.go`

**Steps:**

1. 先写权限、验证、幂等、409、404、410、分页测试。
2. 将 canonical 管理路径和 schema 写入 OpenAPI。
3. 生成/校验客户端，禁止手工漂移 generated files。
4. 注册模块和 `radar.read/write/export` 权限。
5. 运行：

   ```bash
   go test ./internal/radar/http ./cmd/aicrm -count=1
   make openapi-check
   ```

6. 提交：`feat(radar): expose admin lifecycle API`。

## PR R2：冻结供体 UI 的 v3 接入

### Task 7：构建 Radar host Adapter

**Files:**

- Create: `web/v3/radarAdapter.ts`
- Create: `web/v3/radarApiShim.ts`
- Create: `web/scripts/build-radar.mjs`
- Create: `web/v3/radarAdapter.test.ts`
- Modify: `web/scripts/build.mjs`

**Steps:**

1. 先写契约测试：列表、表单、详情和动作的 donor DTO 均由 v3 DTO 正确映射。
2. 测试 401/403/409/410/422 和网络失败都有可见反馈。
3. 测试真实指标不再被映射为固定零值。
4. 使用 build-time alias/入口将冻结 Radar section 接到 v3 shim；禁止修改 donor 文件。
5. 运行 typecheck、build、adapter tests 和 donor hash gate。
6. 提交：`feat(radar): bind frozen UI through v3 adapter`。

### Task 8：webshell 页面与路由

**Files:**

- Create: `internal/radar/ui.go`
- Create: `internal/webshell/templates/admin_radar.html`
- Modify: `internal/webshell/contract.go`
- Modify: `internal/webshell/handler.go`
- Modify: `internal/webshell/renderer.go`
- Modify: `internal/webshell/templates/admin_base.html`
- Create: `internal/webshell/radar_handler_test.go`

**Steps:**

1. 先写 shell 测试，证明未认证跳登录、无权限 403、私有模板不能直出。
2. 注册列表、新建、编辑、详情 canonical 路由和有删除条件的旧别名。
3. 挂载构建产物，保持统一导航/session/CSRF。
4. 删除“入口已预留”占位，只在真实 API 和操作可用时显示完成状态。
5. 浏览器验证所有按钮真实发起 API 并展示反馈。
6. 提交：`feat(radar): mount admin journeys in webshell`。

## PR R3：公开匿名访问与三种内容查看

### Task 9：公开入口和 session

**Files:**

- Create: `internal/radar/app/public_access.go`
- Create: `internal/radar/app/public_access_test.go`
- Create: `internal/radar/http/public_handler.go`
- Create: `internal/radar/http/public_handler_test.go`
- Modify: `api/openapi.yaml`
- Modify: `cmd/aicrm/composition.go`

**Steps:**

1. 测试不存在 404、停用 410、匿名/授权分支、code 枚举防护。
2. 实现匿名 view session、短期 event token 和 `landing` 幂等写入。
3. 链接目标仅允许 HTTPS/策略域名，防开放重定向。
4. 注册 `/r/{code}` 和公共 API。
5. 提交：`feat(radar): serve safe public entry`。

### Task 10：图片/PDF viewer 与 Media Port

**Files:**

- Create: `internal/radar/http/viewer_handler.go`
- Create: `internal/radar/http/viewer_handler_test.go`
- Create: `internal/webshell/templates/public_radar_viewer.html`
- Create: `internal/webshell/static/radar/viewer.js`
- Create: `internal/webshell/static/radar/viewer.css`
- Modify: `internal/media/port/...` only if a missing stable read contract is proven
- Modify: `cmd/aicrm/composition.go`

**Steps:**

1. 先用 fake Media Port 测试 MIME、Range、缺失、无权限、超大文件和中断。
2. 若现有 Media Port 不够，只扩稳定 read Port；不得 import Media store/http。
3. 实现移动端 image/PDF viewer 和 CSP。
4. 只有客户端成功加载后接受 `image_loaded` / `pdf_opened`。
5. 实现链接 `redirected` 终态。
6. 提交：`feat(radar): complete public content viewers`。

## PR R4：严格 UnionID 与 OneID 归因

### Task 11：定义微信用户信息 Provider read Port

**Files:**

- Create: `internal/radar/port/wechat_identity.go`
- Create: `internal/wecom/adapter/radar_identity.go`
- Create: `internal/wecom/adapter/radar_identity_test.go`
- Modify: `internal/wecom/port/...` only when an owning connector contract is needed

**Steps:**

1. 先写测试：成功只产出 scoped UnionID fact。
2. 写失败测试：缺 UnionID、只有 OpenID、scope 缺失、签名/响应异常、超时。
3. 明确禁止 fallback 到 OpenID；禁止把 HTTP unionid 升级为 verified。
4. Provider 调用不得持有 DB 事务，日志只记录安全错误码。
5. 提交：`feat(radar): verify scoped unionid from provider`。

### Task 12：Identity Resolve/显式 Provision 编排

**Files:**

- Create: `internal/radar/app/oauth_callback.go`
- Create: `internal/radar/app/oauth_callback_test.go`
- Create: `internal/radar/store/oauth_postgres_integration_test.go`
- Modify: `internal/identity/port/...` only if caller-owned UoW support is missing
- Modify: `cmd/aicrm/composition.go`

**Steps:**

1. 用 fake Ports 写表驱动测试：resolved、not_found、pending、conflict。
2. 证明 not_found 只有 verified scoped UnionID 才显式 provision。
3. 证明 pending/conflict 不创建客户、不绑定事件、不自动合并。
4. 用 PG16 failure injection 证明 consume state、Identity 结果、session、event、receipt、audit/outbox 原子。
5. 若 Identity 当前只能独立提交，先补 UoW-aware stable Port 和其测试。
6. 提交：`feat(radar): resolve unionid through oneid`。

### Task 13：OAuth 回调与会话安全

**Files:**

- Modify: `internal/radar/http/public_handler.go`
- Create: `internal/radar/http/oauth_handler_test.go`
- Modify: `internal/platform/config/config.go`
- Modify: `deploy/install-release.sh`
- Create: `docs/runbooks/radar-oauth.md`

**Steps:**

1. 测试 state 过期、重复、错 code/版本、Provider 超时、Identity conflict。
2. 实现 HttpOnly/Secure/SameSite cookie 和安全回跳。
3. 配置默认 disabled；release check 验证 callback 域名和 open-platform scope 已配置。
4. 运维手册写启用、验证、回滚和隐私排查，不写 Secret。
5. 提交：`feat(radar): close oauth session journey`。

## PR R5：真实统计、事件查询和 CSV

### Task 14：事件写入与统计 SQL

**Files:**

- Create: `internal/radar/app/events.go`
- Create: `internal/radar/app/stats.go`
- Create: `internal/radar/store/events_postgres.go`
- Create: `internal/radar/store/stats_postgres.go`
- Create: `internal/radar/store/stats_postgres_integration_test.go`

**Steps:**

1. 建 fixture 覆盖匿名刷新、授权重复回调、三种内容终态、跨时间区间。
2. 测试 PV、distinct identity UV、真实查看次数、转化率口径。
3. 测试客户 root 后续变化不破坏 identity-based UV。
4. 使用索引和 `EXPLAIN` 证明常规查询目标性能。
5. 提交：`feat(radar): calculate evidence-backed metrics`。

### Task 15：客户安全投影与 CSV

**Files:**

- Create: `internal/radar/app/event_queries.go`
- Create: `internal/radar/http/export.go`
- Create: `internal/radar/http/export_test.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. 先写 Customer stable Port fake，证明 Radar 不跨表查询。
2. 测试筛选、游标/分页、时间半开区间、CSV escaping 和上限。
3. 测试响应、CSV 和日志中不存在原始 UnionID/OpenID/external_userid/手机号。
4. Adapter 映射供体身份列为安全客户投影/掩码，不伪装旧占位字段。
5. 提交：`feat(radar): expose safe events and csv`。

## PR R6：v2 历史数据导入

### Task 16：只读快照分析与 dry-run

**Files:**

- Create: `cmd/migrate-radar-v2/main.go`
- Create: `cmd/migrate-radar-v2/main_test.go`
- Create: `internal/radar/migration/source.go`
- Create: `internal/radar/migration/report.go`
- Create: `docs/migration/radar/source-inventory.md`

**Steps:**

1. 在获得明确授权的只读快照上重新盘点 schema、数量、状态、URL、媒体和身份字段。
2. 禁止连接 v2 作为正常运行时数据源；迁移器只运行于 CLI。
3. dry-run 输出 counts、quarantine、digest、缺失媒体和不可归因身份，不写目标库。
4. 测试整数 ID 相等、姓名/手机号相同不会生成 OneID 关联。
5. 提交：`feat(radar): add read-only migration preflight`。

### Task 17：可重放导入与复核

**Files:**

- Modify: `cmd/migrate-radar-v2/main.go`
- Create: `internal/radar/migration/importer.go`
- Create: `internal/radar/migration/importer_integration_test.go`
- Create: `docs/runbooks/radar-v2-import.md`

**Steps:**

1. 测试同一 snapshot digest 重跑无重复，不同 digest 必须显式新批次。
2. 内容默认 disabled/draft；不合格 URL/媒体进入 quarantine。
3. 历史点击写 legacy 表，不写实时 events，不生成已授权用户。
4. 输出 source-to-target manifest、校验和、逐类计数与回滚批次键。
5. 先 staging 导入、人工复核，再单独获得生产执行授权。
6. 提交：`feat(radar): import legacy data safely`。

## PR R7：端到端、发布与完成门禁

### Task 18：真实浏览器 journeys

**Files:**

- Create: `scripts/radar-shell-e2e.mjs`
- Create: `scripts/radar-public-e2e.mjs`
- Create: `acceptance/radar_journey_test.go`
- Modify: `Makefile`

**Steps:**

1. 测试管理端列表、新建、编辑、启停、分享、二维码、详情、事件、CSV。
2. 测试 link/image/pdf 在移动端 viewport 的公开访问。
3. 用 Provider staging/fake contract 覆盖严格 UnionID 成功与缺失失败。
4. 测试刷新、回退、重放和网络重试无重复指标。
5. 保存脱敏截图/视频和 receipt 证据。
6. 提交：`test(radar): cover complete user journeys`。

### Task 19：安全与架构门禁

**Files:**

- Create: `scripts/check-radar-boundaries.sh`
- Create: `scripts/check-radar-pii.sh`
- Modify: `Makefile`
- Modify: CI workflow file selected by repository convention

**Steps:**

1. 静态检查 Radar 不 import Identity/Customer/Media 的 app/store/http/provider。
2. 检查 Radar SQL 只触达自有表。
3. 扫描 fixtures、日志、DB dump、CSV 和 bundle，确保无原始外部身份/Secret。
4. donor hash、OpenAPI、modules、Go、Node、PG16、browser tests 全部进入 CI。
5. 提交：`ci(radar): enforce identity and donor boundaries`。

### Task 20：灰度发布与验收

**Files:**

- Create: `docs/runbooks/radar-release.md`
- Create: `docs/migration/radar/acceptance-report-template.md`
- Modify: `deploy/install-release.sh`

**Steps:**

1. 默认关闭 Provider；先发布 schema/API/UI，再在 staging 开启 OAuth。
2. 验证 `/healthz`、`/readyz`、release SHA、真实页面/API/DB receipt。
3. 用内部测试雷达灰度：匿名 → link/image/pdf → UnionID/OneID → stats/CSV。
4. 检查日志和响应无 PII，再逐步开放正式 public route。
5. 回滚优先停 OAuth/停用雷达；schema 保持前向兼容，不删除已提交事件。
6. 生成最终验收报告，明确 deployed、local-only、disabled 或 unverified 项。
7. 提交：`docs(radar): add release and verification runbook`。

## 每个 PR 的统一验证命令

以仓库实际 Make target 为准；不存在的 target 先在对应 PR 中定义：

```bash
gofmt -w internal/radar cmd/aicrm cmd/migrate-radar-v2
go test ./internal/radar/... ./acceptance/... ./cmd/aicrm/... -count=1
go test ./... -count=1
npm --prefix web run typecheck
npm --prefix web run build
bash scripts/check-radar-donor-manifest.sh
bash scripts/check-radar-boundaries.sh
bash scripts/check-radar-pii.sh
make openapi-check
```

## 最终 DoD 检查单

- [ ] 三种内容的管理与公开旅程真实完成。
- [ ] Provider Adapter 最终只产出 scoped verified UnionID fact。
- [ ] UnionID 通过 Identity Port 与其他 ID 关联并解析 `customers.id`。
- [ ] 缺 UnionID 不 fallback OpenID；conflict/pending 不猜客户。
- [ ] 原始外部身份不出现在 Radar DB、日志、API、CSV 和 bundle。
- [ ] 统计与事件基于幂等事实，不是零值/Mock/HTTP 200。
- [ ] 管理命令和授权回调的原子 UoW 已由 failure injection 证明。
- [ ] 供体业务文件 hash 未变，v2 无运行时依赖。
- [ ] 无新队列、Worker、Provider writer 或第二套身份内核。
- [ ] 历史数据与实时指标隔离，生产导入有单独授权和复核。
- [ ] 发布结果用页面/API/PG16/release SHA 证据验证，而非仅凭 PR/CI。
