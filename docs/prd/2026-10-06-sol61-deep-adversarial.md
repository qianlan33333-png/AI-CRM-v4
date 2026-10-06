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

验证短清单在docs/release-checks.json；本轮平台环境和实际浏览器分配见2026-10-06-sol61-validation.md。具名执行证据、每条复现/根因/最小修复/红绿结果和未覆盖矩阵在本机独立sol61-evidence报告，最终准确commit/tree由外部最终收据绑定。库存、问卷指定员工受众保持退役；真实Provider/资金/客户效果/设备、River清理、历史双参与业务处理及真实AI review store未声称覆盖。原末次中断与模型inflight取消的后续有限证据见下文，不能外推真实效果。候选仅供本地评审，不自动推送或上线。

## 9a之后限定闭环（fbb历史范围与收据）

父任务进一步授权独立高风险补充，继续仅本地候选、不push/merge/deploy。9a提交、保护ref和通过收据保留；PAY对抗动作继续暂停，不由其他代理或目标重启。补充仅验证：Survey成功原key在真实merge/revert/handle退役后授权与持久事实、实际Chromium重新授权；Automation模型请求inflight时CancelRun提交后的迟到成功/unknown不能复活run或review；非支付River末次自有子进程中断与rescuer恢复状态。最后一项只用私有schema与无联网合成adapter，实际红则保留边界、停止runtime修改。Distribution不同promoter互惠资格/买家付款锁序仅只读源审，不执行付款/DB/修复；不修历史多参与数据。

这些补充涉及现有OneID、Owner UoW和EER/River；只复用当前Port、组合根和Owner测试设施。没有新身份匹配、队列或恢复框架。未发现实际失败前不改实现，精确受影响具名检查随最终本地提交，9a未变运行输入收据按实际blob证明复用；支付未闭环或River实际红均禁止将本轮称全绿。

限定收尾结果：Survey两条成功cached原command在身份退役后拒绝且receipt为空，fresh只验证ResolveSession anonymous；真实merge/revert保留历史A/B owner，B原receipt可恢复而当前A读取结果403。Chromium两显示模式重新授权通过，限本机源码Host/合成OAuth。Automation实际EER请求inflight后CancelRun提交，迟到executed/unknown均不复活run/review，既有全文/digest/unknown和原EER调用事实保留；intake为spy0，真实AI review store未测。两项只新增具名组合测试，无runtime修复。

非支付River新增实际未修缺陷：私有job设max_attempts=1，自有子进程在实际EER claim提交后无联网adapter中self-exit86；只老化该job attempted_at与原lease以加速真实rescuer。rescuer discarded，EER/original attempt仍attempted且过期、Worker/adapter重启调用0、Owner投影0、generic Retry冲突。一次52字符私有schema导致NOTIFY channel超长为夹具失败，缩短私有前缀后才取得业务红，两日志保留。未修runtime，实验源外部保留不纳入必绿清单；30天cleanup、真实Provider和完整默认25次前置尝试未执行。Distribution互惠资格/买方Payment反序仅条件可达源推断，仍未动态证实/未修。

## A7已证实非金融恢复缺陷的最小修复

父任务进一步授权关闭A7，范围只限已复现非金融合成任务；没有安全阻断，PAY继续暂停，不动态验证分销，不push/merge/deploy。fbb提交及Library v3和两次River原日志/原test字节保留。OneID不涉及：只操作不含用户标识的effect/job绑定。Persistence复用现有River周期任务与同PG事务的原JobRetryTx，执行仍走原Worker/RunAttempt；不新增队列、lease/fence、幂等键或效果状态机。

最小实现：周期维护仅扫描当前generation的过期attempted且非payment Owner，effect行锁内经platform验证原job kind/queue/args、discarded且最后为native rescuer记录，在同事务用JobRetryTx唤醒原job。原RunAttempt只将原过期attempt转unknown并同UoW投影Owner，不再次执行Provider。已有executed/unknown/cancelled、active lease、原job completed/cancelled/不匹配绑定均不复活；并发扫描至多一次重排。每分钟及RunOnStart复用既有River调度，避免startup扫描早于rescuer留下永久缺口。不涉及新增业务限制；payment排除来自父任务的明确暂停范围，金融恢复仍未验收。

五项影响：HTTP/事件合同不变；业务机制只恢复非金融末次中断原任务，unknown仍禁止盲重试；直接关联EER/平台River模块注册及composition周期列表/Owner completion，相关Automation取消事实；无页面改动；在原实际红之上验证实际native rescuer/周期/PG红转绿、同job及事实保留、并发和负控，准确final commit只跑受影响具名合同/API build，未变浏览器不重跑。回退为撤回该局部源码候选，无生产/历史数据回写或新权限。

实际先红后绿：原native rescuer末次中断红保留；新源码的真实子进程中断、rescuer与周期恢复已PASS，原job completed，EER/原attempt outcome_unknown，Worker重入1、adapter调用0、Owner投影1，generation/fence/attempt_count仍1。16个终态/误绑定/取消负控、同事务回滚及两个并发扫描单次原job重排均PASS0skip；准确冻结提交的受影响复验收据在外部A7_FIX_RESULT.json，不把这些预冻结绿冒充最终HEAD运行。热点job行等待、扫描整批effect锁、已清理原job、完整25次前置尝试与OS signal kill仍未覆盖或未消除。
