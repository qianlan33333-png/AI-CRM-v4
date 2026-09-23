# 渠道归档后的配置编辑

问题：生产渠道 id=1、编码123、版本5处于 archived；不改编码、只改客服也被拒绝。v3 Channel.Update 将 archived 视为终态，CatalogService 又把该拒绝映射成通用冲突。

旧仓行为参考：AI-CRM-v2 `internal/contact/app/channel_catalog.go` 的 UpdateChannel/mergeChannel 允许修改已有渠道配置和状态；`web/src/admin/templates/channelForm.html` 对归档记录同样提供启用、停用、归档选项。旧仓仅作为只读行为供体。

确认的行为：
- 启用、停用、归档渠道都可保存合法配置，包括替换客服。
- 编辑归档渠道不会自动启用；只有明确提交 active 才恢复启用。
- 归档和停用渠道仍不能发布；已有编码不改变。
- 所有编辑继续校验原版本；陈旧版本拒绝，不能覆盖其他人的编辑。
- 每次成功修改建立新配置快照，同事务保存收据、审计、Outbox。重放不增加新版本；历史配置不改写。

分类：OneID 不涉及（修改渠道定义，不改变客户身份或历史归属）；持久化复用 Catalog PostgreSQL Unit of Work；不新增 Provider 调用、任务或 External Effects。

验收：领域测试覆盖归档编辑、显式恢复启用、旧版本拒绝；PostgreSQL 集成测试覆盖替换客服后的读取、重放、审计/Outbox/收据数量与恢复启用后的 archived_at 清理。
