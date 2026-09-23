# 负责人迁移：冻结旧页复用记录

## 边界分类

本能力涉及客户归属和企微外部客户身份。Excel 的 `external_userid` 只在服务端用受信任的 `wecom-corp:<CorpID>` scope 解析既有 OneID；未解析到的行不建客、不合并。确认后 Customer 在同一个 PostgreSQL Unit of Work 中落预览、批次、行、审计和 River 持久任务；企微写入经 outbound / External Effects，页面不直接调用 Provider。

## 冻结来源与运行路径

- 来源仓库：`/private/tmp/aicrm-release-fallback-donors/sidebar`，提交 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`。
- 冻结页面：`aicrm_next/app/admin_console/templates/admin_console/owner_migration.html`，Git blob `7d8e99dcb273047faa1ce44b1d52b8239edbf397`；运行资源为 `internal/webshell/static/admin_console/owner_migration_dd8d60d.html`，SHA-256 `7491be60ed89b84fcbe4b9b37f27f5a95dd270cd4915d459449a74349f74138f`。
- 冻结共享员工选择器：`aicrm_next/app/admin_console/static/admin_console/operation_member_picker.js`；运行资源为 `internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js`，SHA-256 `bd84ce78ccb834f170548dea76cb99f6434978bc21211a9ec843dd2bf7ebabea`。
- `/admin/owner-migration` 只渲染 V3 shell。`owner_handoff_host.js` 获取并克隆冻结页面的 style 和 `[data-owner-migration-page]`，再动态载入冻结共享选择器。没有复制旧目录，也没有修改冻结供体或其 manifest。

## 必要适配及旧契约保留

- Host 在克隆后、挂载前清掉未渲染的 Jinja token，再从已认证 V3 context 写入操作人、默认欢迎语和默认开启的企微开关。浏览器旅程检查渲染 DOM 与真实 Provider payload 都没有 `{{` 或 `{%`。
- 共享 `OperationMemberPicker` 按原接口请求 `/api/admin/common/operation-members?scope=owner_migration`。该 Host 适配只读 Access 投影：原负责人请求 `include_inactive=true`，目标负责人请求 `false`；选择结果必须匹配该投影，绝不自动选中首个员工。
- 旧五列 `external_userid/是否迁移/当前负责人userid/客户备注名/备注`、下载模板、导入统计、行号、重复标记、非法迁移标记、文件跳过和源负责人不符状态均保留。管理员预览和 XLSX 导出显示现有授权的 `external_userid`、客户名称和当前负责人 userid；它们不进入结构化日志。
- `当前负责人userid` 与选中源负责人不符的已标记迁移行仍显示为 `not_under_source_owner`，但不传入 V3 可确认预览，避免 V3 无此旧文件字段时把该行执行。其余已解析且标记“是”的行经 scoped OneID 解析后进入预览。
- Excel 的真实下载、阻止行 XLSX、批次结果 XLSX 和显式企微结果读取均完整可用。结果 XLSX 保留行号、`external_userid`、客户备注名、当前负责人 userid、备注、迁移状态和企微状态；读取企微结果只读已有转接，不创建新转接。
- 经供体 `application.py:637-641` 核对，旧扩展名 `.xls` 的实际路由为：PK 签名按 XLSX 解析，其他内容按 UTF-8 CSV 解析；V3 保留该兼容路径，继续执行 1 MiB、ZIP 展开和公式限制。二进制 BIFF 从未被旧代码支持，V3 明确拒绝并提示另存为 CSV 或 XLSX。其余旧页面字段和顺序由冻结页面提供。

## 全量候选规则

全量本地迁移先读取 Customer-owned local owner。存在本地归属时它优先于企微观察；只有本地归属为所选源负责人者进入候选。没有本地归属的客户，才从已完成、同 corp scope、无冲突的 WeCom 主负责人事实中补入。该读取经 WeCom 稳定 Port 完成，不修改观察，也不自动建立 local owner。
