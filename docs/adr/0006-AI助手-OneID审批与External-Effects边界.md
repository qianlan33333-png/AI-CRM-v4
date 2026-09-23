# ADR-0006：AI 助手的 OneID 审批与 External Effects 边界

- 状态：Accepted
- 日期：2026-09-04
- 关联 PRD：`docs/14-PRD-AI助手-OneID与持久审批执行.md`

## 决策

1. 新建 `internal/aiassistant`，独占 review plan、recipient、content version、decision 和 effect binding；产品 URL 继续使用 `/admin/cloud-orchestrator/plans`。
2. Recipient 唯一业务键是 canonical `customers.id`。内部 Port 只接受 Customer ID；外部 Adapter 只调用 Identity Resolve，不 Provision、Bind、Merge。
3. 逐人通过只记录审阅决定，整单确认才接受发送效果。
4. 审批通过 `internal/outbound/port` 创建私聊 intent；Outbound 在同一 UoW 调用 `externaleffects/port.TransactionalAccepter`。
5. EER 只保存四个摘要和状态；正文、客户/员工 Provider 身份和 Provider 响应由各自 Owner 控制。
6. 只使用现有 River/EER；不迁移 donor `broadcast_jobs`、`external_effect_job`、Worker、lease 或重试内核。
7. Provider 调用发生在事务提交后。`executed` 表示 Provider accepted，不代表 delivery；unknown 只允许原键对账。
8. donor 前端 byte-frozen；Host Adapter 只做 v3 shell、DTO、OneID、安全和状态适配。
9. 本期不迁移旧 AI 助手历史数据。

## 架构

```text
Automation / signed integration
          │ aiassistant Port
          ▼
AI Assistant aggregate ── stable Ports ── Customer/Identity/Staff/Media
          │ approval UoW
          ▼
Outbound intent ── transactional accept ── External Effects + River
                                                 │ post-commit
                                                 ▼
                                          WeCom Provider
                                                 │ artifact
                                                 ▼
                                     AI Assistant projection
```

## 拒绝方案

- 翻译 donor Cloud Orchestrator 后端：会复制外部身份、队列和效果状态机。
- 并入 Automation：Agent 配置与客户审批/发送结果生命周期不同。
- 审批后独立创建 effect：无法证明原子性，可能漏发或重复。
- 逐人通过立即发送：破坏最终整单闸门并制造部分发送撤回问题。
