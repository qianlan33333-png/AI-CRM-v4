# 客户 SSR 档案：可观察资料与记录区

## 目标与边界

将真实管理端 `/admin/customers/{id}` 的现有 SSR 档案从四个身份格和散文式摘要，整理为可扫描的身份摘要、标签草稿区、分区记录表和明确状态。列表仍使用同一页的真实筛选和分页契约。

- **OneID：涉及读取，不新增身份逻辑。** 页面继续只显示 Customer Owner 已返回的 canonical Customer ID、OneID 和安全身份摘要；不匹配、创建或合并客户。
- **持久化／外部效果：本 UI 不新增。** 标签选择只编辑既有表单草稿，保留既有 preview-and-confirm 标签命令、幂等和结果刷新。手机号继续通过既有授权 POST 查询，30 秒后清除。不得新增 Provider 调用、队列或命令。
- **数据边界：** 只读取当前 `/360` 已批准分区和已有标签目录。某分区失败、字段缺失或状态非 ready 时显示该分区不可用或待确认，不能用 `0`、空记录或虚构权益／资产补位。

## 真实入口与复用

| 项目 | 真实路径 | 本次处理 |
| --- | --- | --- |
| SSR 路由与页面 | `internal/webshell/templates/admin_customers.html` → `admin_customers.js` → `/api/admin/customers/{id}/360` | 在同一管理端单壳中调整档案结构和可读记录渲染。 |
| 基础样式 | `internal/webshell/static/admin_console/admin_console.css` 的 `--brand`、`--text`、`--bg`、`--panel`、`--line`、`--radius-*` 与既有 admin card/table/state | 扩展客户页面 scoped class，不另建 token 或页面壳。 |
| 标签草稿 | PR #305 的 `AICRMTagPicker` / `SelectionSession` / dialog | 保留现有 Customer tag form 的 preview-and-confirm 边界。整合 PR #306 的 `readyFor(['tags'])` 单飞、失败重试；不能退回全量 standard-components 加载或旧冻结 picker。 |
| 可观察详情 | 既有 `/360` 的 `profile`、`identity_summary`、`order_summary`、`questionnaire_summary`、`risk`、`recent_touchpoints` | 使用已有字段的分区卡和记录表；订单、问卷、触点仅呈现服务端给出的 recent 记录。 |

Product Design 路由：已读取 `product-design:index`。这是对已有 SSR 页面和已选管理端视觉的实现，不需要新视觉探索或原型。

## 交互与验收

1. 档案顶部清晰显示姓名、客户状态、OneID、掩码手机号与现有授权查询；标签单独成区，草稿、取消、预览确认和命令结果不改变。
2. 客户根资料、订单、问卷、风险和最近触点使用统一 card／table／state。记录表保持已返回的编号、状态和时间，不把缺失字段转换为零。
3. 某个 360 分区 degraded、failed 或字段畸形时，其余 ready 分区继续显示；受影响分区显示可理解的不可用／待确认提示。
4. 列表延续既有关键词、手机号、客户状态、分页和选择语义；不扩大查询参数或后台规则。
5. 在 1280、1440、360 视口验证实际 SSR route，包含授权手机号 30 秒清除、标签草稿确认、分区失败隔离和无横向溢出。

## GitHub 参考

- PR #305：真实 V3 Tag Picker 接入 Customer form，已冻结头 `2d493382c85ff9b0bd4ba811a2934d7e6288e54e`。
- PR #306：Customer 页面标签 capability 按需加载、单飞与本地重试，开放头 `31c36564f65a7190eb9f1f9d3ea3d8a9c94bf774`；整合时保留其能力边界和重试，不覆盖为旧 `ready()` 全量加载。
