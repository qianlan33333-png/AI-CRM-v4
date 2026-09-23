# ADR-0001：客户激活、目录与手机号导入边界

- 状态：Accepted
- 日期：2026-09-02

## 决策

1. 以 v2 Behavior Contract 重建能力，拒绝整目录复制。
2. 企微全量同步是可恢复的持久化状态机；Provider 调用不持有数据库事务。
3. 采用一次性最小快照导入，拒绝 v3 长期连接另一生产数据库。
4. 源表手机号没有 Provider 验证证据，因此只能作为 declared identity 附着到已有 Customer。
5. 一号多客或已属他根时写冲突收据，拒绝按昵称、手机号、创建时间等弱证据自动合并。
6. `customer_directory_projection` 是 Customer 拥有的可重建读模型，Identity 和 WeCom 表仍是业务真相。
7. 业务状态、幂等收据、审计、Outbox 与 cursor 同事务提交；投影消费完成并对账后才能宣告同步成功。
8. WeCom 拥有同步轮次和 cursor，但不拥有另一个任务队列；投递、lease 和失败重试复用 `internal/platform/jobqueue` 的 River runtime。

## 后果

- 得到对账友好、可崩溃恢复、不依赖旧环境的能力。
- 需要额外的轮次、收据、Outbox 和投影存储，并在上线前完成恒等式对账。
- 冲突数据会保持待处理，不以自动化便利换取身份误合并风险。
