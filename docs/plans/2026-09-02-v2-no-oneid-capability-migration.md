# 2026-09-02 V2 无 OneID 能力迁移计划

本计划记录 AI-CRM-v3 的分批能力迁移。v2 冻结供体仅用于行为、测试、叶子协议和逐字节前端资产校验，不是 Go module、运行依赖、数据源或子模块。

## PR01：持久任务、外部效果内核与 Push Center

- 供体：`AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`；每个被迁移资产登记 SHA-256 manifest，CI 校验冻结版本与字节。
- 数据：从 v3 `0005` 开始。外部效果、操作回执、尝试记录与 River 任务链接由 `externaleffects` 拥有；Push Center 只拥有从这些本地事实生成的读模型。不得落 `customer_id`、`external_userid`、手机号、收件人或 Provider 请求/响应体。
- 语义：Provider 默认 disabled；明确保留 `accepted`、`queued`、`attempted`、`executed`、`outcome_unknown`、`reconciled`、`final_failed`。`outcome_unknown` 必须进入人工对账，禁止换幂等键重试。
- 运行：持久任务使用 PostgreSQL/River，API 与长运行 worker 为独立 role；任务入队、效果状态、幂等回执和审计在同一事务。Provider 网络调用绝不持有事务。
- 权限：管理员读接口要求 session；取消、重试、对账要求管理员 capability、CSRF 与幂等键。OpenAPI 记录所有公开路由。
- 前端硬门禁：本 PR 迁入的 V2 模板、TS、CSS、文案、交互和请求契约必须逐字节不改；CI 对 manifest 重新计算 SHA-256。只公开已闭环的 External Effects/Push Center 隐藏工作区（`campaigns.html?view=external-effects`），不得开放 Campaign 或其它排除模块；后端兼容 DTO 解决差异。
- 验收：单元状态机、真实 PostgreSQL 回放/事务/取消/重试/对账测试、管理员鉴权与 CSRF 测试、OpenAPI 校验、前端构建和部署产物 manifest 均须通过。

后续 PR 按已冻结依赖顺序迁移，绝不以目录存在、HTTP 200、Mock 或排队成功宣告能力完成。
