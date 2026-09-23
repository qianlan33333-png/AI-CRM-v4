# 企微侧边栏与精简 Customer 360 完整交付计划

目标：在 AI-CRM-v3 上线六页签企微侧边栏与精简 Customer 360，并完成可审计的历史周期权益和客户优惠券迁移。

固定范围：核心画像（含时间线）、问卷、商品、订单、优惠券、素材；Customer 360 包含身份、档案、订单、问卷、风险、最近触点。不建设聊天记录、其他客服聊天、CRM 标签、跟进关系、运营摘要或自动化摘要。企微原生标签仍由企微客户端显示。OAuth/JSSDK 复用现有实现。

```text
OneID: resolves scoped WeCom external identity to canonical customers.id; no follow-relationship coordination
Persistence: local transactions + existing Provider-read infrastructure + outbound-owned Provider writes
```

## 交付工作包

| 工作包 | 可观察结果 | 完成证据 |
|---|---|---|
| P00 | PRD、ADR、供体 manifest 和批准差异明确 | 文档评审；供体 SHA 固定 |
| P01 | 无关系行仍可签发、验证 Context Token | WeCom 单测；Composition 不注入关系 Store |
| P02 | 唯一活动 Host Adapter 只有六页签 | DOM/静态契约；无双控制器 |
| P03 | 画像 CAS 更新和 declared 手机号声明 | 数据库 receipt/audit/outbox；冲突与重放测试 |
| P04 | canonical Customer 问卷与安全时间线 | 本地 projection 测试；无聊天等事件源 |
| P05 | 商品、普通订单、周期权益和备注 | Product/Order Port；Order 原子备注事实 |
| P06 | 客户优惠券、图片素材、启用雷达链接 | Coupon/Media/Radar Port；无假数据 |
| P07 | 商品、素材、雷达由一次性 grant 调用 JSSDK | intent/effect/job/outcome receipt；超时关闭 |
| P08 | Customer 360 六个精简分区 | schema/DOM 禁止字段；分区降级测试 |
| P09 | OpenAPI、迁移、CI、灰度和生产验收 | migration reconciliation、release SHA、readyz、真实企微 smoke |

## 数据迁移完成定义

1. 从固定时间点的 AI-CRM PostgreSQL 只读快照读取 `service_period_entitlements`、`service_period_products`、`wechat_pay_products` 和 `commerce_coupon_claims`。
2. 每行 UnionID 必须以明确的开放平台 scope 经 OneID `Resolve`；not_found/conflict 进入只存 digest 的隔离账，不隐式建客。
3. 周期权益进入 Order Owner，客户优惠券进入 Coupon Owner；source system/key/digest 唯一，重复执行只能 replay。
4. inspect → dry-run → 目标库只读 preflight → backup → apply → replay → reconcile 的 manifest SHA 一致；preflight 阻塞数和最终 quarantine 必须为零，输入数等于 imported + replayed，金额、状态和有效期摘要相符。
5. 迁移过程不写供体，不把 UnionID、external_userid、手机号或 claim_no 写入日志。

## 发布门禁

- `make check`、race、OpenAPI、前端 build、供体行为契约全部通过。
- 生产迁移 0054-0058 已应用，`/readyz` 返回 200，release SHA 等于 main SHA。
- `/sidebar/bind-mobile` 只出现六页签；后续业务请求无 raw external_userid。
- `/api/admin/customers/{id}/360` 不包含 `message_summary`、`tags`、`owners`、`user_ops_status`、`automation_status`。
- 真实企微 smoke 分别证明上下文建立、列表读取、一次发送调用与 outcome receipt；未执行 smoke 的能力必须标为 unverified，不能宣称已发送。
