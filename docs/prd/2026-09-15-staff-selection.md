# V3 共享员工选择：群运营、渠道客服与负责人迁移

## 用户可观察目标

管理员在三个已经上线的工作流中从各自被授权的员工目录选择人员，搜索、取消、重开、刷新失败和权限失效都不会静默改变草稿或原有选择：

| 实际入口 | 使用的受权目录 | 选择结果 | 业务命令边界 |
| --- | --- | --- | --- |
| `web/v3/groupOpsStandard.js` 的计划负责人、新建负责人和群主筛选 | `GET /api/admin/common/operation-members?scope=group_ops&page_size=100`，仅服务器承认的 `group_ops` 范围 | 保留本地 `staff_id`，显示可信 `user_id` / 名称 | 仅编辑群运营表单；既有计划 CAS 保存仍由 GroupOps Host 负责 |
| `web/v3/channelAdmissionHost.ts` 的渠道客服 | `GET /api/admin/common/operation-members?scope=channel_code&page_size=100`，仅服务器承认的渠道客服本地投影 | 保留渠道配置所需的 `staff_id` | 仅编辑已有渠道草稿；既有 Catalog 保存和读取回执不变 |
| `internal/webshell/static_src/admin_console/owner_handoff_host.ts` 的原/目标负责人 | `GET /api/admin/common/operation-members?scope=owner_migration&q=...&page_size<=100` 与本页可信 context 初始投影 | 保留受信 `staff_id` 与 `user_id` 映射 | 仅编辑负责人迁移表单；已有 preview/确认/企微转接流程不变 |

负责人迁移的目录端点没有 offset 或 `has_more`，因此页面不把“本次前 100 项或搜索结果未出现”解释为失效。初选映射必须保留并可检查；源负责人可为停用员工，目标负责人必须为在职员工。目录刷新出现新员工后，选择和后续 preview 都必须使用更新后的 `staff_id ↔ user_id` 映射。

群运营兼容字段名虽为 `owner_userid`，其值仍是 GroupOps Owner 的本地 `staff_id`。创建、计划编辑和群筛选只以该本地 ID 查找或提交；显示的企微 UserID 只来自受权目录，绝不以数值 ID 填充。目录行缺少可信 `user_id` 时保留原选择并禁止确认，避免数值企微 UserID 与另一位员工的 `staff_id` 碰撞而错选。

## 分类与架构边界

- **OneID：不涉及。** 选择器只呈现受权的内部员工投影；不接受、创建、解析或合并客户身份。
- **持久化：选择器无状态。** 它维护短暂 UI 草稿；调用方的 GroupOps CAS、Channel Catalog 写入和 Owner Handoff preview/confirm 各自保持所属领域事务。
- **Provider / External Effects：选择器不涉及。** GroupOps 的员工刷新是既有 Provider 读取与本地目录投影；渠道目录与 Owner Handoff 读取由各自现有 Owner 端点负责。选择、搜索、确认或取消不调用 Provider 写、outbound、队列、预览确认或转接命令。
- **无重复内核：** 不增加身份匹配、表写、Provider Writer、队列、重试或对账。分页/搜索数据由调用方的既有受权 loader 提供。

## 组件与调用路径

组件索引命中冻结 `operation_member_picker_dd8d60d.js` 与 `web/v3/standardComponentsHost.ts`。它目前将刷新动作默认改写为 `group_ops`，不能作为跨页面通用范围。新增 V3-owned `web/v3/shared/ui/staffPickerAdapter.ts`，复用 `SelectionSession`、`selectionDialog.ts`、`selectionDialog.css` 的 IME、焦点、取消、草稿、失权和提交合同。业务 loader、可选范围、回调和读回仍由三个调用方所有。

`standardComponentsHost.ts` 继续加载冻结组件以兼容尚未迁移页面，但不再为任意刷新或 fetch 注入默认 `group_ops`。它只安装独立 `window.AICRMStaffPicker` API；实际 GroupOps、Channel 和 Owner Handoff 入口显式提供 loader 与回调。Owner Handoff 由其 V3 source 生成静态 host，不修改冻结 donor HTML 或 frozen picker。

GitHub 已核查的同仓参考：

- [PR #206](https://github.com/qianlan33333-png/AI-CRM-v3/pull/206) 保留本地 assignment ID，同时显示可信昵称。
- [PR #242](https://github.com/qianlan33333-png/AI-CRM-v3/pull/242) 将共享成员选择器语义限定到调用范围。
- [PR #300](https://github.com/qianlan33333-png/AI-CRM-v3/pull/300) 的 GroupOps 选择器已经验证共享会话的草稿、失败保留、IME、焦点和 Host 读回边界；员工选择不会复制其群聊领域逻辑。

Product Design 路由：已读取 `product-design:index` 和 `audit`，并执行 user-context preflight；没有保存的 Product Design context。此项采用已验收 GroupOps / Material 对话框为现有视觉目标，在真实 Chromium Journey 中做流程和布局审查，不生成平行页面或视觉方案。

## 行为合同

1. 同一入口同时只能打开一个选择器；加载、刷新、确认中不可重复提交。
2. `compositionstart` / `compositionend`、`keyCode 229` 的 IME 输入不触发 Enter 搜索或 Escape 关闭；普通 Escape 和取消恢复调用前 committed 草稿，并把焦点还给触发控件。
3. 每次 loader 传递 `AbortSignal`；旧成功、旧 401/403、取消后的旧失败都不得覆盖当前 session。401/403 锁定当前会话并保留 draft 和 committed 供查看或取消；锁定后不再允许移除或确认。
4. 初选、目录刷新新项目、已删除/无权项目都显示原因；目录不完整时不得静默删除、替换或把未加载记录判为失效。真实目录行若缺少可信 `user_id`，也必须明确不可确认；只有调用方明确声明可仅用本地 ID 时才能另行放宽。
5. GroupOps、渠道和 Owner Handoff loader 的范围必须按其现有端点固定；三者都只显示其端点可证明的每次前 100 / 查询结果，并明确提示可搜索定位，不把缺失当全目录结论。群运营的计划负责人和新建负责人必须保留至少一项；群主/管理员筛选可在选择器中清空，并立刻按无群主条件重新读取群列表。
6. 确认先调用调用方 `onCommit`，仅其成功后提交并关闭；同步 throw / Promise rejection 保留 draft 与原 committed，且不自动重放业务命令。
7. 渠道确认只写现有表单草稿；群运营确认只写现有表单控件；负责人迁移确认只写现有隐藏 ID/可见标签。后续业务保存、preview、confirm 和 Provider read/write 只走已有按钮与端点。

## 计划改动

- 新增 `web/v3/shared/ui/staffPickerAdapter.ts` 与针对 session、IME、异步乱序、分页、失权和回调失败的测试。
- 更新 `web/v3/standardComponentsHost.ts`，安装独立 V3 staff API，删除默认 GroupOps 刷新改写，同时保持 frozen global 可供未迁移调用使用。
- 更新 `web/v3/groupOpsStandard.js`、`web/v3/channelAdmissionHost.ts`、`internal/webshell/static_src/admin_console/owner_handoff_host.ts`，仅接管上述真实按钮；重新生成所需 V3 / owner static host 资产。
- 更新对应 DOM、SSR 和 PostgreSQL Chromium Journey，注册新增测试到 canonical consumer，并更新组件索引和 release staging 闭包。

## 验收证据

- GroupOps：单选负责人、搜索、取消/重开、目录失败和重新读取后仅通过既有计划保存产生读回；数值企微 UserID 与另一 `staff_id` 碰撞时，创建、计划和群筛选都保持本地 ID 归属。
- GroupOps：负责人 A 的名下群聊读取或“刷新名下群聊”命令在 B 已被选中、或详情发生权威重读后才返回时，不得改写 B 的群目录、提示或加载状态。
- Channel：渠道受权客服多选、刷新后新增人员仍能留在草稿，取消不改草稿，原 Catalog 保存后读回。
- Owner Handoff：停用 source / 在职 target 规则、刷新后新增员工选中并进入真实 preview，权限/网络失败保留原映射，不能把前 100 项以外的初选标失效。
- 三条真实路由在 PostgreSQL + 认证 Chromium 下执行，含 IME、焦点返还、360/420/1280/1440 视口、CSS HTTP/MIME 和 staged release 读取。
