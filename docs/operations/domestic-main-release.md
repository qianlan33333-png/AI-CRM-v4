# CRM v4 国内主仓发布

**生效条件：**预备机 ledger 与生产源码游标的 `main_sha/main_tree` 一致；预备机和生产机已安装应用的 SHA、tree、manifest 相互一致且健康。已验证的源码 `main` 必须以该应用 SHA 为 first-parent 祖先；应用提交到所选 `main` 之间只能有工具/文档等不改变运行时的提交，baseline 绑定所选 `main` 的准确 SHA/tree。预备机旧 `aicrm-domestic-release.timer` 和 service 均停止；生产机没有旧/新 domestic release units，必须读回 `not-found`。预备机固定工具与 systemd 单元的摘要对应准确 `main`。缺少任一项继续按[旧流程](domestic-release.md)，不得启动新发布器。发布失败由一个执行者处理。

> **当前未启用。** 这次 partial-bootstrap 恢复必须先只读确认裸仓仍为 `main=291baa2d13864c3a60f3ed93e08382c3e598db33`、tree=`3c17b8a86e2e69ed4f6942304300c609300fb077`，且 app=`960b30e9406fae2045aeb7ef5dce863976407727` 在其 first-parent 链上、`runtime_changed=false`。恢复只补 hook/权限，不移动 main、不建 ledger；`prepare-baseline`/`activate` 也以 291 为 baseline。准确 PR #46 head 之后作为第一个普通 controller-only 候选串行处理。旧固定 291 controller 先对该 SHA 执行 `maintenance-check`，再由从同一已验证 bundle 导出的精确候选脚本重复检查并写 marker；对照两次的 candidate SHA/tree/base、controller 文件清单、toolchain、lane 集合与全通过结果，raw receipt SHA 会因时间、用时和日志路径而不同。两机 app SHA/tree/manifest 与健康、旧队列结果和 timer 状态仍须按 bootstrap 清单读回。GitHub `main` 暂停在 `6d3ee9c`，供之后人工快进归档；无需先合入 PR #46。

## 一次性主机准备与切换

国内主仓尚未启用。一次性恢复、baseline、首个候选维护检查和激活命令集中在[一次性主机准备与切换清单](domestic-main-bootstrap.md)。本次国内初始 `main` 固定为 partial repo 已验证的 291；精确 PR #46 head bundle 用于导出候选脚本/工具，不用于重建或覆盖 source.git。固定 291 controller 保留到 baseline 后首个 candidate 通过旧规则与新脚本双重 maintenance-check，marker 写入且新 controller 精确安装后，才由普通候选处理推进 main。旧/new timer、双机 app identity 与无结果不明发布项等其余门禁不变。

## 日常四步

1. 开发者先从预备机抓取国内 `main`，以它的准确 SHA 新建独立 worktree/`codex/<work-item>` 分支，再提交代码、相关测试和简短变更说明。只推到预备机 `/opt/aicrm/domestic/source.git` 的 `refs/heads/codex/*`。受限推送账号没有 shell、`main` 写权或生产密钥；执行候选测试的 `aicrm-build` 账号不得加入可写 Git 对象库的推送组。本机可能落后的 GitHub `main` 不能作为新分支基线。
2. 将准确 branch、head、base 登记给单一发布器。base 必须等于登记时的国内 `main`，并位于候选的第一父链；若别的线先上线，原开发线将改动更新到新的国内 `main` 上并重新检查，发布器不 rebase 或解决冲突。
3. 发布器在锁内检查准确 SHA/tree，按影响范围运行必要测试，预备机构建并安装，再运行受影响的合成业务合同。未知路径、迁移、共享基础和检查策略变更保守全量。预发失败停在该候选。
4. 生产端先保存、验证可从空仓恢复的完整源码 bundle；之后才接受预发同一安装包。生产版本、完整文件摘要、服务和 `/readyz` 读回通过，发布器才以旧 SHA 为条件推进国内 `main`。每条线按队列顺序累计上线。纯文档提交只备份源码、推进源码游标，不重装应用。

普通页面或程序发布不备份数据库；生产迁移前按受保护主机角色备份，预备机只有可重建的合成数据。真实支付、扫码等业务结果另行验收，不能用技术健康代替。

## 人工 GitHub 归档

GitHub 凭据只在用户电脑。`scripts/manual_github_sync.py` 默认只读展示 GitHub 已同步 SHA、待同步提交和生产源码收据；仅显式 `--execute` 才尝试普通快进推送。它核对国内 `main` 等于生产 `main_sha/main_tree`，另核对最近一次应用安装收据、完整摘要和源码祖先。GitHub 若前进、分叉或推送后读回不符即停止，绝不强推。**无自动推送，也无固定同步周期；未同步不阻塞下一次技术发布。**

首次启用人工归档前，仓库管理员须调整 GitHub `main` 的有效 branch protection/ruleset：只允许指定归档维护者直接更新；移除会阻止该维护者直接快进推送的 PR 和 required-check 门槛；继续禁止 force push 与删除。核对 branch protection 和 ruleset 的合并生效结果，并确认普通快进路径可用后再归档。不得通过强推、临时关闭整条保护规则或扩大到所有用户写权限来绕过拒绝。

激活后，在含新同步脚本的本机 V4 工作树配置 `domestic` 远端为 `aicrm-release-push@aicrm-v4-stage-source:/opt/aicrm/domestic/source.git`，从该工作树运行命令。先用默认预览；只有你决定归档时，给同一命令追加 `--execute`。SSH config 应让 production `124.220.53.183` 使用固定运维 SSH identity（该 key 本身不具只读权限，仅用于执行 `domestic-promote.py --read-domestic-main` 只读查询），让 `aicrm-v4-stage-source` 使用独立的 stage archive-ack key 和已审查的 ProxyJump/ProxyCommand；两条配置的 HostName、user、port 必须匹配各自 allowlist。若设置 `HostKeyAlias`，该 alias 必须有持久 pin；未设置时，`HostName` 必须有持久 pin，`ssh -G` 输出 `HostKeyAlias none` 可以接受。`_resolve_ssh_target` 会解析 SSH alias、拒绝不匹配的 endpoint/user/port，并按 HostKeyAlias（若设置）或 HostName 检查 pin。stage 强制命令只接受格式精确的 `domestic-archive-ack --sha <40位小写 SHA>`，SHA 通过 stdin 进入固定 `archive-ack-stdin --config /etc/aicrm/domestic-main-release.json` endpoint；sudoers 只授权固定参数命令，不接受附加 argv。**不要给这条命令传 `--ssh-key`**：该参数会同时覆盖 production 与 stage 的 SSH 身份。用 `ssh -G 124.220.53.183` 和 `ssh -G aicrm-v4-stage-source` 先读回两条配置的 IdentityFile、ProxyJump/ProxyCommand、HostName、User、Port 和 HostKeyAlias，并确认 HostName 或配置的 HostKeyAlias 对应持久 known_hosts pin。退出码 3 表示 GitHub 已读回但预备机 ACK 待核对，退出码 4 表示推送后 GitHub 读回失败、结果不明；两者均不能盲目重推。

```sh
python3 scripts/manual_github_sync.py --repo "$PWD" --production-host 124.220.53.183 --stage-host aicrm-v4-stage-source --known-hosts "$HOME/.ssh/known_hosts"
```

最新已验证的完整源码 bundle 留在生产机 `/opt/aicrm/domestic/source-backups/`，可用于预备机故障后恢复国内 `main`。在没有已验证的新 bundle 前不得删旧 bundle。两台国内机器在人工归档前同时丢失，GitHub 不保证找回期间代码。

## 停止与恢复

- 候选 SHA/tree、base、影响检查、预发合同或 bundle 摘要不符：停止，不触碰生产或国内 `main`。
- 生产安装明确失败：依安装器收据回滚到上一技术版本，并读回健康。数据库迁移不靠反向恢复真实库。
- 安装结果不明：停队列，只读对账生产 current、安装收据、源码 bundle、完整摘要、服务和健康。若已准确安装且健康，但国内 `main` 尚未推进，`reconcile` 只在原 SHA/tree 与收据全匹配时完成原本的 CAS；绝不重装。
- 预备机故障：在生产机只读核对最新 bundle、marker 和安装收据，将 bundle 恢复到新的空裸仓并比对 SHA/tree，然后重新建立受限推送与单一发布器。不能从可能落后的 GitHub `main` 静默覆盖生产源码。

切换演练须覆盖双分支顺序、过期基线、预发失败、生产健康失败、结果不明、bundle 空仓恢复、人工同步快进和远端分叉；2 核、2GB 预备机还须实测构建稳定性。未通过不得激活。
