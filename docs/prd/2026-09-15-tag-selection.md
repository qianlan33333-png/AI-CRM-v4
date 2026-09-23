# V3 标签选择器：本地目录、表单草稿与实际调用点

## 目标

把客户标签变更草稿、商品购买后标签和渠道入渠标签迁移到同一套 V3 `SelectionSession`／dialog。确认只更新每个真实页面已有的表单草稿；选择器不创建客户标签命令、不发送、不修改标签目录，也不触发 Provider 同步。

## 开发前分类

- **OneID：不涉及。** 客户页只保留现有 Customer 命令的本地 `tag_id` 草稿，不读取、解析或创建客户身份。
- **持久化：选择器自身无持久化。** 商品和渠道仅在各自原有“保存”动作中持久化既有配置；客户页只有管理员随后提交既有“预览并确认标签变更”表单才会走原 Customer 命令。选择器不访问业务表、不执行命令。
- **External Effects：选择器不涉及，客户原命令涉及。** `GET /api/admin/wecom/tags` 是 Tag Owner 的本地目录读取。选择器不触发同步、Provider 写入或发送；客户原命令的 preview/accept/outbound 收据流程保持 Customer Owner 原有边界，浏览器验收不提交该命令。

## 目录与键

标签记录必须保留 `{ source: 'local_tag_catalog', group_id, tag_id }`。`provider_tag_id` 只能显示为只读目录事实，不能作为选择、保存或客户筛选键。Tag Owner 在一个 local source 内保证 `tag_id` 唯一，因此 session 用 `source + tag_id` 作稳定草稿键；完整目录回读会更新同一记录的 group/name。标签移组不会制造第二个选择，来源不匹配或无效 ID 是显式配置错误，不能静默重写为当前来源。

现有 Tag Owner read API 是有界的本地快照（域上限 1000），没有截断的远端分页 API。调用方在打开或显式刷新时读取一次 `/api/admin/wecom/tags`；共享 adapter 仅在 `read_model_status=ready`、`count=total_tags=items.length`、上限和 group/tag 关联都有效时采纳该完整本地快照，再在本地做 query、分组筛选和分页。刷新失败保留上一份目录和当前草稿；首次读取失败也保留初始已选项并明确“目录状态待确认”，不将其删除或替换。完整快照会补全当前搜索或第 20 页之外的初选项；若它确实找不到该 ID，则显式标为不可用、可移除且阻止确认保留。`AbortSignal`／epoch 防止旧失败覆盖新会话；403 仅在当前请求有效时锁定编辑并保留可见草稿。

## 共享合同

`tagPickerAdapter` 只拥有目录展示、单／多选、分组筛选、搜索、分页、IME、焦点、readonly 和取消。调用方显式给出 `source`、初始记录（可为待目录解析的既有 `tag_id`）、选择模式／上限、受权 loader 和 `onCommit`。共享 `SelectionSession.reconcile(items)` 只补全已知 draft/committed 记录的显示值，不改草稿、已提交集合或当前页；已移除项不会因目录刷新重新进入 draft。

`onCommit` 只返回完整本地选择；调用方成功接受后才提交会话并关闭。回调异常保留草稿且不自动重放；选择器也不宣称能回滚调用方的业务效果。取消不调用提交回调。当前页行提供可见选中态和 `aria-pressed`；目录行按分组段落显示，分组选择框和搜索在窄屏仍可用。

## 实际调用点

| 页面／Host | 原入口 | 新调用方式 | 回读和边界 |
| --- | --- | --- | --- |
| 客户列表与详情 `internal/webshell/static/admin_console/admin_customers.js` | 既有 `add_tag_ids`／`remove_tag_ids` 多选草稿与“预览并确认标签变更”按钮 | 多选 V3 tag dialog；确认只更新原 select 草稿，新增目录项会 upsert 为 option，移组/改名更新 option 文本 | 列表与详情都支持取消重开；目录缺失初选仍回显且可移除。选择器不调用 preview/accept，原 Customer 命令、receipt 和 Provider 边界不变。 |
| 商品编辑 `web/v3/productAdapter.ts` | 购买后企微标签开关和隐藏 JSON | 多选 V3 tag dialog；确认只更新该隐藏 JSON 草稿 | 原商品保存负责持久化，选择器不创建客户标签或调用 Provider。 |
| 渠道编辑 `web/v3/channelAdmissionHost.ts` | 冻结渠道表单的入渠标签按钮 | Host capture 仅接管该按钮到单选 V3 tag dialog；确认只更新 `entry_tag_id/name/group_name` 草稿和既有摘要 pill | 原渠道保存负责持久化；目录失败保留原入渠标签，不开放 manual tag，也不退回冻结 picker。 |

`AICRMWeComTagPicker` 仍保持冻结全局兼容；新增 `AICRMTagPicker` 只由上述 V3 调用点显式使用。`standardComponentsHost.ts` 只加载／暴露共享 V3 adapter，不默认目录范围或标签写入。客户 SSR shell 通过 manifest 校验的 `selectionDialogStyles` 加载 V3 scoped 样式；不能依赖生成 `customers.html`，因为它不是 `/admin/customers` 的真实路由。

## 前端一致性与 Product Design 路由

- 复用：`web/v3/shared/ui/selectionSession.ts`、`selectionDialog.ts`、`selectionDialog.css`；不复制冻结 `wecom_tag_picker`。
- 实际装配：`scripts/build-v3-host-adapters.mjs` → `standardComponentsHost`／各领域 Host → 管理端现有单壳或渠道独立 Host；`internal/webshell/presentation.go`／`templates/admin_base.html` 为真实 Customer SSR shell 装配 manifest `selectionDialogStyles`，`internal/product/ui.go` 和 `internal/channel/ui.go` 装配同一 entry。稳定 standard Host 会把其 chunk 相对路径重写到 `/assets/chunks`，并在 release manifest 保留递归 closure，避免 release stage 丢失依赖。
- Product Design：已按 `product-design:index` 路由并运行 user-context preflight；当前没有保存的视觉上下文。以现有三处页面和 V3 dialog 为视觉目标，实际 Chromium 验收记录布局、焦点、IME、失败和确认可达性。

## GitHub 与仓内参考

- GitHub 已核验 [PR #206](https://github.com/qianlan33333-png/AI-CRM-v3/pull/206)：非数字企微 userid 不能被当作本地 staff_id；本次沿用其“显示身份与本地保存 ID 分离”的边界，不复制冻结组件。
- 标签目录 Owner 合同：`internal/tag/http/handler.go` 的 `GET /api/admin/wecom/tags`，返回 local `tag_id`、`group_id`、名称和只读 Provider 绑定。
- 共享 dialog 合同：已冻结 parent `096779ce910c58ce99b8ed9ee1282e774fde3308`；群聊栈 `b5cc35814b6b2c29899ae727a9c9c646091faa9c`。

## 验收

- 单／多选、过限、取消重开、搜索、分组筛选、分页、IME Enter／Escape、焦点回归、当前 403、旧 403 晚到、刷新失败保留、已选目录缺失、标签移组不重复和 `aria-pressed`。
- 客户列表和详情只改变原 `add_tag_ids`／`remove_tag_ids` 草稿，不发 preview/accept；商品和渠道确认只更新可保存草稿，分别经其已有保存路径读回。
- Tag Owner HTTP／PostgreSQL 目录键验证；真实 Chromium 覆盖客户列表/详情、商品和渠道的 V3 调用点，以及 360／420／1280／1440 布局。
- ES2020 typecheck、相关 Host／adapter 测试、串行 `bash scripts/run-donor-view-consumers.sh check` 与完整 CI。
