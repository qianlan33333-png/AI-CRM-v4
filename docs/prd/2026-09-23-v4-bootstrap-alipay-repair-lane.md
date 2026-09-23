# 首次 v4 运行时晋级与支付宝入口修复通道合同

## 业务判断与问题

旧生产批次 `6088e57ccf63b62dff46b4e1` 仍在 `observing`。欢迎语拉群、收货地址只有用户陈述通过，缺真实业务读回；支付宝入口不存在，旧商品页实付旅程无法执行。PR #3 修复该入口，不能为了等待旧批次支付宝验收而永远拒绝它，也不能因此把旧批次标为 `released`。用户陈述只记作 `user_statement_only`，支付宝仍为 `not_accepted`。

旧生产 release `4928f94e53a100ffab73132c0f096c4c4ff6e1d4` 的 Git tree 是 `9c788ee796a4bfecc229675bf4e539a16c749166`，与 v4 根提交 `ef0d4a4d25fa79fad6b1043f7efca460daf56de6` 的 tree 一致；旧 release SHA 不在 v4 对象库。此为**内容等价的跨仓引导证据**，绝非 Git 祖先关系。普通祖先门禁保留；仅首个准确 v4 修复候选可使用一次性证据桥。

## 参考与复用评估

- [in-toto Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)：以 subject digest 绑定具体产物；本合同复用精确 SHA 和证据摘要的思想，不引入新签名框架。
- [SLSA GitHub Generator](https://github.com/slsa-framework/slsa-github-generator/blob/main/internal/builders/generic/README.md)：构建产物摘要必须与来源证明绑定；复用现有 `release_freshness.py`、staging receipt 与同包晋级，不复制 PR #4 的来源签名工作。
- [Argo CD](https://github.com/argoproj/argo-cd/blob/master/docs/index.md)：源 revision、live state 与 health 分别核对；本合同保留 production release/readback、队列与业务观察独立。
- 仓内复用 `release_control.py` 的 preview 双亲、`release_handoff.py` 的 accepted package/receipt 校验、`release_queue.py` 和 `release_coordinator.py` 的串行状态、`release_promote.py` 的同包与 `outcome_unknown` 保护。仅扩展一次性元数据验证与精确候选门禁。

## 分类与边界

OneID：不涉及，治理工具不读写客户身份。Persistence：本地 release 状态与不可变收据；无业务数据库事务或持久任务。External Effects：GitHub/生产状态只读校验；生产写仅由指挥台原有晋级器执行。数据 Owner 为发布指挥台；本 PR 不改支付 Provider、构建来源/签名或业务代码。

## 规则与成功标准

1. 例外记录必须绑定旧 candidate/release/tree/package、生产技术 receipt 的 SHA-256，v4 根 commit/tree，当前 main、PR #3 准确 head、双亲 preview/tree、accepted package/receipt、候选 ID、队列 owner 和生产实时 readback。任何字段不一致、证据文件缺失或过期均拒绝。
2. 入队与生产晋级分别需要明确、可追溯的用户决定。入队批准仅允许准确 PR #3 候选与旧 observing 批次共存；生产批准另行绑定同一候选、包和当前活跃生产读回。任务创建、PRD、CI 或用户确认入口缺陷均不等于批准。
3. 旧观察保留原状态和未验收项；新修复候选另建观察。新观察记录 `repairs_observation_id`，旧观察记录 `repaired_by_candidate_id`，不能覆盖原证据。其他候选继续被旧观察锁阻挡。
4. 只允许一个首次 v4 运行时修复候选；例外一旦消费、回滚或 `outcome_unknown`，不可切换候选或复用以发第二次。新候选须重做完整普通门禁，必要时重新取得新业务决定。
5. 晋级仍是同一个 staging accepted package；合并后 main tree、当前 head/preview、required checks、预发布技术与受影响旅程读回、生产 active release/readback 必须逐一核对。`outcome_unknown` 停止重试，先只读对账；回滚到旧活跃包须记录旧/新观察和支付影响，不改写旧批次为成功。
6. 真实支付宝商户配置、商品页选择、实付、回调验签与幂等、原单查单、退款及状态读回属于用户后测。技术部署只到 `observing`。欢迎语与地址的用户陈述继续保持较低收据级别，直到独立真实读回补齐。

## 验收与回滚

对抗验证覆盖：非 PR #3、错旧 release/tree/package/receipt、错 v4 root、main/head/preview 移动、错 accepted package、缺 owner/批准、旧批次非 observing、第二个活跃候选、重复消费、读回变更、伪造祖先、未知结果与回滚。纯治理 PR 的应用包、staging install 和业务运行时读回为 N/A；治理检查与准确 Git HEAD/tree、完整所需 CI 单独交付。指挥台负责最终批准、合并、部署与观察。
