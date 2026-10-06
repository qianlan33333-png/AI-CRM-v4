# 本轮独立敌手审查与最小修复

用户要求 GPT-6.1 Sol 最高可用推理，进行更细致扫描、修复、严格对抗测试和迭代。父调用指定 gpt-6.1-sol/ultra；当前工具无实际后端设置读取接口，不能独立确认。本轮子代理创建请求或继承同设置。

准确国内 main/base：b74037c3c9dbd3a187338cb7ccc8c80435423c6e；tree ff1ba2ff460d6c5a2e89e7be3a38aca8d2cf8004。线上固定 helper 只读游标/实际安装收据均绑定同 head/tree，ready。原 v13 报告与 c334 发布事实保持不变；其他工作区未提交修改只读记录，不取或覆盖。

本轮授权：扫描、本地实现修复及隔离验证，不推送、合并、构建安装共享预发或生产。不触发真实资金、Provider、客户消息、付费AI，不改生产数据、安全设置或凭据。库存和问卷指定员工受众不恢复。

业务验收按实际持久化不变量：

1. 支付成功、退款累计金额、权益发放撤销与收据必须一致且同 UoW；迟到/乱序/重复回调及旧幂等键不会错归属或丢恢复任务。
2. 身份合并/拆分后主体、历史数据及额度一致，现有会话/别名/对象不能产生错误客户归属。
3. 活动/产品/资格停用归档或配置变化与任务并发时保留准确历史快照，撤销与奖励/佣金记录不会穿透；资金接受必须按锁内当前已退款事实。
4. 租约过期/抢占/崩溃/重试耗尽恢复保留原意图及幂等键，owner 与 effect 状态同事务，unknown 不盲重试。
5. 实际 DOM 原生恢复、身份换绑、迟到响应、双击和断网不产生新购买/领券意图或保存过期数据；按实际作用补齐前轮八项 JSDOM 环境缺口，并选必要 WebKit/Chromium 用例。

OneID：客户归属、公共会话、订单/裂变/分销和 Survey 涉及 canonical customer 与稳定 Identity Port；产品定义本身不需身份匹配。Persistence：业务本地 UoW、River 内部持久任务与 Provider read/write 意图；合成 Provider 仅证实逻辑，不构成真实外部效果验收。External Effects：复用既有唯一接受/lease/fence/reconciliation，不新增内核。当前不涉及无依据新增限制；具体修复如需约束须记录实际错误及最小边界。

先从实际实现、SQL约束、锁序及直接消费者推断不变量，再写能先失败的独有目标。区分业务缺陷、夹具/环境失败和规则不明确。失败修复只重跑目标及相邻影响，不引入大型测试框架或无证据重构。必要时小规模状态组合/mutation检验断言。报告按文件阅读深度与实际具名运行记录，不以测试数替代覆盖。

参考已读：PostgreSQL16事务隔离 https://www.postgresql.org/docs/16/transaction-iso.html 、River重试 https://riverqueue.com/docs/job-retries 、OWASP对象授权 https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html 。用于核对并发/授权原则；本仓实际锁与 Port 是实施依据，不导入额外平台。

回退仅本地候选撤回或最小代码回退，不生产数据回写；无需重新申请父 brief 授权。最终交付 clean committed 独立分支、准确 head/tree/base、实际证据、复用边界与未验收项供上线决定。Library 使用本轮新身份，不覆盖上一轮最终发布事实。

## 已证实缺陷与候选范围

商品旧分享归档后撤销、公开读取期间撤销竞争，MemberGrid/Coupon规则/disabled ExternalPush跨actor事件键，Coupon真实OneID合并额度和历史receipt，公共领券卡住/换身份/续签恢复，以及合并期间签发会话在拆分后的current subject均有先红证据。修复复用既有Owner、Identity Port、UoW与receipt编码，不重写历史owner、Payment session、邀请token或已完成事实。公共body仍严格空对象，RecoveryOnly仅核对当前可信主体的原receipt、miss不新增领券。

Referral已关闭活动停用/截止、奖励撤销竞争、actor键、disabled-WeChat配置native fanout、合并后双参与/双积分；Distribution改为同UoW当前支付退款事实、收窄不必要join写锁、晚到退款处理时钟、同证据异常幂等。EER补既有过期owner投影、返回错误保留原River job、明确不支持unknown-generation核对为409，自动化取消终态不复活。Survey保留不可变handle防拆分后错归属，时间线按当前lineage读取、OAuth proof/UX return按原state隔离；本轮0223新增列的readiness配套遗漏也已红绿修复。

已存在两项Payment app边界退款红未修：对应退款回调对抗子任务被平台终止，精确trigger未披露；HTTP/PG仅夹具构建失败，没有业务验收。保留外部原测试、日志和阻塞说明，不换路径重跑，不把它们混入已关闭候选。没有Payment app/Provider实现修改，独立EER owner adapter0探针及nominal合成paid fanout不代表退款回调通过。

验证短清单在docs/release-checks.json；本轮平台环境和实际浏览器分配见2026-10-06-sol61-validation.md。具名执行证据、每条复现/根因/最小修复/红绿结果和未覆盖矩阵在本机独立sol61-evidence报告，最终准确commit/tree由外部最终收据绑定。库存、问卷指定员工受众保持退役；真实Provider/资金/客户效果/设备、River末次硬崩溃和清理、历史双参与业务处理、模型inflight取消等未声称覆盖。候选仅供本地评审，不自动推送或上线。
