# 发布指挥台

指挥台验证开发 handoff，维护串行发布队列，向原始任务返回失败，并只晋级已验收的同一包。它不编辑候选源码、PR、分支或 worktree。

AI-CRM-v4 是公开仓库，`main` 使用 required check、严格同步、管理员约束、禁止 force push/删除和会话解决要求。GitHub 保护负责合并门禁；指挥台队列负责一次一个候选、预发布构建、生产晋级和观察。当前不使用 GitHub 原生 Merge Queue。

预发布机访问 GitHub 可能超时，因此指挥台从 GitHub REST 读取准确公开 `main`，用本地 Ed25519 SSH key 签发短时 freshness attestation。预发布只接收 source bundle、证明和固定 allowed-signers 公钥，离线验签并运行 `git bundle verify`；它不保存 GitHub token 或长期代理。构建完成后，晋级证明再绑定同一 package SHA-256。

```sh
python3 scripts/release_freshness.py issue ...
python3 scripts/release_freshness.py verify ...
python3 scripts/release_control.py handoff validate handoff.json
python3 scripts/release_control.py --state /secure/release/state.json   --coordinator-thread-id <thread-id> handoff submit handoff.json <candidate-id>
python3 scripts/release_events.py --state /secure/release/state.json show
```

完整状态机、通知桥接、同包晋级和失败分类见 [发布指挥台运行合同](operations/release-coordinator.md)。
