# 首个 v4 同包批次跨历史桥接 PRD

## 业务判断

旧生产候选 `6088e57ccf63b62dff46b4e1` 仍处于 `observing`，活跃 release 为 `4928f94e53a100ffab73132c0f096c4c4ff6e1d4`。旧仓 release 与 v4 orphan root `ef0d4a4d25fa79fad6b1043f7efca460daf56de6` 的 Git tree 都是 `9c788ee796a4bfecc229675bf4e539a16c749166`。这证明内容起点相同，不建立 Git 祖先关系，也不证明真实业务已验收。普通生产祖先校验必须保留；首个 v4 运行时批次只能经一次性、候选专属证据桥通过。

拟议首包同时含共享 CI 基线 PR #15、支付宝入口 PR #3、群邀请换码 PR #13；PR #17 预发分槽治理若先合并可作为基线，若纳入同包则必须作为显式成员。最终成员、顺序及各 head/tree 均在冻结批次时实时读取 GitHub，本文不预填近期变化的 head。现有 PR #5 只服务 PR #3，不能作为本批次授权或复用其消费标记。PR #17 的预发并行规则不授权并行生产晋级。

## 参考与复用

- [Git `merge-tree` 对无共同历史的限制](https://github.com/git/git/blob/master/Documentation/git-merge-tree.adoc)：无祖先时不能把 tree 相同冒充正常 merge；桥只替代首包的这一项祖先断言。
- [GitHub protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)：保留 strict required checks、受保护 main 和串行指挥台合并，不启用 GitHub Merge Queue。
- [actions/attest](https://github.com/actions/attest)：精确绑定来源与产物摘要；本仓复用现有 freshness attestation、staging receipt 和 package SHA-256，不引入第二种签名机制。
- 仓内复用 `release_batch.py`/`release_deferred_acceptance.py` 的成员与收据合同、`release_control.py` 的 merge preview 和晋级准备、`release_promote.py` 的同包与生产锁。桥接校验独立实现，避免改动 PR #17 的预发队列逻辑。

## 范围与分类

OneID：不涉及，桥不处理客户身份。Persistence：仅发布控制文件内的不可变桥消费记录，数据 Owner 是发布指挥台；不改业务数据库。External Effects：GitHub API 和生产状态只读核对；实际生产写仍须原有预发节点、固定 Host Key、同包、锁和晋级器。本文和桥接校验均不授权支付或企微 Provider 效果。

桥只为**一个**首包 candidate ID、精确的成员集合及顺序、每个成员当前 GitHub PR head/tree、联合 preview/tree、staging accepted package/receipt、旧生产 candidate/release/tree/package/receipt 与当前生产只读读回成立。变更任一 head、base、preview、包或收据即需新候选与新授权；不能沿用旧记录。旧 observing 原样保留，新候选独立观察；不得将 ack、技术健康、fixture 或预发虚拟效果写成真实业务验收。

## 输入、权限与状态

桥接 manifest 由指挥台在批次冻结时生成，包含唯一 bridge ID、GitHub 仓库、旧/新基线、批次 manifest 摘要、成员 PR URL/head/tree、当前 main、联合 preview/tree、package SHA-256、accepted receipt 摘要、旧生产 receipt 与实时 readback 摘要、队列 owner、授权来源及失效时间。成员至少精确包含 #15、#3、#13；#17 是否进入由最终批次显式决定，不能默许额外成员。GitHub PR 必须 open、指向同一 v4 main，并按实时 REST head 校验。所有引用文件逐字节核 SHA-256；校验失败关闭。

授权分两段：候选入队和生产晋级分别须有与同一 candidate/package 绑定的用户决定；仅创建任务或批准 PRD 不等于晋级批准。首包入队不能改变旧观察状态；生产晋级须确认旧 active release 仍为原 SHA、旧观察仍在、没有另一生产占用者，并在网络写之前原子记录消费。任何 `outcome_unknown` 停止重试只读对账；消费记录不可删除或转移到另一 candidate。旧批次的延期业务项由原 Owner 后续补验，首包新增两项业务也分别验收。

## 验收、上线与回滚

对抗用例覆盖：错旧 tree/receipt、错 v4 root、未知成员/额外 PR、PR head 移动、head tree 不符、preview 双亲错、包/accepted receipt 不符、旧观察消失、第二生产占用者、重复消费/改绑、授权缺失或过期、读回过期或变更、`outcome_unknown`。首次批次仍须完整当前 head CI、Linux amd64 预发同包构建、受影响旅程虚拟自验、真实技术收据；合并后 main tree 必须等于 accepted candidate tree。生产部署后只到 `observing`，真实业务验收后才可 `released`。

治理 PR 的 runtime package、staging app install、业务运行时 readback 为 N/A；提交当前 commit/tree、治理专项与完整 CI 证据。回滚治理代码使用 Git revert，但已消费的桥接记录与旧/新收据保持可审计，不借回滚重发另一候选。
