# PR03 企微标签管理：冻结 donor 前端闭环审计

> 审计性质：只读 donor 审计与迁移准备。本文不改变 donor 前端字节，也不授权把 v2 作为 v3 的运行时、数据库、Go module 或远程依赖。

## 结论

冻结 donor 是 `qianlan33333-png/AI-CRM-v2` 的
`6bfbe5816bb89913c70adaca87d6a486260e016e`。现有 PR03 donor 准备提交
`564780c1ece8073e8ac4ffcb9d04d6627c0709b4`（`codex/import-wecom-tags`）已
冻结了标签领域行为和三个直接页面文件，但它不是一个可合入、可部署的完整
用户能力：它还没有 v3 HTTP、PostgreSQL migration、OpenAPI、Composition Root、
Worker/Provider 回执或单壳页面挂载。

PR01 已经把 donor 的完整 `web/` 源码、构建脚本、依赖锁和前端测试整体冻结进
v3。以包含 PR01 的观察树
`f250aa3313df2f87177e2e18ac98cb7b57989eb5`（`codex/feature-media`）核对，
本文件列出的全部 tag 页面闭包文件与 donor SHA-256 一致。因此 PR03 不应再次
改写或复制 `controller.ts`、`admin.ts` 等共享前端；最终 PR 只需在 v3 后端提供
donor 已经在请求的兼容路径/DTO，并用 v3 webshell 将原样 fragment 挂入现有
一级侧边栏壳。

**前端硬门禁：** `web/src/admin/templates/tags.html`、所有共享运行时代码、
请求 DTO、文案、默认值、内联样式、交互顺序和构建输入必须逐字节保持 donor
版本。`web/dist/admin/wecom-tags.html` 这个 donor 生成页面包含第二套 v2
sidebar，禁止直接作为 v3 页面发布。

## 两轴边界判断

| 轴 | 结论 | 依据与实现边界 |
| --- | --- | --- |
| OneID / 外部身份 | 不涉及 | 页面只管理标签组、标签目录和同步；没有 `external_userid`、客户归属、客户 ID 或身份解析。`wecomTagPicker` 是共享标签选择器，只传 tag id，客户打标/去标不属于本板块。 |
| 持久化 / 异步 / 外部效果 | 涉及两种边界 | 组/标签 CRUD 是 tag-owned 本地事务；同步按钮是本地命令接受、幂等收据、Outbox/EER 与后续 Provider 读取效果。网络调用不得持有 DB 事务，`outcome_unknown` 不得换 key 盲重试。Provider 默认 disabled。 |

因此 PR03 的最终交付仍必须是完整闭环：登录与权限门禁 → 页面读取 → 组/标签
CRUD/归档 → 同步命令接受与真实回执状态 → 刷新持久化 → 审计可查。只迁移
HTML、只返回 HTTP 200、只排队或只保留本地 catalog 都不构成完成。

## 冻结文件与 SHA-256

下表中的 donor hash 均由冻结 commit 的工作树计算。观察树中所有 target hash
已逐一核对为相同值；自动校验脚本见同目录的
`../../scripts/check-pr03-frontend-donor-manifest.sh`（迁移到主线后从仓库根执行）。

### 直接页面与共享运行时闭包

| donor / target path | 角色 | SHA-256 |
| --- | --- | --- |
| `web/src/admin/templates/tags.html` | 标签管理页面 fragment（直接 donor） | `50753645bc7bd5843727f88b60e95b12e7f48a229592d192e467272bb6275606` |
| `web/src/admin/sections/wecomTagPicker.ts` | 共享企微标签选择器 | `e4094bb5f7c6578c4f4bc580409c3e6b3a7588803d5266d744685d52a1249e71` |
| `web/src/admin/sections/wecomTagPicker.css` | 共享选择器样式 | `634e610ac25a1a5d31df65249ee2f44b534b784364c78cea1d6848cd429bf134` |
| `web/src/admin/controller.ts` | 模板控制器；包含 tags 状态、派生视图和命令绑定 | `2c0d51283902b370c431dd04124bcc2215214eac314099fa5d6001ccdb038500` |
| `web/src/admin/legacy.ts` | legacy loader；加载模板 runtime/controller | `37181a469d55c60e8cd1397894f0c3aae352622c0ebf43ab9f28e67da42a5b48` |
| `web/src/admin/main.ts` | 根据 `data-page` 加载 admin runtime | `61bc0ef4ff883bb243af79f989813bbe29c3109168544f32d7358a7608514161` |
| `web/src/admin/sections/util.ts` | `copyText` 等原样辅助逻辑 | `75e3f2b24bc5e031382f7e5c58ddf64578eb7708b06d30467edaf80464362621` |
| `web/src/shared/ui/runtime.ts` | `#tpl` → `#stage` mini-runtime | `1122c0be280b1f62c1784510459471bd3ffcc6989493f103daf811900411e66a` |
| `web/src/shared/ui/feedback.ts` | toast、confirm、busy 的原样交互 | `5c16cd3b057663d2b0c5d2a01416e6330ec979513c6754f1f64f6e41f364a546` |
| `web/src/shared/ui/picker.ts` | shared picker runtime | `690639bde2fb605024a05fe3196f2ddf8fd5b4ae87c76ef3ff5868a7adf912c0` |
| `web/src/shared/ui/tokens.css` | donor token 样式入口 | `0f9b719686a8516727ad86fa9475b10cbb059fd10003b3eb6ef041900c7ee3b0` |

### 请求、类型、生成物与构建输入

这些文件不能只抽取“标签几行”：`admin.ts` 和 `controller.ts` 是 v2 共享单体，
admin bundle 的静态依赖必须整体保持 donor。PR01 的
`docs/donor-manifests/pr01-web.sha256` 是更高层的完整 `web/src`/build
文件集合门禁，本表是 PR03 的命名闭包索引。`web/src/api/admin.test.ts`
现由 v3 测试边界拥有并继续在完整前端测试中执行，因此不再作为标签模块的 donor 字节冻结文件。

| donor / target path | 角色 | SHA-256 |
| --- | --- | --- |
| `web/src/api/admin.ts` | tags list/read/write DTO adapter | `574293ff7ab6fb0c6d1227ff879649dbc05cf454caaaec6a0fbc1d23727df9ee` |
| `web/src/api/transport.ts` | same-origin credentials、CSRF、非 2xx 解包 | `fc5e4b447d10487f571fdafd953cb51756274bc40b019bb51b6cdd61cfbad885` |
| `web/src/api/generated/p4-tag-compat/p4-tag-compat.ts` | frozen path/method/status/DTO 生成物 | `ee681aa3460deccb41aad4daea555e3428301d30bafc804860ab6d83df1a930e` |
| `web/src/api/generated/health.schemas.ts` | frozen tag DTO/schema types | `7f1bc1d05b3e012de46b1d53ef7b56319c0bc032a1c0389fa3fd138c7218b40d` |
| `web/src/shared/api/client.ts` | `AdminApi` tags interface + HTTP implementation | `2e1bfde0d36f6ab6637da66fddf6b7ee94984364a7175ad787b1da80f98695d5` |
| `web/src/shared/api/types.ts` | `TagGroup` / `WecomTag` / `AdminDb` types | `6fea805d568cf91b7c43292128c2a2b0694cf6515d85c264e048726a270c5a20` |
| `web/src/shared/api/mockData.ts` | donor mock shape used by shared controller | `d202111695e91432879fb16a3101eae6b7f10ba53237dd493989ffd284c8264c` |
| `web/src/admin/nav.json` | donor nav metadata/icon/copy | `ee7a9a6629dcdaae4d9792ffcd757cee850bad796edcfb7ff68b6028206f1ed1` |
| `web/src/admin/registry.json` | donor screen key `tags` and title | `df5f131d9b322e435a09fccdc89c4f8269f3ef03f7856ece250d412af71bb145` |
| `web/scripts/build.mjs` | donor transform, tags output name, hashed bundle | `fb932a1a43a7174f206f690fd6a6d5b309268de6200b5132b4c413c8cbb7697d` |
| `web/tsconfig.json` | donor TypeScript build contract | `1103af563387917ef59c965fba156498f5c7453ec700a8c7a4ebe2b9dfb12435` |
| `package.json` | donor Node/npm/esbuild versions and scripts | `ab5eaf7d1c014619f2d3ef8eeebd4f7a0336f0384df4aaa5e96dc0cad245b19e` |
| `package-lock.json` | donor dependency lock | `bbcf2ecd7a3eaf9c5fe0b8dc594047e7cf733d86b6eb89c601391b6059d34408` |

## 页面 fragment 与交互清单

直接 donor 文件 `tags.html`（冻结行 2–80）没有 `<html>`、`<body>`、v2
`<aside class="side">` 或 donor `shell`。它只包含挂载在 `#stage` 内的业务
fragment，所有文案和 inline style 都是冻结内容：

| 区域 | 冻结 DOM / 文案 | 行为绑定 |
| --- | --- | --- |
| 页面头 | `客户管理后台 / 运营 / 企微标签管理` | 无请求 |
| 工具栏 | `同步企微标签`、`新增标签组`、`新增标签` | `tagsPage.sync`、`openCreateGroup`、`openCreateTag` |
| 搜索/容量 | `搜索标签组 / 标签 / tag_id`；`capacity / 1000` 和容量条 | 本地过滤 `tagsPage.search`；无新请求 |
| 左栏 | `标签组`、组数量、每组名称/标签数量、`data-tag-group-card="true"`、`aria-pressed` | `g.pick`，切组并回到第 1 页 |
| 主表 | 当前组名称、标签数量、`每页 20 个`；列为标签名/使用人数/最近同步/操作 | `openEditGroup`、`deleteGroup` |
| 行操作 | `详情`、`复制 tag_id`、`编辑`、`删除` | 详情/编辑 modal；clipboard；删除确认后归档 |
| 分页 | `第 n / m 页，共 k 个`、上一页/下一页（含 disabled 分支） | `tagsPage.prev` / `next` |
| 组 modal | `#fTagGroupName`、创建时 `#fTagFirst`；标题新建标签组/编辑组名 | 空值 toast；创建或 PATCH 更新 |
| 标签 modal | 创建时 `#fTagGroup`；`#fTagName`；标题新建标签/编辑标签 | 创建或 PATCH 更新；创建先读取所属组 |
| 详情 modal | 标签名称、tag_id、所属标签组、使用人数、复制、关闭 | 只读；不做客户查询 |
| modal 通用 | 关闭 `×`、取消、创建/保存、遮罩和固定 inline 样式 | `closeModal` / `save`；交互顺序保持不变 |

控制器行为证据：

- `controller.ts#L262-L266` 的初始状态为 `tagGroupId: 1`、空 modal、空搜索、
  第 1 页；`#L1642-L1692` 实现 modal、输入校验、创建/更新/删除和原样 toast。
- `controller.ts#L2675-L2740` 只在已加载 catalog 内做搜索、组选择、20 条分页、
  capacity 和 modal view model；`copy tag_id` 使用 donor `copyText`。
- `controller.ts#L3694-L3728` 是 `tagsPage` 的完整绑定；同步成功文案固定为
  `标签同步已受理；尚未收到 Provider 同步结果`，失败文案固定为
  `标签同步受理失败`。
- `legacy.ts#L412-L421` 要求 `#tpl` 和 `#stage`，随后调用
  `AdminController.init()`、`mount(stage, tpl.innerHTML, controller)`；tags
  没有额外自定义 section。

## 页面读取与请求 URL / DTO 合同

### 页面启动

正常 `/admin/wecom-tags` 启动时，`readAdminRows('tags')`（`admin.ts#L2015-L2063`）
并发读取：

- `GET /api/admin/wecom/tag-groups`；
- `GET /api/admin/wecom/tags`。

如果 URL 额外带 `?id=<positive integer>`，`readAdminPage` 的 donor 分支
（`admin.ts#L2066-L2113`）还会读取该 group、tag 和
`GET /api/admin/wecom/tags/live/gate`，但列表行的“详情”按钮本身只打开已有
catalog 的 modal，不改变 URL。

### 页面实际使用的写操作

`admin.ts#L1958-L1963` 和 `client.ts#L1017-L1037` 是页面实际调用的稳定边界。
每个请求都经过 donor `apiRequestOptions`（`transport.ts#L36-L54`）：
`credentials: include`，从 `aicrm_csrf` / `csrf_token` cookie 复制
`X-CSRF-Token`，非 2xx 不得被当成成功。`writeMeta()` 生成 body 内的
`idempotency_key`（优先 `crypto.randomUUID()`，否则 `web-${Date.now()}`）。

| UI 动作 | 方法与路径 | donor body | 成功响应 / 页面使用字段 | donor 错误语义 |
| --- | --- | --- | --- | --- |
| 列表读组 | `GET /api/admin/wecom/tag-groups` | 无 | `groups` 或 `items`，每项 `group_id/group_name/sort_order`；映射为 `id/name` | `401/403/503` |
| 列表读标签 | `GET /api/admin/wecom/tags` | 无 | `items` 或 `tags`；`tag_id/group_id/group_name/tag_name/user_count/sort_order`，以及 `count/total_tags/tag_limit/synced_at` | `401/403` |
| 新建组 | `POST /api/admin/wecom/tag-groups` | `group_name`、`first_tag_name`、`idempotency_key`（可选 metadata） | `200`；`group` 和首个 `tag`，`reason=group_created` 或 validated reason | `400/401/403/503` |
| 编辑组名 | `PATCH /api/admin/wecom/tag-groups/{group_id}` | `group_name`、`idempotency_key` | `200`；`group`，`reason=group_updated` | `400/401/403/404/503` |
| 兼容组更新 | `PUT /api/admin/wecom/tag-groups/{group_id}` | 同上 | `200`；生成物保留 PUT 兼容别名，页面实际调用 PATCH | 同上 |
| 删除组 | `DELETE /api/admin/wecom/tag-groups/{group_id}` | `idempotency_key`（可选 metadata） | `200`；`group`，`reason=group_archived` | `400/401/403/404/503` |
| 新建标签 | 先 `GET /api/admin/wecom/tag-groups/{group_id}`，再 `POST /api/admin/wecom/tags` | `group_id`、读回的 `group_name`、`tag_name`、`idempotency_key` | `200`；`tag`，`reason=tag_created` | GET：`401/403/404/503`；POST：`400/401/403/404/503` |
| 编辑标签 | `PATCH /api/admin/wecom/tags/{tag_id}` | `tag_name`、`idempotency_key` | `200`；`tag`，`reason=tag_updated` | `400/401/403/404/503` |
| 兼容标签更新 | `PUT /api/admin/wecom/tags/{tag_id}` | 同上 | `200`；生成物保留 PUT 兼容别名，页面实际调用 PATCH | 同上 |
| 删除标签 | `DELETE /api/admin/wecom/tags/{tag_id}` | `idempotency_key`（可选 metadata） | `200`；`tag`，`reason=tag_archived` | `400/401/403/404/503` |
| 手动同步 | `POST /api/admin/wecom/tags/sync` | `idempotency_key`（可选 `trace_id`） | `202`；`accepted=true`、`state=queued`、receipt/effect 字段；UI 不冒充 Provider 成功 | `400/401/403/409/503` |

### 生成物中的额外路径及排除

`p4-tag-compat.ts`（冻结 hash `ee681...`）还定义了以下协议：

- `GET /admin/wecom-tags`：donor 是跳转到通用 shell 的 `302` 页面入口；v3
  必须保留路径但由 v3 webshell 直接渲染单壳页面，不能复现 donor 第二侧栏。
- `POST /api/admin/wecom/tags/sync-due`：后台 due-sync 入口，属于 PR03 后端
  闭环，但不由页面按钮直接调用。
- `GET /api/admin/wecom/tags/{tag_id}`、`GET /api/admin/wecom/tag-groups/{group_id}`
  和 `GET .../live/gate`：URL `?id` 详情/能力门禁兼容读取。
- `POST /api/admin/wecom/tags/live/mark`、`POST .../live/unmark`：**排除**，
  body 含 `external_userid`，属于客户打标/去标和外部身份，不得因生成物存在而
  开放或接入本页面。
- `POST /api/admin/wecom/tag-effects/{effect_id}/reconcile`：**排除本页面准备**，
  它是 typed EER 人工对账入口，必须由 outbound/EER 按 effect receipt 规范实现，
  不能由 tag 前端自行调用。

### DTO 最小冻结字段

`health.schemas.ts#L11608-L11707` 定义 metadata 和写请求：

- `LegacyTagGroupCreateRequest`：`group_name`、`first_tag_name` 均为 1–200；
  另有可选 `actor`、`idempotency_key`、`trace_id`、`dry_run`。
- `LegacyTagGroupUpdateRequest`：`group_name` 1–200，加同一 metadata。
- `LegacyTagCreateRequest`：`group_id >= 1`、`group_name`、`tag_name` 均 1–200，
  加同一 metadata。
- `LegacyTagUpdateRequest`：`tag_name` 1–200，加同一 metadata。
- `LegacyTagArchiveRequest`：只含同一 metadata。

读取/响应对象（`health.schemas.ts#L11731-L11793`、`#L12304-L12368`）必须保留
donor 字段和安全状态：`ok`、`source_status=local_catalog`、
`route_owner=ai_crm_next`、`real_external_call_executed`、`sync_executed`；
catalog 还保留 `fallback_used`、`fixture_used`、`read_model_status`、
`tag_limit=1000`、`synced_at`。同步 queued acceptance（`#L12417-L12454`）
必须能返回 `receipt_id/event_id/river_job_id/state=queued/effect_id/effect_state`
及 `accept_receipt_id/queue_receipt_id`，但这些字段不能伪装成 Provider receipt。

## v3 单壳挂载边界

当前 v3 shell 已存在 `api.admin_wecom_tags_page → /admin/wecom-tags`（
`internal/webshell/contract.go#L62-L76、#L113-L130`），但
`internal/webshell/handler.go#L215-L219` 仍是“入口已预留”的 placeholder，
`Renderer.RenderAdmin` 也仍会选 `admin_placeholder`。最终 PR03 需要 Terra：

1. 在现有 `admin_base` 中增加与 `RenderMedia` 同等级的、受信任的 tags adapter；
   只放入 donor `tags.html` 经 donor `build.mjs#L23-L41` 变换后的 fragment、
   donor `data-page="tags"`、`#stage`、`#tpl` 和冻结 admin/tokens asset。
2. 不复制 donor `adminShell`、`<html>`、`<body>`、`<aside class="side">`、donor
   nav 或 donor user footer；一级侧边栏只能来自 v3 `admin_base`。
3. donor `admin/main.ts`、`legacy.ts`、`controller.ts` 和 shared API 作为现有
   PR01 冻结 bundle 使用；后端不兼容时增加 HTTP/DTO Adapter，禁止改 donor
   页面代码、请求 URL 或字段名迁就 v3。
4. 静态 hashed asset 只通过 v3 发布清单读取；不把 donor `web/dist` 页面目录
   暴露为可浏览路径，不提交 `web/dist`。

最终渲染形态应当是：

```text
v3 admin_base (唯一 sidebar + session/CSRF gate)
└── main#stage
    └── template#tpl (tags.html 的逐字节 donor 内容，仅做既有 sc-if/sc-for 变换)
        └── frozen admin bundle (data-page=tags)
```

## 当前缺口与合入门禁

`564780c` 之后仍不可宣称 PR03 完成，缺口如下：

- tag-owned additive PostgreSQL migration、Store/UoW、唯一性/并发 CAS、归档引用保护；
- session/管理员权限/CSRF HTTP handler，以上述原样路径和 status/DTO 返回；
- OpenAPI 与生成校验（包括 PUT/PATCH 兼容、sync 202、错误状态）；
- outbound-owned catalog sync Provider read adapter、EER receipt、`outcome_unknown`
  禁止盲重试、人工对账和 `id-dev` Provider disabled 边界；
- `cmd/aicrm` 模块注册、readiness、admin shell route adapter 和发布 asset staging；
- donor API contract/typecheck、PostgreSQL fresh/upgrade、race、权限/CSRF/幂等/CAS、
  1440×900/1280×800 视觉对比，以及 Journey：登录 → 进入标签管理 → 创建组/首标签
  → 编辑/删除 → 同步接受 → 刷新仍存在 → 审计可查。

合入前必须同时满足：

- `scripts/check-pr01-donor-manifest.sh` 的完整 donor web 文件门禁通过；
- 本文闭包脚本在设置 `AICRM_V2_DONOR_ROOT` 后验证 donor HEAD 是冻结 SHA，并验证
  每个 target hash；
- 任何前端业务文件与冻结 hash 不同即停止 PR，不通过加一层 v3 自定义 TS/HTML/CSS
  “修复”；
- 仅 `GET /api/admin/wecom/tags`、`GET .../tag-groups` 等真实响应驱动页面，禁止
  sessionStorage mock、假统计、HTTP 200 占位或只显示“已排队”；
- Provider 写/读执行和回执边界由 outbound/EER 完成，标签页面只能显示明确的
  queued/attempted/executed/outcome_unknown/reconciled 状态。

## 审计复现

本审计在以下只读树完成：

- donor：`/tmp/aicrm-v2-audit.yN3jmr`，`git rev-parse HEAD` =
  `6bfbe5816bb89913c70adaca87d6a486260e016e`；
- target observation：`/private/tmp/aicrm-v3-migration.jDLgdz/media-integration`，
  `git rev-parse HEAD` = `f250aa3313df2f87177e2e18ac98cb7b57989eb5`，且 PR01
  `6ac3aba` 是其祖先；
- 当前工作树：`codex/import-tags-audit`，只新增本审计文档和校验脚本。

测试结果：直接页面、共享运行时、API/生成物、构建输入共 25 个路径均为
`IDENTICAL`；donor `tags.html` 未发现 nested shell、`external_userid`、
`customer_id` 或 OneID 词；工作树无未提交变更（提交审计文档后除外）。
