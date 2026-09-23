# PR06 内容包与群运营 donor 契约审计（历史 preparation 记录）

> 本文件记录完整实现前的 donor/preparation 证据。当前闭包结论以
> `pr06-closure-review.md` 为准；其中的 Media receipt/lease、Group Ops
> runtime、HTTP 和 Composition 实现已取代本文的 preparation-only 状态。

## 冻结边界

- v3 基线：`origin/main@19384b93fe362c7786edc81dd5595b79570f6bb1`。
- donor：`/tmp/aicrm-v2-audit.yN3jmr@6bfbe5816bb89913c70adaca87d6a486260e016e`，只读。
- 目标：冻结 donor 的内容包引用、群运营计划定义和运行/效果观察契约，供后续完整实现复核；本文件对应的历史提交本身是 preparation-only，不代表当前 `pr06-closure-review.md` 的实现状态。
- 证据：原样前端 35 对 source/target 的 SHA-256 与 `cmp` 说明在
  `docs/migration/groupops/pr06-donor-sha256.txt`，完整文件映射和排除项在
  `docs/migration/groupops/pr06-donor-manifest.yaml`。

本 PR 不带客户、OneID、手机号、external customer、受众、Campaign、客户自动化、收件人筛选或客户标记行为。群引用和 `target_reference` 均为本领域不解释的 opaque 值；不会由 Group Ops 推断客户身份或生成客户。

## 可复用后端叶子

`internal/groupops/domain` 是无 I/O 的计划、节点、typed material、Webhook 引用和状态转换校验。计划状态为 `draft -> active <-> paused -> archived`，归档是终态；节点仅有 `message` 和 `delay` 两类，素材计划由 `image`、`miniprogram`、`attachment`、`group_invite` 加稳定服务端 ID 组成，最多 9 个引用。结构化输入遇到 customer/OneID/phone/segment/audience/Campaign/recipient 或 secret/credential 字段时 fail closed。

`internal/groupops/app/service.go` 只实现本地计划的创建、读取、CAS 更新、成员/群引用/节点增删改、生命周期转换、Webhook 描述符保存和内容预览。写入通过 v3 UoW 的本地事件/幂等收据 seam，未持有事务调用网络；Provider、Store、worker、HTTP 均未复制。`port.go`、`dispatch.go`、`runtime.go`、`history.go` 保留跨层稳定 DTO/port：运行状态明确区分 `accepted`、`provider_accepted`、`delivery_proven`、`outcome_unknown`、`reconciled`、`final_failed`，但本 PR 没有 Provider 实现或真实回执。

内容包创建、校验、版本快照和素材冻结归 PR02 Media owner；PR06 只消费 Media 的 port/快照合同，不复制 `internal/media` 或直接访问其表。后续外部群写入必须由 outbound 接受不可变意图并以独立可验证的回执完成对账。

## 页面入口与 Journey

这些页面是冻结 v2 业务前端的 archive-only 快照，未接入 v3 build。所有页面沿用 v2 的请求校验和中文提示；页面本身不会被本 PR 改写。

1. `groupops.html` 是计划列表入口：列出本地计划、active 数、本地队列和运营成员，支持创建、编辑、激活、暂停、归档、删除，并可打开群目录。页面明确“本地队列不等于已发送”，不接受 run-due、broadcast 或 Webhook。
2. `groupopsDetail.html` 是编辑入口：修改名称，选择可信运营成员，选择/移除 opaque 群引用，维护有序 message/delay 节点，使用 typed material picker 写入稳定素材 ID，读取内容预览和 Webhook 描述符，并只读显示 execution projection。保存按 CAS revision 逐项写入并读回，使用 Idempotency-Key；accepted/本地队列不被显示成送达。
3. 群目录 modal (`groupOpsDirectory.ts`) 先读取负责人，再按负责人分页读取本地目录；显式刷新才调用只读目录 adapter，并保留“可能读取企微、不发送、没有 Provider 读取回执”的警告。勾选只留在本地表单，仍需保存计划。
4. 历史入口为 `groupops.html?history=1`，详情由 `history=1&id=<plan_id>` 打开；`groupOpsHistory.ts` 只读展示归档计划、两个来源的目录、计划群和历史节点，校验 `source=v1_history`、`read_only=true`、`real_external_call_executed=false` 和计划 ID 关联，不同步、不激活、不发送。
5. 群发任务历史入口为 `automation.html?broadcast_job_history=1[&history_id=<id>]`；本次补齐了 `admin/sections/broadcastJobHistory.ts`，只展示封存任务观察、计数、原状态和原 Provider 标记，不能创建、发送、重试或调用 Provider。
6. 生成客户端还保存 run-due、broadcast、Webhook inbound、execution/reconcile 和 execution-runtime compat 合同，供后续 adapter 对照；当前页面不触发这些外部效果。

## 前端冻结闭包与装配门禁

本节把“原样搬运”定义成可回归的文件闭包，而不是把一个 v2 页面目录直接部署。`pr01` 已冻结共享运行时 bundle；PR06 复用同一组共享字节，不另行裁剪、改写或复制一套运行时。当前 archive 的 35 个前端文件分为：

| 闭包 | 数量 | 文件职责 |
| --- | ---: | --- |
| 页面模板 | 2 | `admin/templates/groupops.html`、`groupopsDetail.html`；业务样式内嵌，无外部 Group Ops CSS |
| 业务 section 与 DOM 交互 | 4 | `groupOpsDirectory.ts`、`groupOpsHistory.ts`、`broadcastJobHistory.ts`、`util.ts` |
| API adapter 与 characterization runner | 7 | `groupOpsDirectory*`、`groupOpsHistory*`、`broadcastJobHistory.ts`、`admin.test.ts`、`capabilities.ts` |
| 生成 DTO/路径合同 | 7 | Group Ops、workspace、history、execution-runtime、broadcast-history、operation-member edge、health schema |
| legacy mount bridge | 1 | `admin/legacy.ts` 中的 Group Ops history/broadcast history dispatch 与模板页 mount 入口 |
| PR01 共享运行时 bundle | 14 | controller、main、nav/registry、`api/admin`、transport、shared client/types/mock、feedback/picker/runtime/tokens |

逐字节闭包还满足以下事实：`web/donors/groupops-v2/src` 目录恰好只有上述 35 个文件；没有 PNG/SVG/字体或其他静态业务资源，没有独立 Group Ops stylesheet，模板/section 使用的样式均在冻结文件中。`tokens.css` 属于 PR01 共享 bundle，不能另造一份或改变内容。`scripts/check-pr06-donor-manifest.sh` 会先解析 donor commit，再对 35 个 source/target 对逐一校验 donor SHA、目标 SHA 和 `cmp`，最后检查 archive 文件集合及模板不得包含 `<aside>`/`.side` 第二侧栏。

页面 DOM/交互闭环也冻结如下：

- `groupops.html` 的操作是列表刷新、创建计划、编辑、activate/pause/archive、删除 draft 和打开群目录；统计区明确本地队列不等于已发送。列表行通过冻结模板的 `onClick` 绑定 `p.edit`/`p.toggle`/`p.archive`/`p.del`，不增加发送或客户筛选按钮。
- `groupopsDetail.html` 的冻结字段为 `groupOpsName`、`groupOpsStaff`、只读 `groupOpsAssets`、`groupOpsNodes`、`groupOpsMaterialNodePosition` 和 `groupOpsWebhook`；按钮/动作是返回、保存、打开目录、选择 image/miniprogram/attachment、复制 Webhook URL。节点 JSON 只接受 `message`/`delay` 与 typed `materialPlan.references`，保持原校验和提示。
- 素材选择器由冻结的 `shared/ui/picker.ts` 提供，Group Ops 只调用 `image`/`mp`/`attach` 三种 kind，对应 `images`/`mpLib`/`attach` 页面数据；图片最多 3 个、小程序 1 个、附件 9 个，节点总引用最多 9 个。`group_invite` 只作为 opaque 群资产/typed 引用保存，没有额外的客户或群发 UI。
- `groupOpsDirectory.ts` 的 modal 是 `#group-directory`；`data-gd` close/apply/read/prev/next/refresh、`data-gd-ref` 选择和 `data-gd-remove` 删除必须保留。负责人切换、分页、刷新确认、选择保留和 `crypto.randomUUID()` 幂等键行为不能改；其中 `chat_reference` 始终是 opaque 群引用。
- `groupOpsHistory.ts` 仅读 `history=1` 列表/详情，保留 `data-history-rows`、`data-prev`、`data-next`、`data-refresh`、`data-retry` 和“仅展示，不执行”的 content-package JSON；不能增加同步、激活、发送或重试。
- `broadcastJobHistory.ts` 仅读 `broadcast_job_history=1` 及可选正整数 `history_id`，保留 `data-broadcast-job-history*` 分页控件和失败提示，不作为 PR06 可开放入口，不创建/发送/重试任务。其静态 section 仍是 Group Ops archive 的 35 文件闭包，必须保留哈希；旧的外层 `automation.html` 由 PR07 管理，不能为 PR06 偷带整页模板。

### 嵌套模板与运行时风险（必须在集成前处理）

1. donor `admin/main.ts` 会按 `data-page` 把非 customers 页面加载到 `admin/legacy.ts`；`legacy.ts` 静态依赖 `surveyUnresolvedHistoryHttp`，并声明二十多个其他页面的动态 import。`controller.ts`/`api/admin.ts` 同样包含客户、Audience、Campaign 等旧分支。因此这 14 个共享文件是 PR01 冻结的完整 bundle，而不是 PR06 可以单独编译或删枝的“最小依赖”。活动路由只能通过 PR01 已冻结的 runtime/mount 接入 Group Ops，禁止改动或裁剪这些 shared bytes，也禁止开放未迁移页面。
2. Group Ops history 在 `groupops.html?history=1` 与 `groupopsDetail.html?history=1&id=...` 下由 legacy dispatch 转入 `groupOpsHistory.ts`；broadcast job history 的 donor URL 是 `automation.html?broadcast_job_history=1[&history_id=N]`。后者是 PR07 的自动化外层模板/页面，不属于 PR06 的可开放业务入口；若构建器把该 section 作为静态闭包打包，仍必须使用本 manifest 的 exact file/hash，不能复制、改写或用 mock 替代。
3. `groupOpsDirectory.ts` 原样 import `p4-ai-audience` 的 operation-member edge，且 `admin.ts` 中存在 `groupOpsOperationMembersDto`。这些是 donor DTO/行为证据，不授权 PR06 引入 Audience/客户领域；active v3 route 必须由后端 staff adapter 提供等价 DTO，或关闭负责人选择。前端 donor 文件不得为适配而改字节。
4. PR10 的 v3 `admin_base` 是唯一一级壳和侧边栏。Group Ops 模板只有自己的业务 card/section nav，不能将 `admin/nav.json`、`registry.json` 或 donor `automation.html` 当作第二个壳部署；静态哈希资源可公开读取，管理 HTML 仍走 v3 Session 权限门禁。

因此，PR06 的前端验收顺序是：先用 PR01 frozen bundle 装配 v3 PR10 shell，再只开放 Group Ops 列表/详情/目录/历史对应 route，最后按既有 Journey 做 DOM/API replay；任何“只挂模板”“只挂 API”“删 shared import”“重画页面”或开放 `automation.html` 的做法都不满足本契约。

### 内容包前端边界

donor 没有独立的“内容包列表/编辑”一级页面：PR06 能逐字节复用的内容包表面只有 Group Ops 详情中的 typed `materialPlan`、预览 DTO，以及历史详情中只读的 `content_package` JSON。内容包的 preview/create/update/version 生成 API 与 Media-owned port 记录在 PR02；Media donor 的 UI 是图片、附件和小程序素材库，并未新增一套内容包编辑器。故“内容包 + 群运营”完整开放前，必须把 PR02 已冻结的内容包 UI/后端 adapter 一起纳入同一 Journey；若该 UI 尚未可开放，PR06 只能标记为未完成，不能自行创造页面、改写 Group Ops 模板或用 mock 顶替。

## HTTP/DTO 对照（仅契约证据）

以下路径和方法来自冻结 donor 的生成客户端。它们没有在本提交注册或暴露；`{plan_id}`、`{node_id}`、`{staff_id}`、`{asset_reference}`、`{execution_id}`、`{webhook_key}` 为路径参数。

### 计划、成员、群引用和节点

| 方法 | 路径 | 请求 DTO/查询 | 成功 DTO | 语义 |
| --- | --- | --- | --- | --- |
| GET | `/api/admin/automation-conversion/group-ops/plans` | `ListGroupOpsPlansParams` | `GroupOpsPlanPage` | 本地计划分页 |
| POST | `/api/admin/automation-conversion/group-ops/plans` | `GroupOpsPlanCreateRequest{name}` | `GroupOpsPlanDetail` | 建立 draft |
| GET | `/api/admin/automation-conversion/group-ops/plans/{plan_id}` | — | `GroupOpsPlanDetail` | 读取计划全量 |
| PATCH/PUT | `/api/admin/automation-conversion/group-ops/plans/{plan_id}` | `GroupOpsPlanUpdateRequest{expected_revision,name}` | `GroupOpsPlanDetail` | CAS 名称更新 |
| DELETE | `/api/admin/automation-conversion/group-ops/plans/{plan_id}` | `GroupOpsTransitionRequest{expected_revision}` | `GroupOpsPlanDetail` | 兼容删除/归档语义 |
| POST | `/plans/{plan_id}/activate`, `/pause`, `/archive` | `GroupOpsTransitionRequest` | `GroupOpsPlanDetail` | 本地生命周期，不调度 |
| POST | `/plans/{plan_id}/enable`, `/disable` | `GroupOpsTransitionRequest` | `GroupOpsPlanDetail` | 旧兼容生命周期别名 |
| GET | `/plans/{plan_id}/members` | `ListGroupOpsPlanMembersParams` | `GroupOpsMemberPage` | 计划 staff 范围 |
| POST | `/plans/{plan_id}/members` | `GroupOpsMemberRequest{expected_revision,staff_id}` | `GroupOpsPlanDetail` | draft 增加 staff |
| DELETE | `/plans/{plan_id}/members/{staff_id}` | `GroupOpsRevisionRequest` body | `GroupOpsPlanDetail` | draft 移除 staff |
| GET | `/plans/{plan_id}/group-assets` | `ListGroupOpsPlanGroupAssetsParams` | `GroupOpsGroupAssetPage` | opaque 群资产分页 |
| POST/DELETE | `/plans/{plan_id}/group-assets[/{asset_reference}]` | `GroupOpsGroupAssetRequest` / revision | `GroupOpsPlanDetail` | 绑定/移除 opaque 群 |
| GET | `/plans/{plan_id}/groups` | `ListGroupOpsPlanGroupsParams` | `GroupOpsGroupAssetPage` | 旧兼容群引用列表 |
| POST/DELETE | `/plans/{plan_id}/groups[/{chat_id}]` | `GroupOpsGroupAssetRequest` / revision | `GroupOpsPlanDetail` | 旧兼容群引用写入 |
| GET | `/plans/{plan_id}/nodes` | `ListGroupOpsPlanNodesParams` | `GroupOpsNodePage` | 有序节点分页 |
| POST | `/plans/{plan_id}/nodes` | `GroupOpsNodeRequest` | `GroupOpsPlanDetail` | 增加节点 |
| PATCH/PUT | `/plans/{plan_id}/nodes/{node_id}` | `GroupOpsNodeRequest` | `GroupOpsPlanDetail` | CAS 更新节点 |
| DELETE | `/plans/{plan_id}/nodes/{node_id}` | `GroupOpsRevisionRequest` body | `GroupOpsPlanDetail` | 移除节点 |

### 内容预览、Webhook、运行和效果

| 方法 | 路径 | 请求 DTO/查询 | 成功 DTO | 安全边界 |
| --- | --- | --- | --- | --- |
| GET/PUT | `/plans/{plan_id}/webhook-descriptor` | `GroupOpsWebhookDescriptorRequest{expected_revision,reference}` | `GroupOpsWebhookDescriptorResponse` / detail | 只返回 same-origin 路径、HMAC-SHA256 头描述和 opaque reference，不返回签名密钥 |
| GET | `/group-ops/{plan_id}/webhook` | — | `GroupOpsWebhookDescriptorResponse` | 旧兼容描述符读取 |
| POST | `/plans/{plan_id}/content/preview` | — | `GroupOpsContentValidation` | 仅校验节点、群和素材快照，不发送 |
| POST | `/plans/{plan_id}/run-due/preview` | — | `GroupOpsRunDuePreview` | 仅计算候选和 blockers |
| POST | `/plans/{plan_id}/run-due` | — | `GroupOpsRunSummary` (202) | 后续应由 runtime/outbound adapter 受理；本 PR 不调用 |
| GET | `/plans/{plan_id}/executions` | `ListGroupOpsExecutionsParams` | `GroupOpsExecutionPage` | 读取 effect projection/回执存在性 |
| POST | `/plans/executions/{execution_id}/reconcile` | `GroupOpsReconcileRequest{generation,fence,lease_expires_at,evidence_digest,delivery_proven}` | `GroupOpsExecution` | 仅接受已验证证据；`outcome_unknown` 不盲重试 |
| POST | `/api/automation/group-ops/broadcast` | `GroupOpsBroadcastRequest{plan_id}` | `GroupOpsRunSummary` (202) | inbound intent only；不代表 Provider 或送达 |
| POST | `/api/automation/group-ops/webhooks/{webhook_key}` | `AcceptGroupOpsWebhookBody` | `GroupOpsRunSummary` (202) | 需后续验签/重放保护；本 PR 不暴露 route |

执行 projection 的安全字段必须同时保留 `provider_execution_eligible`、`real_external_call_executed`、`provider_accepted`、`delivery_proven`。只有独立 receipt/evidence 才能推进对应状态，不能把 queued/accepted 当作发送成功。

### 群目录、员工范围、历史和观察

| 方法 | 路径 | 请求 DTO/查询 | 成功 DTO | 语义 |
| --- | --- | --- | --- | --- |
| GET | `/api/admin/automation-conversion/group-ops/groups` | `ListGroupOpsDirectoryGroupsParams{owner_userid,limit,offset}` | `GroupOpsDirectoryPage` | 当前本地目录快照 |
| POST | `/api/admin/automation-conversion/group-ops/groups/sync` | `GroupOpsDirectorySyncRequest{owner_staff_id,limit}` | `GroupOpsDirectoryPage` | 显式只读目录同步 |
| GET | `/api/admin/automation-conversion/group-ops/group-picker` | `ListGroupOpsGroupPickerParams` | `GroupOpsDirectoryPage` | 本地 picker 投影 |
| POST | `/api/admin/automation-conversion/group-ops/group-picker/sync` | `GroupOpsDirectorySyncRequest` | `GroupOpsDirectoryPage` | 显式 picker 刷新 |
| GET | `/api/admin/automation-conversion/group-ops/history/plans` | `ListGroupOpsHistoryPlansParams` | `GroupOpsHistoryPlanPage` | v1 归档计划 |
| GET | `/history/directory` | `ListGroupOpsHistoryDirectoryParams` | `GroupOpsHistoryDirectoryPage` | 两来源历史目录 |
| GET | `/history/plans/{plan_id}/groups` | `ListGroupOpsHistoryGroupsParams` | `GroupOpsHistoryGroupPage` | 归档计划群 |
| GET | `/history/plans/{plan_id}/nodes` | `ListGroupOpsHistoryNodesParams` | `GroupOpsHistoryNodePage` | 归档历史节点/内容包 |
| GET | `/api/admin/broadcast-job-history` | `ListBroadcastJobHistoryParams` | `BroadcastJobHistoryPage` | v1 群发任务封存列表 |
| GET | `/api/admin/broadcast-job-history/{history_id}` | — | `BroadcastJobHistoryDetail` | v1 群发任务封存详情 |
| GET | `/admin/automation-conversion/group-ops/ui` | — | page response | 计划 workspace 页面入口 |
| GET | `/admin/automation-conversion/group-ops/plans/{plan_id}` | — | page response | 计划详情 workspace |

`GroupOpsOperationMemberPage` 的 scope 固定为 `group_ops`，每项是 `staff_id`、`sender_userid`、`display_name`；它不是客户目录，也不能用于猜测 OneID。历史和群发任务响应必须带 `source=v1_history`、`read_only=true`、`real_external_call_executed=false`。

## 排除依赖与适配风险

- donor 的 `groupOpsDirectory.ts` 为保持原样，仍导入 `p4-ai-audience` 的 `listAIAudienceOperationMembers({scope:'group_ops'})`。这是唯一已识别的受众命名空间边缘；该调用不进入 v3 active build，也不等于采用 Audience 领域。Terra/Web 在挂载前必须改为 v3-owned 的可信 staff port/adapter，或不提供负责人选项；禁止修改此 donor 文件来“修复”。
- `admin.ts`、`controller.ts`、`legacy.ts`、`health.schemas.ts`、`shared/api/*` 是 PR01 已冻结且由 PR06 复用的原样共享 bundle，内部保留客户/受众/Campaign 等旧分支。PR06 不得裁剪或改写其字节；v3 只通过已冻结 runtime/mount 暴露 Group Ops route，并以路由门禁阻止未迁移分支。
- Group Ops 不拥有 Customer/OneID/Audience/Campaign 表，也不读取其 Store/app/http/provider。任何收件人筛选、客户身份解析、Campaign 绑定和效果发送均由本 PR 明确排除。
- runtime/history HTTP、Store/SQL/migration、scheduler/worker、Provider 和 outbound/composition 适配均交给 Terra。Terra 需要在本领域新建 owner 表、CAS/锁、审计、幂等收据、Outbox 和证据对账，并保证 provider 网络调用不持有数据库事务。
- PR10 的 v3 `internal/webshell/admin_base` 是唯一一级侧边栏壳。原样 donor 不能以完整 v2 页面部署，也不能引入第二个 `.side`；挂载必须由 v3-owned template/HTTP adapter 完成。

## 交付验证

后端叶子测试覆盖 domain invariants、状态终态、typed material/scope 校验、service 生命周期、CAS、幂等回放、Webhook 安全和读回。donor 供体测试为 `admin.test.ts`、`groupOpsDirectory.test.ts`、`groupOpsHistory.test.ts`；三组 runner 均以冻结 source 打包并通过，新增 `broadcastJobHistory.ts` 与既有 `broadcastJobHistory` API 配对，未改任何 donor 字节。v3 的 `go test ./internal/groupops/...`、`go vet ./internal/groupops/...`、`go test -race ./internal/groupops/...`，以及 donor 的 `go test ./internal/groupops/...` 均通过；SHA/cmp 证据固定在 `pr06-donor-sha256.txt`。
