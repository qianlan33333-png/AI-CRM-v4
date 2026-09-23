# 群聊共享选择器与群运营计划接入

## 目标

在群运营计划详情的“选择群”入口接入 V3 共享选择器。运营人员可按群聊目录选择、取消或移除多个目标群；提交时由既有 GroupOps 计划群资产命令保存完整差异，重新打开时以服务端绑定结果回显。

## 边界与分类

- **OneID：不涉及。** 选择值是 GroupOps Owner 的不透明 `chat_reference`，不读取、解析或创建客户、外部身份。
- **持久化：选择器不涉及。** 它只保留页面会话草稿；确认回调由现有 GroupOps `plans/{id}/groups` 命令按既有 revision 写计划群资产。没有新表、队列或事务。
- **Provider 与外部效果：不涉及。** 目录由已授权的 GroupOps 本地目录投影读取；本能力不触发同步、入群、消息发送、素材上传或 Provider 调用。

## 交互合同

调用方显式提供 `source`、`scope`、带 query/cursor/signal 的分页读、初始完整 `selectedRecords`，以及一次完整结果的 `onCommit`。`onCommit` 用于原子应用完整的本地选择；选择器只会在该回调成功后提交会话并关闭。调用方拥有业务持久化的幂等和失败恢复责任；回调抛错或拒绝时，选择器保留草稿和原提交值、不自动重放，也不宣称已回滚调用方可能已发生的业务效果。选择器支持多选、readonly、不可选原因、搜索按钮或非 IME Enter、分页选中保持、刷新失败保留草稿、乱序读保护、取消无写入、焦点返回和键盘操作。已绑定但目录缺失的群不静默丢弃，显示为不可用的历史绑定。

## 本次业务判断

- 打开选择器只读取计划详情和已绑定的 `chat_reference`，不为补显示名而预取整个群目录。首次目录读、搜索和分页都走 Owner 的本地受权 read port，按 `q`、`limit` 和 `offset` 查询；浏览器不再只过滤首个 50 条结果。
- 初始绑定永远以计划详情为准。目录读取失败、搜索不命中或绑定群改属其他负责人时，绑定记录仍可查看；改属时明确显示“当前负责人不可管理此群”，不改变或混用 `chat_reference`、`rawchat_id`、群邀请素材 ID。
- 一次“确认选择”固定每个新增／移除步骤的 idempotency key。重试先读回计划绑定，并只发送尚未达到原意图的差异；不能用全量覆盖抵消其他操作者刚新增的绑定。网络结果未知时沿用原 key 查询／重试，不生成新 key。部分成功会保留草稿并说明已保存的部分；取消仅关闭选择器，不伪造回滚。
- 公共会话只管理草稿、当前查询、请求 epoch 和弹窗键盘行为；群运营调用方仍拥有本领域目录读取和计划资产命令，素材调用方仍拥有自己的授权 loader／commit。

## 实际接入与参考

- 接入页：`web/v3/groupOpsHostAdapter.ts` 的群运营计划详情“选择群”；复用 `SelectionSession` 与既有 `group-ops` modal 样式，不修改冻结 `web/v3/groupOpsStandard.js`。
- 领域读取/写入：`internal/groupops/http/handler.go`、`internal/groupops/app/runtime.go`、计划群资产端点。
- 既有共享选择器参考：`web/v3/shared/ui/materialPickerAdapter.ts` 与 `SelectionSession`。
- GitHub 参考：已用 `gh pr view 296` 核验 [PR #296](https://github.com/qianlan33333-png/AI-CRM-v3/pull/296) 的 `SelectionSession`／素材选择器合同；本 PR 在其分支上复用并补足同一组件的共享 dialog、权限乱序和真实 GroupOps 受权读写，而不是复制冻结 donor。
- 本仓现有 GroupOps Host 端到端参考：`scripts/groupops-host-adapter-e2e.mjs`。

Chromium 对 Radar 页的素材弹窗只验收该真实 Host 能加载共享 dialog，以及 360／420／1280 宽度下多选、主体滚动和底部确认可达。Radar 现有 adapter 尚未把 `selectedRecords`／`onCommit` 接到其业务持久化；这项业务迁移不属于本 PR，不能把该布局验收描述为 Radar 素材保存验收。

本 PR 仅迁移此一真实 GroupOps 调用点；素材、成员、标签和客服页面不在范围内。
