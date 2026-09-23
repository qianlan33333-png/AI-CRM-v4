# 群运营已启用计划的保存与停用边界

## 问题与业务判断

运营计划详情页把已启用计划显示为“可保存”，并开放基础配置、绑定群和 Webhook 的写入口。计划 `12` 的只读页面状态为 `active`、Webhook 接收计划、已绑定 10 个群，基础配置也显示绿色“可保存”。这不是线上保存复现：为避免影响该计划，本次没有点击保存或修改它。

源码已经确定因果链：`web/v3/groupOpsStandard.js` 只把归档计划设为只读；`savePlan` 随后把 active 计划送到 Host。`web/v3/groupOpsHostAdapter.ts` 对详情保存先发 `PUT /plans/{id}`，即使表单选择“停用”也要等这个 PUT 成功后才请求 `/disable`。而 `internal/groupops/app/service.go` 的 `mutate` 只允许 paused 计划执行 `plan_update` 或 `webhook_descriptor_put`，active 计划的配置写入返回 `ErrStateConflict`。HTTP 层将状态冲突和版本冲突一并映射为 409 `operations_conflict`，Host 因而显示“计划状态、版本或配置不满足要求，请刷新后检查”。

这不是应靠刷新绕过的版本问题。active 计划可能已有已接受的 Webhook 或运行定义；把它改成可写会破坏既有状态和版本隔离。详情页应如实展示当前生命周期、版本和可执行下一步。

## 用户行为

1. 已启用计划显示“已启用 · 当前版本 vN”，基础配置不再显示“可保存”。基础字段、负责人选择、保存入口、群绑定和标准编排写入口均不可操作，并直接提示“启用计划请先停用后再修改配置”。
2. 详情页提供单独的“停用计划”动作。它只调用现有 `POST /plans/{id}/disable`，以页面实际展示的 revision 作为 `expected_revision`；不先提交基础配置，不把停用混入“保存”。
3. 停用响应必须确认同一计划、`paused` 状态和递增 revision，随后再读同一计划详情。只有这次权威回读成功，基础配置和 Webhook 配置才重新可编辑。
4. paused 不是草稿：基础配置、Webhook descriptor 和显式重新启用沿现有合同可用；绑定群和标准编排仍是 draft-only，页面明确说明而不保留会被后端拒绝的按钮。需要改变目标群或节点时，用户应建立新的草稿计划，不能绕开既有接受语义。
5. 保存 paused 基础配置会随页面展示 revision 提交，不再让 Host 先读到较新的 revision 后静默提交。409 后只读取当前详情：草稿保留，并在同一计划读回成功时显示“保存使用 vX；当前为 vY”。不会自动重试写入。若 Host 明确标记本次 PUT 已成功、随后启用被现有内容校验以 409 拒绝，复用只读 `POST /plans/{id}/content/preview` 的 `issue_codes` 显示具体缺项（绑定群、负责人、Webhook descriptor 或标准编排）；不把这一类问题写成“请先停用”。
6. Host 明确标记 PUT 已成功而后续启用为 500 或网络失败时，启用结果仍未知：立即权威 GET，读取失败则锁定所有计划写入口，只提供同一计划 GET 重试，绝不自动重试 PUT 或启用。停用 POST 的网络或 5xx 同样只做 GET 恢复；409 是已拒绝的 CAS/状态动作。已有草稿在任一 409、结果未知或“冲突后停用成功”链路中保留，不因回读清空。
7. 一项基础保存、停用或权威回读正在进行时，所有计划写入口和负责人草稿选择统一锁定，防止基础配置与群、节点或 Webhook 交错提交。`刷新名下群聊` 是既有 Provider/source 读取加本地目录投影，不改计划 revision：paused 状态正常可用，但同样在上述写/回读窗口锁住，以免与负责人草稿竞争。

## 复用与实现范围

- 修改 V3-owned `web/v3/groupOpsStandard.js`：生命周期只读呈现、独立停用协调、显示 revision、统一计划 mutation lock、草稿/只读回读恢复，以及为真实 `data-action` 绑定 `__dcBound` / `capabilityState=real`。不修改 frozen donor 或 shared feedback。
- 修改 V3-owned `web/v3/groupOpsHostAdapter.ts`：详情 PUT 优先采用调用方显式且严格校验的 `expected_revision`，并以仅内存的已接受 PUT 标记区分后续 lifecycle 失败；保留既有 Host 认证、CSRF、幂等和 API 路径。此处不重排 `PUT → enable` 的既有 draft/paused 行为；active 不会进入该链。
- 修改 `scripts/groupops-host-adapter-e2e.mjs`，并扩展现有隔离 PostgreSQL GroupOps 生命周期测试。测试 fixture 使用合成计划、群和运营成员，绝不访问计划 `12` 或真实 Provider。
- 新增本 PRD。无 OpenAPI、迁移、后端业务规则、删除路径或 frozen asset 改动。

## 分类和边界

- **OneID：不涉及。** 负责人继续使用既有 GroupOps 本地 staff compatibility key；不解析客户、渠道或外部身份。
- **持久化：复用。** 停用和基础配置写复用既有 GroupOps PostgreSQL UoW、行锁、CAS、审计和幂等收据；不变更事务边界或表。
- **内部任务与 Provider：不新增。** 本修复不创建任务、队列或 Provider 调用。`刷新名下群聊` 保持既有 Provider/source 读取和本地目录投影合同，不改计划或新增读取；显式停用会调用既有本地生命周期命令，可能按既有版本/状态合同阻止后续执行；不会发送群消息或修改真实群。
- **前端一致性：** 复用 `groupOpsStandard`、`groupOpsHostAdapter` 与 frozen shared feedback 的真实 ownership 约定。当前 Skills catalog 没有可调用的 Product Design 路由；已按前端一致性 Skill 和 component map 检查既有组件，未伪称 Product Design 审查完成。

## 验收

| 场景 | 结果 |
| --- | --- |
| active Webhook 计划（含绑定群） | 显示状态与版本；所有配置写入口零 PUT/POST；共享 feedback 不将真实按钮标作“后端未就绪”。 |
| 明确停用 | 一次 `POST /disable`，body 使用展示 revision；同 ID/paused/revision 确认后才 GET 回读并开放 paused 允许的编辑。 |
| 停用 409 / 网络 / 5xx | 不自动重复停用；保留用户上下文；仅允许同计划 GET 恢复，提示当前状态和版本或读取失败。 |
| paused 基础保存 | 一次 PUT 使用展示 revision；成功后权威回读；仍为 paused，不隐式启用、建任务或外部效果。 |
| paused 的陈旧版本 | Host 不用后读到的新 revision 覆盖页面版本；409 后保留草稿，读回同计划的新 revision 后允许人工复核再保存。 |
| PUT 已接受、启用 409 | 仅此可确认链显示预览中的具体缺项；不把另一个编辑者的 revision 增长误称为本次保存。 |
| PUT 已接受、启用 500 / 网络 | 权威 GET 区分当前状态；读失败进入 GET-only 锁，草稿保留且零自动 PUT/enable 重试。 |
| 写/回读 pending | 基础、群、节点、Webhook、负责人选择和目录刷新均零计划 mutation；paused 稳定时目录刷新仍可维护本地投影。 |
| paused 群/节点 | 不显示会被服务端拒绝的写动作，且程序化 action 不发写请求。 |
| 后端生命周期 | 隔离 PG 证明 active 配置拒绝、pause CAS 成功、paused 更新/descriptor 保持 paused，接受的 Webhook/运行版本保护和零 Provider 调用不变。 |

## GitHub 参考

- [#212](https://github.com/qianlan33333-png/AI-CRM-v3/pull/212)：paused 基础保存保留 CAS、幂等和草稿可见反馈。
- [#289](https://github.com/qianlan33333-png/AI-CRM-v3/pull/289)：两个保存入口共用写锁，成功后必须权威回读，失败不伪造成功。
- [#304](https://github.com/qianlan33333-png/AI-CRM-v3/pull/304)：详情读取的 generation 和旧响应隔离，不能让延迟结果覆盖当前计划状态。
