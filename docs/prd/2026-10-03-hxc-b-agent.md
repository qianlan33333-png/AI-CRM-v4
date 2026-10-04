# B 板块按最新问卷生成个性化私信

用户授权：2026-10-03，使用 Agent 模式及现有 DeepSeek 配置跑通 B0–B5；不让用户填空，让用户感到被理解与关心。继承原 B 时间窗口、小时刷新、每阶段每客户一次及生产配置授权。

```mermaid
flowchart TD
  A[原人群包每小时筛选并新增成员] --> B{原阶段一次性收据已接受?}
  B -->|是| C[返回原收据 不补发]
  B -->|否| D[冻结最新问卷 已发布Agent提示词 原小程序素材与模型策略]
  D --> E[同事务保存原入包登记 原发送Run 与现有AI生成效果]
  E --> F[现有External Effects调用DeepSeek]
  F -->|失败或结果未知| G[保留现有失败或未知收据 不盲重试 不发占位文案]
  F -->|有效结果| H[同事务保存生成结果并接受原Outbound发送意图]
  H --> I[现有企微发送与独立回执]
```

现状：生产保存 deepseek-chat 配置 version=2，真实连接测试 200 / 连接成功（11 tokens，无客户数据）；环境默认开关 false，但认证读取有效配置中心 revision=2 的 ai_agent_generation.enabled=true，因此生产最终开关已启用，不改环境或全局配置。Agent 可冻结问卷、聊天、标签、激活上下文；现有手工 Agent Run 生成后进入人工审核。自动入包策略只接固定内容，直接换模式不会通。

最小改动：复用原自动入包与一次性登记，原生成 item / EER / River / completion 和 Outbound；自动策略 Run 的有效生成结果进入已授权自动发送，手工 Run 保持原审核。冻结最近一次问卷，避免旧回答混入。小程序源在接受时冻结，生成完成不再读取当前问卷、Agent 或素材。使用现有 JSONB 上下文保存自动发送所需冻结参数，无新表、无迁移、无新队列或 Python 定时器，无前端开发。

OneID：读取原 canonical customer，复用原成员事件及一次性 lineage 保护；问卷经 Survey Port、聊天与标签各经稳定 Port，不自建匹配或建客。

持久化 / 外部效果：生成接受、入包登记、一次性收据、Run、审计在同一 PG UoW；生成 completion、Outbound 接受及效果完成同事务。DeepSeek 及企微写沿用现有 EER / Outbound；结果未知保留原键，不补发历史用户。无需新增限制，复用已有模型输出和 Provider 边界。

文案：六个 Agent 按阶段分别提供开始提问、降低门槛、拆行动、卡点帮助、推荐话题、轻提醒。结合最新问卷中的一个真实方向表达关心，推荐完整可直接复制的问题；HXC 提问使用“根据你对我的了解”，不要求 HXC 读取问卷全文。不得输出填空、未替换变量、具体联系方式或收入，不假设没聊天，不编造邀请或权益。

验收：最新问卷选择含重提覆盖；Agent 自动入包真实生成接受/成功完成/原小程序意图，重复事件与完成重放只产生一次；失败/未知不发送；固定路径与手工 Agent 审核保持原行为；生成或 Outbound 接受失败原事务回滚。最终同包安装旅程与生产 Agent 绑定/启用/模型开关独立读回，真实 Provider 与收到结果分别报告。

五项影响：接口不新增；业务自动策略增加 Agent 生成分支；直接模块 automation、Composition、generation context；无 UI；相关 Automation 与 Composition 用例及 PG 接受/重放旅程。回退使用旧固定 Agent 及原包绑定，保留原一次性收据，运行未知先只读对账。

参考：[DeepSeek 官方首次调用](https://api-docs.deepseek.com/zh-cn/)、[Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion/)、[Temporal Durable AI](https://docs.temporal.io/ai)。采用其持久化模型步骤思路，复用本仓 EER/River，不引入 Temporal 或 SDK。

## 自然运行发现的相关修复

18:01 自然刷新新增一条 B0 原事件。问卷、聊天、标签、画像的生产实际 Adapter 在 PostgreSQL read-only 下均读取成功，冻结上下文有效；无生成 item / 新 Outbound 接受。事务失败点是新分支登记 Config-owned usage 使用 `policy_generation`，现有合同仅接受 preview / confirm / execution，故整事务回滚。复用既有 worker/execution 登记，不扩大配置中心合同、不加限制或表。原队列自动重试原事件，保留 once 与生成/发送幂等键，未人为重放或补发。

回归在原 Agent PG 接受/完成/重放旅程绑定真实 Config RuntimeReleaseService 与 store，增加 usage/其他状态一起回滚、重放仅一个 execution usage 的断言。修复前真实服务复现同样 runtime unavailable，修复后四个分支重跑。生产只读上下文诊断不写生产、不调用模型/企微、不打印客户内容或密钥；与合成旅程分开记录。


## 2026-10-04：聊天发言归属修复

生产只读复现：一条 B1 第3版生成记录，把用户作为接收者的三条群消息混入上下文，产生“你在群里聊”的错误归属。该记录在00:00:20生成，短模板在00:00:57发布；已冻结的旧任务保持原版本，不是缓存。另一用户未提交问卷，命中A2固定话术，属于原业务分支。

```mermaid
flowchart LR
 A[本人最新问卷] --> C[冻结 Agent 上下文]
 B[当前 OneID lineage] --> D[Archive 仅查询本人作为可信 sender 的消息]
 D --> E[按时间筛选后取最近20条]
 E --> C
 C --> F[已发布短模板生成]
 F --> G[原 Outbound 发送与一次性收据]
```

参考：LangChain 官方消息合同 https://reference.langchain.com/javascript/langchain-core/messages/Message 将角色/来源视为上下文的一部分。本仓 Archive 已保留 participant_role、actor_type、customer_id_at_ingest、resolution_status，直接复用，不引入新的身份匹配或队列。

最小改动：为稳定 CustomerQuery 增加默认false的 AuthoredByCustomerOnly 过滤；在 Archive Store 内按 canonical lineage + sender + external_customer + found 筛选，且在 ORDER/LIMIT 前执行。仅 Agent 聚合器启用。保留用户本人私聊和群聊发言，排除其他客户、员工、robot/unknown/未解析发言。后台聊天展示继续使用默认完整会话视图。

OneID：仅读取原 CanonicalLineage，不改身份或归属。Persistence：仅 owner 读取语义，无迁移、历史数据写入。External Effects：原冻结/生成/发送路径与幂等不变；不人工重放、撤回或重发已提交消息。UI：无页面代码变动。不涉及新增任意资源阈值；本人发言过滤来自用户明确要求及已证实归属缺陷。

验证：真实 PG 对其他成员群发言、员工私聊、unknown sender、本人私聊/群聊及 lineage 成员发言进行隔离；过滤在分页前生效，watermark/keyset与默认会话查询保持原行为。聚合器具名测试核对查询标志，最新问卷继续优先。已安装包认证 Archive 读取保持完整会话，Agent 冻结上下文只含本人消息；生产只读内容核验不输出 PII/原文。

历史已冻结/Provider接受的任务不改内容；本次修复只作用于新冻结上下文。A分支文案不属于本修复范围。
