# 运营闭环 Excel 批次 API 契约（T1）

状态：实现契约；浏览器页面、Excel 组件和 Go Host 共用。所有 `/api/admin` 路由要求既有管理员会话；写请求还要求既有 CSRF 校验与 `Idempotency-Key`。不提供 service-token、匿名或跨站写入口。

## 架构分类

```text
OneID: involved — 上传只保存 UnionID + Open Platform scope；执行时由既有 Identity Port 解析，不建客、不猜测。
Persistence: local transaction + existing Provider write/external effect — 批次/版本/关联/审核审计/幂等收据与既有 AI Assistant 批准意图在同一 PostgreSQL UoW；企微任务继续由既有 outbound + External Effects 创建。
```

`operation_cycle_strategies.strategy_key` 是“长期计划”稳定键。一个 Excel 批次必须关联一个已存在且当前管理员有权读取的长期计划；没有用于从页面创建长期计划的 API。关联由 `aiassistant` 表拥有，`operationcycle` 只经受控读取 Port 验证计划存在，双方不跨域写表。

## 公共约定

- 写请求必须有 8–200 字符的 `Idempotency-Key`。同一操作者、同一路由命令、同键同请求体返回原响应；同键不同请求体为 `409 {"error":"idempotency_conflict"}`。
- 批次级命令（替换、换封面、预览、提交）使用审核计划的 `expected_version`；单行 `PATCH` 使用该行的 `expected_version`。过期版本为 `409 {"error":"batch_request_failed"}`。行内容、分层、排除或封面改变都会使整个计划的预览摘要失效；已批准行回到待审核，必须重新预览后提交。
- 读取为 `200`；新批次为 `201`；输入或状态不合法为 `400 {"error":"invalid_input","message":"...","row_errors":[...]}`；未认证 `401`，无权限 `403`，不存在 `404`，组件未启用 `503`。重复文件为结构化 `409 duplicate_file`，同键请求体漂移为 `409 idempotency_conflict`，不会被伪装成 `503`。
- 行读取始终分页：查询参数 `cursor`（首请求为空）和 `limit`（默认 **50**、最大 **50**）；响应的 `next_cursor` 为空才表示当前内容版本读取完毕。客户端在全部分页完成前不得预览或提交，避免静默遗漏大批次。
- 所有 JSON 字段为 `snake_case`，时间为 RFC 3339 UTC；客户端不得依赖未在此文档列出的响应字段。
- 所有上传仍受现有安全限制：Excel 原始文件最多 **8 MiB**，封面仅 PNG/JPEG、最多 **2 MiB**、解码像素不超过 40,000,000；最大行数沿用 `aiassistant.MaxRecipients` 即 **5,000**。文件过大、解析失败或超过行数均为 `400`。

## 批次和版本 JSON

```json
{
  "batch": {
    "id": 918,
    "batch_key": "xb_...",
    "operation_cycle_strategy_key": "weekly.review",
    "state": "pending_review",
    "version": 3,
    "file_digest": "sha256:...",
    "cover_digest": "sha256:...",
    "current_content_version": 2,
    "source_kind": "excel_batch",
    "segment_source": "excel",
    "created_at": "2026-09-09T...Z",
    "summary": {"total_rows": 120, "excluded_rows": 2, "empty_title_rows": 1, "expected_tasks": 117}
  }
}
```

批次 ID **就是现有 AI Assistant `plan_id`**；不增加第二个业务 ID。`batch_key` 是文件上传的不可变幂等/溯源键，长期计划键只是该审核计划的受控关联。`state` 是 `pending_review | partially_approved | approved | dispatching | needs_attention | completed | completed_with_failures | rejected`。提交前只允许 `pending_review` 或 `partially_approved`；`completed_with_failures` 是明确终态，逐行 `failure_reason` 说明失败事实。`segment_source` 对受控新 Excel 为 `excel`。旧 AI Assistant Excel 的持久来源为 `legacy_snapshot`，即使随后人工关联长期计划也不改写；组件报告将其映射为 `unavailable` 且只给总体统计，绝不伪造成 Excel 分层。

行 JSON：

```json
{
  "id": 33,
  "version": 4,
  "unionid": "保留原字符串",
  "sender_userid": "sender_from_excel",
  "text": "话术",
  "path": "pages/...",
  "title": "可空",
  "card": {"appid":"固定配置 AppID","path":"pages/...","title":"可空","cover_digest":"sha256:..."},
  "segment": "A",
  "excluded": false,
  "review_state": "pending_review",
  "delivery_state": "pending_submission",
  "failure_reason": "",
  "sent_at": null
}
```

`segment` 只能是 `A|B|C|D|""`。`delivery_state` 仅使用底层事实：`pending_submission`、`task_created_waiting_employee`、`delivery_proven`、`final_failed`、`outcome_unknown`；`excluded` 独立显示。空 `title` 可以上传和审核，提交前计入 `empty_title_rows`，执行时为明确失败且不调用企微创建任务。

## 长期计划下的路由

### `GET /api/admin/operation-batches/strategies/{strategy_key}`

返回策略键和当前管理员可见的批次列表，按 `created_at` / 批次序号倒序；回执或报告刷新不得改变历史选择顺序。默认当前批次是最近一个。该接口只读，页面一级列表仍用现有 `GET /api/admin/operation-cycles/strategies` 获取计划标题。

```json
{"strategy":{"strategy_key":"weekly.review"},"items":[{"id":918,"batch_key":"xb_...","state":"pending_review","version":3,"summary":{}}]}
```

### `POST /api/admin/operation-batches/strategies/{strategy_key}/imports`

`Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`，原始 Excel body；`?new=1` 是同文件明确新建批次的唯一方式。无 `new=1` 的同文件命中已有批次时返回 `409 duplicate_file` 及已有批次所属计划，不覆盖或跳转。

```json
{"error":"duplicate_file","existing_batch":{"id":918,"operation_cycle_strategy_key":"weekly.review"}}
```

Excel 按列名、所有单元格字符串读取，列顺序无关：`unionid`、`话术`、`小程序 path`、`发送人 userid` 必填且不可空；`标题` 列必需但单元格可空；可选 `分层` 为 `A/B/C/D` 或空。重复 UnionID 或非法分层返回 `row_errors:[{"row":7,"field":"分层","code":"invalid_segment"}]`。成功创建待审核批次，**绝不批准或创建发送任务**，返回 `201` 的批次；逐行预览通过随后分页的详情读取。

### `GET /api/admin/operation-batches/{batch_id}`

返回 `{batch,rows,next_cursor}`，当前行受公共分页约定限制。`GET /api/admin/operation-batches/{batch_id}/versions` 返回只读上传内容版本摘要；`GET /api/admin/operation-batches/{batch_id}/versions/{content_version}` 返回 `{batch_id,content_version,summary,rows,next_cursor,read_only:true}`。每个历史版本固定自己的行内容和当时有效的封面记录；历史读取不能接受写命令。

### `PUT /api/admin/operation-batches/{batch_id}/import`

与创建上传的原始 Excel body/校验相同，并以查询参数提供 `expected_version`。仅未提交批次可替换：保留 `batch_id`、`batch_key`、已上传 `cover_digest`，创建可只读查看的旧内容版本；新行不继承批准或排除状态并回到待审核。已提交为 `409 batch_submitted`。

### `POST /api/admin/operation-batches/{batch_id}/cover`

原始 PNG/JPEG body，查询参数 `expected_version`。成功覆盖本批次每行卡片的封面，变更版本并取消旧预览/批准；返回批次和 `cover_digest`。`GET /api/admin/operation-batches/covers/{digest}` 返回私有封面字节。

### `PATCH /api/admin/operation-batches/{batch_id}/rows/{row_id}`

```json
{"expected_version":4,"text":"...","path":"pages/...","title":"","segment":"B","excluded":false}
```

除 `unionid`、`sender_userid`、`appid` 外，内容和 `segment` 可修改；`excluded` 可切换。话术或卡片字段改变时追加或重挂该行的不可变内容版本；仅分层或排除更新该行的批次属性。两类修改都记录审计、取消当前审核/计划预览；已提交批次拒绝。

### `POST /api/admin/operation-batches/{batch_id}/preview-approval`

```json
{"expected_version":3}
```

返回冻结候选摘要及 `preview_digest`：`{"preview_digest":"sha256:...","summary":{"total_rows":120,"excluded_rows":2,"empty_title_rows":1,"expected_tasks":117}}`。无封面为 `400 cover_required`。

### `POST /api/admin/operation-batches/{batch_id}/approve`

```json
{"expected_version":3,"preview_digest":"sha256:..."}
```

一次命令完成既有 AI Assistant 审核及创建现有 outbound/External Effects 群发任务的意图；同一 PostgreSQL UoW 提交批次关联、最终内容/封面/分层冻结、审核、既有审计/收据、发送意图。不得由上传字段伪造批准状态。返回最终批次；`task_created_waiting_employee` 不是送达成功。

### `GET /api/admin/operation-batches/{batch_id}/receipts`

返回 `{batch_id,content_version,items,next_cursor}`，使用同一分页约定。行的 `delivery_state` 是稳定 UI 投影：`pending_submission`、`task_created_waiting_employee`、`delivery_proven`、`final_failed`、`outcome_unknown`；不把 queued/accepted 视为成功。

### `GET /api/admin/operation-batches/{batch_id}/report`

直接透传 Excel 组件的单一报告协议，不二次映射：报告以每人实际 `sent_at` 计算 12/24/48 小时，未满窗口单列。数据缺失使对应打开率为 `null`；页面不得将其显示成 0。每个窗口在 `windows["12"|"24"|"48"]` 下有 `overall`、`groups` 和 `has_segments`，紧凑总体投影在 `overall["12"|"24"|"48"]`。`GET /api/admin/operation-batches/{batch_id}/report.csv` 下载逐人数据。

```json
{
  "plan_id":918,
  "updated_at":"2026-09-09T00:00:00Z",
  "segment_source":"excel",
  "has_segments":true,
  "overall":{"12":{"sent":80,"observing":35,"matured":45,"opened":9,"unavailable":2,"open_rate":null}},
  "windows":{"12":{"overall":{"sent":80,"observing":35,"matured":45,"opened":9,"unavailable":2,"open_rate":null},"groups":{"A":{"sent":30,"observing":10,"matured":20,"opened":4,"unavailable":0,"open_rate":0.2}},"has_segments":true}}
}
```

UI 必须把报告、回执和内容都绑定到当前选择的 `batch_id`，不得与另一历史批次混合。

## 受控旧批次关联

`GET /api/admin/operation-batches/legacy` 只返回可发现但未关联的 `source_kind=excel_batch` 审核计划：`{"items":[{"id":917,"name":"...","state":"...","created_at":"...","linkable":true}]}`；不会提供默认长期计划或写动作。

仅管理员受保护命令可执行：`POST /api/admin/operation-batches/legacy/{plan_id}/link`，JSON `{"strategy_key":"...","expected_version":N}`。同一 AI Assistant plan 只能关联一次；验证长期计划存在、权限、来源为 Excel、幂等键和审计。无页面通用迁移入口；未关联旧批次仍保留只读发现性。

## 兼容入口

`POST /api/admin/operation-batches/imports` 已禁写并返回 `410 legacy_write_disabled`；不会再创建未关联批次。既存未关联 Excel 仍可由 `GET /legacy` 只读发现，并须使用受控 `legacy/{plan_id}/link` 明确关联。所有新写入一律使用上述 `strategies/{strategy_key}` 路由。
