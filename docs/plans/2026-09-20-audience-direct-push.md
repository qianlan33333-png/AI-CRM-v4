# 人群包持续推送接口

## 业务判断

该能力与“成员增量进入时触发一次”并行：调用方主动提交一批明确的收件人、固定话术、小程序素材和发送人，合法条目无需 AI 生成或人工审核，直接进入现有企微发送队列。一次请求接收 1–1000 条，正文上限 2 MiB，逐条受理；单条失败不回滚其他业务校验失败项。

`accepted` 只表示业务事实、幂等收据、审计、Outbox、Outbound intent 和 External Effect 已在同一 PostgreSQL 事务持久化，不代表企微已实际发送。实际 `sent_at` 必须来自企微分页回执的唯一匹配结果；24 小时观察由该时间起算，覆盖不足不能记为未打开，最迟补查至发送后 48 小时。

## 开发前分类

- OneID：涉及。请求中的 UnionID 只通过配置好的开放平台 scope 唯一解析现有 Customer；不隐式建客、不自动合并、不在响应和结构化日志中暴露。
- 持久化与内部任务：涉及。Automation 持有配置、批次、条目、观察事实、审计和 Outbox；River 持有回执扫描和延迟观察任务。
- 外部效果：涉及。Automation 只提交 `audience_direct_push` 消息意图；企微写入仍由 Outbound → External Effects 唯一执行。
- 领域边界：Segment 只通过稳定 Port 回答发布快照成员和发送人白名单资格；Media 冻结素材来源；Identity 提供 OneID 解析；任何领域均不跨表写入。

## 复用与参考

- 复用现有 `automation_message` Outbound intent、企微私聊发送器、External Effects、素材准备链路和发送完成投影。
- 将 Excel 批次的 `msgid + sender_userid + recipient` 全分页唯一匹配抽成 Outbound 稳定共享函数。
- 复用 Excel 观察组件的精确 path 和覆盖语义，新增只读 `/content-opens` 查询，不把组件 SQLite 作为业务权威状态。
- 幂等语义参考 Stripe：同键同正文重放原结果，同键不同正文冲突。
- 生命周期参考 Twilio：受理、排队、Provider 接受、实际发送证明和失败分离。
- 延迟任务参考 River：业务状态与唯一任务在同一 PostgreSQL 事务中提交。

## 接口

- `POST /api/automation/audience/webhooks/{webhook_key}`：HMAC-SHA256，数组 1–1000 条，字段为 `unionid`、`text`、`miniprogram_id`、`sender_userid`、可选 `client_reference`。
- `POST /api/automation/audience/webhooks/{webhook_key}/status`：批量查询最多 1000 个 `push_id`，只能读取当前 Webhook 所属人群包。
- `GET/PUT /api/admin/ai-audience/packages/{id}/direct-push`：管理开关、Webhook、客户端标识和滚动 24 小时频控；密钥不回显。
- `GET /api/admin/ai-audience/packages/{id}/direct-pushes`：脱敏发送与观察记录，不展示 UnionID 和完整话术。

## 前置条件

人群包存在且请求客户位于当前已发布快照；UnionID 在固定 scope 内唯一解析；发送人是有效 CRM 员工且位于人群包发送人白名单；小程序素材存在且启用；独立环境变量 `AICRM_AUDIENCE_PUSH_WEBHOOK_SECRET` 至少 32 字节；企微 Provider 写入和回执读取可用；精确打开统计另依赖 Excel 观察组件及其只读覆盖数据源。

## 验收边界

部署版本、配置读回、接口受理、企微 Provider 回执、真实发送时间、24 小时打开结果是六个独立门禁。非好友、缺外部联系人、企微拒绝和无法统计必须形成明确状态，不能隐藏、自动重发或伪装成送达/未打开。
