---
name: aicrm-v3-development
description: Implement or repair AI-CRM-v4 with deliberate identity, transaction and external effect boundaries, mechanical preparation, direct-consumer checks and exact candidate delivery. The v3 name is retained for compatibility.
---

# V4 开发

读当前 `AGENTS.md` 和 [开发入口](../../docs/development-before-start.md)。复用已授权父 brief；一项能力只准备一次业务判断流程、参考和 PRD。一个候选交付完整可观察行为及其相关测试，按行为拆分，不按文件/行数拆分。

## 开始判断

留下简短结论；不涉及时写原因，不制造依赖：

```text
OneID: 不涉及 | 读取客户 | 解析身份 | 显式建客 | 关联身份
执行: 无状态 | 本地事务 | 内部持久任务 | Provider 读取 | Provider 写入
```

- 客户主键只有 `customers.id`。身份用 `internal/identity/port`；OpenID/UnionID scope 和可信来源不能省略，HTTP 不能自报 verified，Resolve 不隐式建客，证据不足保持 pending/conflict。
- 同一业务命令的状态、幂等、审计、Outbox/外部意图接受若需原子性，确认共享 PostgreSQL Unit of Work；不同事务不等价。
- 可恢复内部执行用 `internal/platform/jobqueue`；Provider 读走归属 connector；企微业务写只经 outbound 与 externaleffects Port。复用原幂等键、attempt、lease/fence、回调和对账，不再造队列、匹配器或重试状态机。
- 稳定 Port/版本化事件协调领域。每表一个 Owner，不跨领域表写或 import app/store/http/worker/provider。
- accepted、queued、executed、delivered 是不同结果。outcome_unknown 保留原键只读对账。支付/退款重放、身份错归属、鉴权绕过、泄密和数据丢失是停止条件。

## 限制必要性判断（奥卡姆剃刀原则）

假设没有拟新增的限制：业务正确性、安全和实际资源需求仍成立，就不加；否则先复用现有边界，采用足够的最小约束。数量、格式、长度、重试、超时和权限必须有具体业务/Provider/数据库/资源依据，不凭习惯设固定数字。私有凭据权限不能机械套到公开不可变源码与 CSS。简短记录“对象、具体后果、依据、作用范围”；没有新增限制写不涉及即可。

## 实现与验证

- UI 先用 [Product Design 路由](../aicrm-v3-frontend-consistency/SKILL.md)，复用已批准参考和组件；不为相关修复重做全套设计。
- 自动准备源码索引、消费视图、CLI 目录及发布程序声明；展示机械差异，提交后从干净准确 checkout 核对。准备修改改变了 head，旧收据不升级成新 head 的结果。
- AI 读取准确 diff、改动函数直接调用方与字段直接消费者，在 `docs/release-checks.json` 给出包/具名用例/简短原因。小包整包，大包具名；共享/未知改动补最近合同，不自动全量或全仓 vet/race。
- 字段或操作能力覆盖用户动作→保存/任务→持久化→重新读取。新增题型不只测展示；刷新目录不只测昵称。后端改动也看实际页面消费者。
- 身份、权限、支付幂等、事务、迁移只加强本次相关拒绝、重放、并发、数据保留；不让正常流程为了过测试增加注册或提示。
- 纯单元有效结果和有效缓存可复用；Linux/PG/浏览器在适用环境执行一次。空匹配、skip、缺环境和超时为未完成。修复只重跑失败与直接影响检查。

## 交付

五项影响简短说明：对外合同、业务机制、直接关联模块、页面、验证证据。用开发入口的 handoff 命令生成准确 base/head/tree/ref、原任务 ID、范围授权、结果和未验证项；实际推送后重读 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json` 并向其 thread_id 投递。父范围授权继承；无关新增另列。批次内已获开发授权、实际交付候选的原任务可按用户授权通知；其他对话或外部消息不因此获准。

[发布 skill](../aicrm-v4-release/SKILL.md) 管原队列和同包晋级。源码检查、安装包预发、生产技术读回、真实认证/Provider 业务事实分别汇报；局部检查不称完整回归。
