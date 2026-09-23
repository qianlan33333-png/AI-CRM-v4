# 渠道列表归档动作

## 业务判断

渠道归档不是永久删除。归档保留渠道配置、历史与归因记录，停止新的扫码欢迎语和入渠标签；管理员可在编辑页明确选择“启用”后恢复。当前列表把“下架”显示为“后端暂无渠道归档 operation”，但现有 Channel Catalog 已有受版本保护的 `PATCH /api/admin/channels/{channel_id}`，其 `status=archived` 是唯一归档路径。

永久删除仍不在当前 Catalog/OpenAPI 能力中。本变更不把删除伪装为归档，也不发送 `DELETE`；列表显示灰色“删除不可用”与原因。

## 范围

1. 仅在 V3-owned `channelCenterAdapter` 中，于冻结模板运行前作内存级文案/可用性转换：将旧“下架”占位呈现为“归档”，保留“删除不可用”；冻结模板源文件和 controller 源文件均不改。
2. 从冻结 `AdminController.renderVals()` 的当前行 `resourceId` 建立归档回调闭包；不得从渠道名称、DOM 文本、行序或 dataset 推断目标。确认期间即使列表行重渲染/复用，操作仍只针对该闭包 ID。
3. 确认后先 `GET` 完整渠道与 ETag，再以原四类欢迎素材、标签、客服分配、不可变编码和其余完整写入 DTO 执行一次 `PATCH`，仅将 `status` 改为 `archived`。
4. 使用 CSRF、If-Match 与同一次逻辑操作稳定的幂等键。网络结果未知（含 5xx）先读取同一渠道；未确认归档时不更换幂等键盲重发。只有明确未接受的 401、403、409 终结该意图。
5. 成功后由 controller 重新读取权威列表；已归档行显示“已归档”并零 PATCH。若重新读取失败，显示“已归档，但列表未更新”，并让旧回调保持该确认状态，不能创建新归档意图。

## 不在范围内

- 不增加归档或删除 API，不硬删除，不修改冻结 donor/controller。
- 不创建 Provider 调用、外部效果、任务、队列或迁移。
- 不操作线上“测试渠道”或其他已有渠道；验收使用隔离 fixture。

## 分类与复用

- OneID：不涉及。渠道配置归档不解析、创建、关联或合并客户身份。
- 持久化：复用现有 Channel Catalog PostgreSQL Unit of Work、CAS、收据、审计和 Outbox；本 PR 不改变其事务边界。
- External Effects：不涉及。归档更新不调用 Provider。
- 终端：管理端单壳。复用现有 Channel Center adapter 与 shared feedback 的 `confirmBox`/`toast`；不修改冻结 donor。

## 验收

| 场景 | 结果 |
| --- | --- |
| 取消确认 | 零写请求。 |
| 确认归档 | 完整 DTO 经 PATCH 仅变更为 `archived`，携带 CSRF、ETag 与稳定幂等键。 |
| 并发版本或无权限 | 明确失败，页面不把渠道表述为已归档。 |
| 网络结果未知 | 先回读；未确认时不换键重发。 |
| 已归档后的列表 | 回读并显示“已归档”；扫码欢迎/标签停止语义与二维码 guard 保留。 |
| 同名或行复用 | 操作仍只发送闭包持有的 resourceId，不会按名称或当前 DOM 行改写其他渠道。 |
| 成功但列表回读失败 | 显示“已归档，但列表未更新”；旧操作入口不会创建新 PATCH。 |
| 删除 | 不展示可点删除，也不发 DELETE。 |

## GitHub 参考

- [#235 归档渠道可编辑并显式恢复](https://github.com/qianlan33333-png/AI-CRM-v3/pull/235)
- [#255 归档渠道不可作为扫码就绪渠道](https://github.com/qianlan33333-png/AI-CRM-v3/pull/255)
