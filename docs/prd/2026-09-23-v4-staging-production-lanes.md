# v4 专用预发与生产观察分槽缺陷合同

## 业务判断与参考

现行队列把 `preview_building` 至 `observing` 视为同一占用。旧候选在生产 `observing` 时，独立主机上的专用预发机也不能为新候选安装自验，令共享基线和后续支付入口修复相互阻塞。预发 `49.232.57.128` 与生产 `124.220.53.183` 使用各自主机本地数据库；预发支付/企微外部效果已关闭。此隔离只支持预发自验分槽，不能证明生产可并行晋级。

GitHub 参考：[GitHub Actions 环境并发与保护规则](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments)将环境串行控制独立设置；[actions/checkout PR head 与 merge commit 讨论](https://github.com/actions/checkout/issues/426)提示必须保留准确 head/preview 来源。本仓继续使用自己的发布队列、签名 bundle、预发构建锁与生产晋级锁，不启用 GitHub Merge Queue。

## 分类、复用和范围

OneID 不涉及：没有客户身份操作。Persistence 是现有发布 JSON 状态文件与不可变事件；不改业务数据库。External Effects：队列命令只更新发布元数据，不调用支付或企微；安装与生产部署仍由既有指挥台路径执行。复用 `release_queue.py`、`release_coordinator.py`、`release_control.py prepare_promote` 和预发安装锁。

本修复只允许 `preview_building`/`staging_acceptance` 与旧生产 `observing` 同时存在。预发同槽仍单飞；`frozen`、`waiting_merge`、`merged`、`production`、`observing` 仍互斥。`adopt-accepted` 不得绕过生产占用。`prepare_promote` 不变，仍要求唯一已验收包占用 `waiting_merge`。治理/发布元数据脚本按 operator-only 分类；应用运行时改动仍须 staging receipt 与完整 handoff。

## 验收与回滚

对抗测试需证明：旧生产 observing 时新候选能进入预发并 accepted；第二个候选不能并发占用预发；新候选不能进生产槽；`adopt-accepted` 也不能旁路；原候选状态和包不改写。高风险 CI/发布脚本改动要求准确 head 完整代码 CI、required check 和治理测试。纯治理 PR 的应用 package/install/business readback 为 N/A。回滚本 PR 时恢复旧单槽策略，不更改已存候选记录；生产任何例外须独立治理批准。

预发实际切换前由指挥台核旧 active SHA、包/receipt、保留版本、数据库迁移版本和回滚兼容性；安装使用专用主机锁，虚拟外部效果和隔离认证数据。构建清理采用先盘点引用再在构建锁下应用，保留 current/rollback/观察中及已交接同包和审计元数据；自动清理可另行治理，不是本 PR 范围。
