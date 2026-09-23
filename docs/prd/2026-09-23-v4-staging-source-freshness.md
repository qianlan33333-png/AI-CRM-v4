# v4 预发布构建来源与签名新鲜度修复合同

## 业务判断与缺陷

发布指挥台只能把公开 `qianlan33333-png/AI-CRM-v4` 当前 `main` 与准确 PR head 组成的候选交给预发布。预发布无法稳定访问 GitHub，因此须凭指挥台短时签名证明离线核对来源；旧证明、换包、换候选、错误签名或缺少对象均停止构建。当前构建入口没有消费证明，远端 built receipt 误写 `AI-CRM-v3`，且缓存缺对象时会静默换传未被原证明绑定的完整 bundle。

## 参考与复用

- Git 官方 [git-bundle 文档](https://github.com/git/git/blob/master/Documentation/git-bundle.adoc)：`verify` 检查 bundle 完整性和 prerequisite，增量 bundle 必须在接收端具备先决对象。
- OpenSSH 官方 [ssh-keygen 手册](https://github.com/openssh/openssh-portable/blob/master/ssh-keygen.1)：`-Y sign/verify` 使用 namespace、签名身份和 allowed-signers 校验。
- 仓库已有 `scripts/release_freshness.py` 负责规范 JSON、短时有效期、签名、摘要和 bundle 校验；`scripts/release_candidate.py` 负责候选树，沿用它们，不增加第二套签名格式。

## 修复范围与验收

1. 构建入口强制要求 source attestation、signature 和固定预发布 allowed-signers；在任何 SSH/上传前核对仓库、分支、stage、main/head/preview SHA 与 tree、bundle 摘要、有效期、签名及候选双亲。证明数据与 manifest 不一致时失败关闭。
2. 远端在锁内对收到的**同一个** bundle、签名和候选再次验签，先完成 bundle verify、对象与双亲检查，再运行候选树中的任何脚本。缺 prerequisite 时拒绝，要求指挥台重新签发完整 bundle。
3. built receipt 的 repository 固定为 `AI-CRM-v4`；下游 accepted receipt 校验同样拒绝 v3。保留不可变构建目录、单飞锁、Linux amd64 构建与同包晋级边界。
4. 对抗测试覆盖缺证明、错误仓库/签名/过期/摘要、错误双亲或 tree、增量 bundle 缺 prerequisite、远端未验签前执行、v3 receipt。工具测试和治理检查绑定最终 commit/tree；不伪造 staging package、receipt 或应用部署。

## 架构分类与上线

- OneID：不涉及；无客户身份或归属。
- Persistence：本地发布文件与 Git 对象，无 PostgreSQL 事务或持久业务任务。
- External Effects：构建脚本会 SSH 上传和在 staging 构建，必须在本地验证后才允许发生；不涉及 Provider 写入。
- 权限：签名私钥留在指挥台；预发布只持固定 allowed-signers 公钥文件。证明不携带 GitHub token。
- 回滚：不合并该工具 PR 即保持现状；合并后如失败，只生成新候选和新证明，不复用旧 receipt。
- 并行：支付 PR #3 不在本 PR 范围；部署脚本为共享入口，串行合并由指挥台负责。
