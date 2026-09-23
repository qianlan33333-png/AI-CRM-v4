# ADR-0005：自动化运营的 OneID、持久快照与外部效果边界

- 状态：Accepted
- 日期：2026-09-03

## 背景

v2 同时存在 Segment、Automation、发送和冻结前端，但其客户 ID、运行时约束与 v3 的 OneID/EER 不同。简单复制会产生第二套身份根、队列、重试器和 Provider writer；页面实时扫上游则无法复现历史人群，也不能安全重试或审计。

## 决策

1. 使用 Segment-owned 不可变 audience configuration 与 published snapshot。规则只经稳定 Source Port 读取事实，输出排序去重后的 canonical `customers.id`。
2. 外部身份只经 Identity Port 解析；本模块不建客、不绑身份、不自动合并，也不复制 v2 customer ID。
3. 使用 Automation-owned immutable policy、unique enrollment、run、recipient 与 action snapshot 冻结执行输入。
4. 内部持久任务统一使用 River；业务模块不自建 worker、lease、retry 或 reconciliation 状态机。
5. 所有企微业务写由 Outbound 接受 message intent，并由 EER 持有 effect/attempt/fence/reconciliation；Provider 调用在事务外。
6. Segment/Automation/Outbound/EER/Identity/Access 只通过 Port 或版本化事件协调，禁止跨域 Store 和跨域表访问。
7. v2 作为固定 SHA 的只读供体，冻结前端字节不变；v3 Adapter 接真实 API。
8. Provider 默认 disabled，并增加自动化运营独立的 disabled/probe/limited 门。probe 强制单收件人。

## 被拒绝的方案

- 整目录复制 v2：会复制旧身份和执行模型，形成运行依赖。
- 全部归 Automation：破坏表所有权和跨域边界。
- 每次页面实时查询成员：无法复现、无法原子发布，也无法安全对账。
- 业务模块直接调用企微：绕过 EER 的幂等、未知结果和 fence。
- 复用全局 Provider 开关：会把其他领域的授权误扩展到自动化运营。

## 后果

正向：历史执行可复现；重复任务和重启安全；身份与发送人执行时重验；未知结果不会被误判成功；迁移可 Shadow 对账。

代价：需要多个不可变版本和摘要；运行状态更细；发布必须经过两级 Provider 门和探针；跨领域联调只可走 Port。

## 失败模式与控制

- 身份无唯一命中：隔离，禁止入群。
- 刷新中断：staging 保留为失败事实，旧 published snapshot 继续服务。
- preview 后版本变化：确认冲突并要求重预览。
- 调用后超时：outcome_unknown，停止自动重试。
- 过期执行者写回：generation/fence 拒绝。
- v2 迁移有在途效果或凭据可写：inspect 立即停止。
- Shadow 数量/摘要漂移：禁止探针和扩量。

## 复核条件

只有当产品范围新增多渠道 Campaign、Identity 提供新的已验证合并语义、EER 获得新的事务协调接口，或单数据库模块化单体架构被正式替换时，才复核本决策。
