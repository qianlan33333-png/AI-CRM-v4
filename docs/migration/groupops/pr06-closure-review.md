# PR06 内容包与群运营 donor 完整闭包复核

## 结论

本复核基于 v3 最新基线 `b0887c3`（包含 PR05 `153aebb`）和固定 donor `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`，在独立 `codex/groupops-full` worktree 完成。Group Ops 的 v3 本地计划、CAS/幂等/审计/outbox、HTTP/CSRF、目录投影、运行快照、EER 接受与 provider-disabled 完成投影已装配；donor 前端仍是唯一业务 UI，未改任何 donor 字节。

这不是 Provider 已上线的声明。当前 composition 使用确定性的 disabled Group Message adapter：本地 EER intent 可以被接受并落库，worker 随后产生 `final_failed` 的本地回执；没有真实群发、没有送达证明。素材 source capture/freeze 已经通过 Media stable ports 接入，目录刷新和人工对账则按 provider-disabled/fail-closed 基线返回明确结果，见第 9 节。

本提交不 push、merge 或 deploy。

## 1. 开发前边界分类

Group Ops 不涉及 OneID、Customer、Audience、Segment、手机号、外部客户身份、客户归属或持券记录。计划成员是本地 `staff_id`；群目标是本域不解释的 opaque `chat_reference`/`asset_reference`；素材是稳定类型和资源 ID。Group Ops 不解析身份、不建客、不自动合并客户根，也不读取客户或 Audience 表。

本 PR 同时涉及持久化、内部 durable job 和外部效果意图：owner 表、版本/CAS、幂等回执、audit/outbox、run/execution 快照与 EER 记录必须通过同一个 PostgreSQL Unit of Work 提交。Provider 网络调用不持有事务；Provider 默认 disabled；`accepted`、`queued`、`provider_accepted` 均不等于送达。

## 2. donor 实际 build path 与前端硬门禁

固定 donor 的实际浏览器链是：

```text
web/scripts/build.mjs
  -> web/src/admin/main.ts
  -> web/src/admin/legacy.ts
  -> AdminController
  -> groupops.html / groupopsDetail.html
```

`build.mjs` 以 `web/src/admin/main.ts` 为 admin entry，遍历 `registry.json` 生成两个 Group Ops 页面；`main.ts` 对非 customers 页面加载 `legacy.ts`；`legacy.ts` 继续使用宽的 `AdminController` bundle，并在 Group Ops history 页面做 section dispatch。不得改成 coupon-only、groupops-only loader，也不得另写 frontend runtime。

前端门禁结论：

- donor 业务文件 **35/35**；`docs/migration/groupops/pr06-donor-sha256.txt` 保存 donor/source/target SHA-256，`scripts/check-pr06-donor-manifest.sh` 同时对固定 donor、archive 和 active `web/src` 做 `SHA-256 + cmp`。
- HTML、TS、CSS、图标、文案、默认值、交互、URL、DTO 与 bundle 行为均逐字节保持 donor；本分支没有改 donor 业务文件。
- `internal/webshell/templates/admin_base.html` 只有一个 `class="admin-sidebar"`；Group Ops donor 模板是业务 stage，不包含第二个 `<aside>`、side shell 或导航壳。
- 没有第二套 Group Ops frontend/runtime；`web/donors/groupops-v2` 仅是 archive evidence，不是 v3 module、submodule、build 输入或部署依赖。
- donor 原有按钮（包括 data/share 等）不被屏蔽、不被美化。越出本轮能力的后端动作只能 fail-closed，不能靠隐藏按钮制造迁移完成假象。

## 3. 内容包：donor 真实 surface 和边界

固定 donor **没有独立内容包列表或编辑页面**，也没有由 donor 页面拥有的内容包创建/版本/素材快照 frontend API。`groupOpsHistory` 中的 `content_package` 是历史节点 JSON 的只读展示，不是可编辑当前实体；本 PR 不发明页面、section、字段、默认值或新的 frontend runtime。v3 只在后端提供一个不改变 donor UI 的 API-only transport adapter，把请求转给 Media-owned `ContentDeliveryService`。

当前 donor 可达详情页通过既有 Group Ops UI 完成：

- `GroupOpsNode.material_plan.references` 保存 `image`、`miniprogram`、`attachment`、`group_invite` 加正整数 ID；旧 `material_reference` 只读兼容，运行时 fail-closed。
- 既有 picker 读取现有素材页面数据并把 typed 引用写回节点；Group Ops `content/preview` 只校验计划、节点、群引用、staff 和 typed material shape。
- 保存沿用 donor 的串行 CAS 顺序：plan、staff、opaque 群、nodes、webhook descriptor，然后 preview/readback。每个 logical mutation 有幂等键和上一版本 `expected_revision`；前序已成功变更不会由后续失败自动回滚。
- 当前 v3 保存的是本域引用；运行接受时由 Media stable ports 捕获 source digest 并冻结 immutable provider-shaped material snapshot，不伪称 GroupOps 自己拥有 canonical content package。

真正的内容包创建/校验/版本/素材快照由 PR02 Media owner 的稳定 port 提供。Composition 将 Media `GroupOpsMaterialSourceCapturer`、事务绑定的 preparation reader 与 `GroupOpsMaterialSnapshotFreezer` 绑定到 GroupOps runtime：接受运行时先捕获有序引用的 source digest，再冻结 provider-shaped snapshot；Group Ops 只持久化该不可变快照，不复制 Media 表、不直接读 Media store，也不使用 Product、Customer、OneID 或 Audience 代替素材依赖。现有 Media ContentDelivery 的 create/update/version/preview/bind 仍由 Media owner 负责，GroupOps 仅通过 API-only adapter 暴露这些调用路径：`POST /content-packages`、`PUT|PATCH /content-packages/{package_id}`、`POST /content-packages/{package_id}/versions`、`POST /content-packages/preview`、`POST /content-packages/bind` 和 `GET /content-packages/bindings/{campaign_code}/{content_plan_id}`。这些是后端 DTO/port 路由，不是第二个 donor 页面或 frontend runtime。Media 的 `media_group_ops_preparation_receipts/items` 由 approved Provider preparation adapter 在网络调用完成后通过 transaction-neutral writer 写入，receipt/lease、source digest、provider attachment、audit 和 outbox 在一个 Media UoW 内提交；provider-disabled adapter 不持有 writer。group_invite 不写伪 receipt，直接以 Capturer 的真实 title/url/description 生成 link；其他素材没有真实 lease 时仍确定性 fail-closed。

## 4. 页面、URL、DTO 和可观察 Journey

### 业务页面

- `/admin/groupops.html`：donor 列表，展示计划总数、active 数、本地队列和运营成员；保留创建、编辑、启用/暂停、归档、draft 删除和群目录交互。
- `/admin/groupopsDetail.html?id=<plan_id>`：donor 详情，展示计划、最多 5 位 staff 候选、群目录 modal、节点/typed material、content preview、Webhook descriptor 与 execution projection。
- `/admin/automation-conversion/group-ops/ui`、`/groups/ui`、`/plans/<plan_id>`：v3-owned carrying aliases，302 到上述 canonical donor 页面；不创建独立内容包页面。
- `?history=1` 历史页继续使用 donor history section，只读计划/目录/群/节点和历史 `content_package`，不激活、不发送、不重试。

### active API surface

| 方法 | URL | 响应/事实 |
| --- | --- | --- |
| GET/POST | `/api/admin/automation-conversion/group-ops/plans` | `GroupOpsPlanPage` / 新 draft `GroupOpsPlanDetail` |
| GET/PATCH/PUT/DELETE | `/plans/{plan_id}` | detail；DELETE 是 archive 语义，服务端仍做状态/CAS 校验 |
| POST | `/plans/{plan_id}/activate\|enable`、`pause\|disable`、`archive` | 本地状态机转换和 detail |
| GET/POST/DELETE | `/plans/{plan_id}/members[/{staff_id}]` | 本地 staff scope 和 detail |
| GET/POST/DELETE | `/plans/{plan_id}/group-assets[/{asset_reference}]`、`groups[/{chat_id}]` | opaque 群引用和 detail |
| GET/POST/PATCH/PUT/DELETE | `/plans/{plan_id}/nodes[/{node_id}]` | 有序 message/delay 节点和 detail |
| GET/PUT | `/plans/{plan_id}/webhook-descriptor`；GET `/webhook` | 不含密钥的 HMAC descriptor |
| POST | `/plans/{plan_id}/content/preview` | `GroupOpsContentValidation`，只读校验 |
| POST | `/api/admin/automation-conversion/group-ops/content-packages` | Media-owned content package create；API-only、admin+CSRF+idempotency |
| PUT/PATCH | `/api/admin/automation-conversion/group-ops/content-packages/{package_id}` | Media-owned package update/CAS |
| POST | `/api/admin/automation-conversion/group-ops/content-packages/{package_id}/versions` | Media-owned explicit version update/CAS |
| POST | `/api/admin/automation-conversion/group-ops/content-packages/preview` | Media-owned package validation/preview；只读 |
| POST | `/api/admin/automation-conversion/group-ops/content-packages/bind` | Media-owned content package bind to opaque campaign/plan/group reference |
| GET | `/api/admin/automation-conversion/group-ops/content-packages/bindings/{campaign_code}/{content_plan_id}` | Media-owned binding readback |
| POST | `/plans/{plan_id}/run-due/preview` | `GroupOpsRunDuePreview`，候选/blockers，不受理 |
| POST | `/plans/{plan_id}/run-due` | `202 GroupOpsRunSummary`，本地 EER intent acceptance |
| GET | `/plans/{plan_id}/executions` | `GroupOpsExecutionPage`，本地 projection |
| POST | `/plans/executions/{execution_id}/reconcile` 及 plan-scoped alias | generation/fence/lease/evidence 校验后对账 |
| GET/POST | `/api/admin/automation-conversion/group-ops/groups[/sync]`、`group-picker[/sync]` | 本地完整群目录 projection/刷新意图 |
| GET/POST | `/api/admin/common/operation-members[/sync]` | `scope=group_ops` 的本地 eligible staff；拒绝 Audience scope |
| POST | `/api/automation/group-ops/broadcast` | admin+CSRF 的本地 run acceptance，不是送达 |
| POST | `/api/automation/group-ops/webhooks/{webhook_key}` | HMAC、时间窗、单值 headers、digest-only replay 后的本地 run acceptance |

所有 mutation 要求 admin session、写权限、CSRF 和 `Idempotency-Key`；GET 需要可读的 admin/staff principal。响应带本地安全投影，错误响应明确 `provider_disabled`/`protocol_auth_unavailable` 等 fail-closed 状态。

## 5. 状态机、快照、任务和回执

计划状态是 `draft -> active <-> paused -> archived`，`archived` 为终态；active 前必须通过内容校验。节点仅有 `message` 和 `delay`；message 必须有文本或 typed material，delay 为 1–10080 分钟。每次运行冻结 plan revision、content snapshot、Media capture/freeze 产出的 immutable material snapshot、target digest 和 sender owner projection；execution key 为 `node × opaque group`，重复请求不重复建 intent。

运行状态区分；每个 execution 还保存独立 `scheduled_for`。delay 节点累计为下一个 message 的 due time，EER/River job 的 `ScheduledAt` 与该事实在同一 Unit of Work 写入，因此重启后仍由 durable jobqueue 执行，不是进程内 ticker：

```text
accepted -> provider_accepted -> delivery_proven
        \-> outcome_unknown -> reconciled
        \-> final_failed
```

当前 disabled adapter 的实际终态是 `final_failed`，receipt digest 可审计但 `provider_accepted=false`、`delivery_proven=false`、`real_external_call_executed=false`。若未来 Provider 调用跨过业务边界却无终态，EER lease recovery 进入 `outcome_unknown`，禁止换幂等键盲重试；只有独立 evidence verifier、精确 generation/fence/lease 和 EER reconcile receipt 才能进入 reconciled。Group Ops 不把 HTTP 202、River job、EER accepted 或本地目录刷新当成企微成功。

Webhook 使用 HMAC-SHA256，签名输入是 `timestamp + "\\n" + event_id + "\\n" + exact body`；只保存 event/payload digest，不保存 signature、raw body、token 或 secret。相同事件和相同 payload 被拒绝为已消费，payload drift 进入 conflict；缺 secret/replay store/DB 时返回 503。

## 6. 后端 owner、权限和跨域审计

`migrations/0012_group_ops.sql` 的 Owner 是 Group Ops，包含：plans、members、opaque group assets、nodes、webhook descriptors、operation receipts、audit events、outbox、runs、executions、directory projection、refresh receipts 和 protocol replay digests。Store 只访问这些表；同一事务内完成业务状态、幂等 receipt、audit/outbox、run/execution 与 EER intent 的提交。Media-owned `0016_media_content_packages.sql` 另拥有 content package/version/snapshot 与 `media_group_ops_preparation_receipts/items`；Group Ops 只经稳定 port 读取冻结事实。

v3 后端新增/绑定的主要文件：

- `internal/groupops/store/postgres.go`、`internal/groupops/app/runtime.go`、`internal/groupops/http/handler.go`、`internal/groupops/module.go`、`internal/groupops/ui.go`；
- `internal/outbound/group_message.go`、`internal/externaleffects/store.go` 的 completion/reconcile port；
- `cmd/aicrm/group_ops_adapters.go`、`cmd/aicrm/group_ops_protocol_auth.go` 的 composition-owned Adapter；
- `api/openapi.yaml` 的实际 route/DTO/security 描述。

Group Ops 不 import Customer/Identity/Audience/Segment/Campaign 的 app/store/http/provider，不访问这些领域表；员工读取通过 Composition 的 Access stable port，素材读取通过 Composition 的 Media stable ports；GroupOps store 仅访问自身表。它只通过 stable EER/outbound contracts 接受 opaque Group Message 意图。唯一 sidebar 是 PR10 `admin_base`，仅允许 v3-owned mounting/adapter；donor frontend 未补功能。

## 7. donor 与 v3 的不可混用项

donor 的旧完整 runtime、v2 auth/contact/provider、历史 Media/EER 直表访问只作为 behavior/协议证据，不能成为 v3 Go module、submodule、远程运行依赖或正常数据源。v3 不复制 donor backend 目录，不另建 scheduler/worker/lease/retry，不把 Audience operation-member route 当作客户受众选择。

生成 client 中的 `/enable`、`/disable`、`/groups`、`/webhook` 和无 plan reconcile 是同一 donor surface 的兼容 URL；它们已在 v3 handler/OpenAPI 明确映射，不能演化为第二实现。历史 `automation.html?broadcast_job_history=1` 属于其他 PR owner，本 PR 不把它变成新的发送、重试或内容包入口。

## 8. 可执行检查和窄验证

门禁：

```text
AICRM_PR06_DONOR_DIR=/private/tmp/aicrm-v2-pr04-donor \
AICRM_PR06_DONOR_SHA=6bfbe5816bb89913c70adaca87d6a486260e016e \
  scripts/check-pr06-closure.sh
```

该脚本会调用 donor manifest，验证固定 SHA、35/35 archive 与 active build source 的 SHA-256/cmp，检查 `build.mjs -> main.ts -> legacy.ts -> AdminController`，检查单 sidebar、无第二 content-package 壳、无重复 active GroupOps frontend、跨域 import/Access-owned table bypass 禁止、Media SourceCapturer/Freezer Composition binding、GroupOps implementation/OpenAPI markers 和 webhook secret fail-closed markers。

当前窄验证目标：

- `go test ./...`：Go 单测、EER integration、Group Ops service/domain/runtime/http/outbound；
- `scripts/check-pr06-donor-manifest.sh` 和 `scripts/check-pr06-closure.sh`；
- `ruby -e 'require "yaml"; YAML.load_file("api/openapi.yaml")'`；
- `git diff --check`；
- donor 35/35 active source：SHA-256 + `cmp`，不执行任何 donor 改写。

## 9. 基线边界与 PR01/PR02 依赖

以下是已实现且按边界明确 fail-closed 的运行基线，不是待补的 PR06 页面或第二套实现：

1. **PR02 Media port 已接入**：Media owner 负责 ContentDelivery 的 create/update/version/preview/bind 与素材事实；Group Ops API-only adapter 有真实 HTTP/DTO 调用路径，Composition 将其绑定到同一 Media service。Composition 通过 `GroupOpsMaterialSourceCapturer`、Media-owned preparation reader 和 `GroupOpsMaterialSnapshotFreezer` 取得 source digest、receipt/lease（非 invite）和 immutable provider-shaped snapshot。group_invite 使用 Capturer 的真实 link 字段，无需 Provider receipt；image/attachment/miniprogram 由 `media_group_ops_preparation_receipts/items` 提供 source-bound receipt/lease，缺失、过期或 source swap 时拒绝运行接受，不生成 kind/id 派生 source digest。transaction-neutral preparation writer 只给 approved Provider adapter 使用；其 deterministic CI seam 已证明写入真实 receipt 后 freezer 接受 provider-shaped attachment，provider-disabled adapter 不调用该 writer。GroupOps 不读 Media 表，不把 Provider-disabled 误报成送达。
2. **Group Directory source**：本地目录 projection 的 list/selection、计划 group asset CRUD、运行目标 owner 解析均可用；composition 显式装配 concrete `providerDisabledGroupOpsDirectory`，id-dev 未注入真实企微目录 source 时，`/groups/sync` 明确返回 503/provider-disabled，保留已有本地 projection，不猜群、owner 或 sender。PR01/企微只读 port 未来只需替换该稳定 source adapter，并保持 owner-complete snapshot contract。
3. **独立 reconciliation evidence verifier**：EER transactional reconcile adapter 已绑定并要求精确 lease/fence/generation；composition 显式绑定 concrete `providerDisabledGroupOpsEvidence`，它拒绝把 HTTP digest 或 `delivery_proven` 自报升级为独立证据，因此人工 reconcile 明确 `provider_disabled`/fail-closed。该行为是防止误对账的完成门禁。
4. **Provider 生产启用**：composition 显式装配带 Media preparation-writer port 的 `GroupMessageProvider`，当前配置为 disabled；它把可接受的 EER intent 确定性投影为 `final_failed`，不会网络调用或写 preparation receipt，也不产生 provider accepted/delivery proof。真实 WeCom group-message provider、回执查询和对账 Journey 属于后续启用验收，不改变本轮 donor/UI 或 GroupOps owner boundary。
5. **PR01 platform contract**：admin session/CSRF、UoW、durable jobqueue、EER completion/recovery 已通过 stable seams 接入；Composition 仍是唯一装配点，GroupOps 不自建队列/worker/lease/retry。

因此，本 PR06 donor UI、local plan/group/node editing、preview、Media material snapshot、idempotency、audit、EER acceptance、directory selection 和 deterministic disabled final-failed receipt 已具备可检查实现；只有真实目录读取、真实 Provider 发送/回执和独立证据对账在对应依赖启用后，按同一 API/Journey contract 验收。
