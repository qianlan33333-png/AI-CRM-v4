# CRM v4 国内主仓发布

本次周期详情、退款回调、推荐海报和自动化配置应用已发布为 `00e248605b7cc0cd5b481b091bf12a6d23688f12`。应用发布不等于国内源码基线或发布器已启用：只有主机读回的 `prepare-baseline`、`activate`、`verify` 收据及精确 SHA/tree、双机应用身份、固定工具摘要和健康结果全部吻合，才可报告基线已激活。本文不预设 timer 已启用；GitHub 归档始终由用户明确发起，无自动推送或固定同步周期。

国内主仓的一次性基线对齐见[主机基线对齐清单](domestic-main-bootstrap.md)。日常发布只使用该清单完成后的准确国内 `main`；不要从旧的 291/960 partial-bootstrap 说明推断当前状态。

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

任何 SHA/tree、收据、bundle、固定工具、预发合同或生产健康结果不符，停止队列并只读对账。发布器的检查、构建和健康边界不得凭本文缩减；完整一次性初始化步骤见[主机基线对齐清单](domestic-main-bootstrap.md)。

首次启用验收仍覆盖双分支顺序、过期基线、预发/生产失败、结果不明、bundle 空仓恢复、人工快进和远端分叉；SHA/tree、工具链与范围一致的既有 controller 合同和 00 主机收据可复用。D 提交自身仅改文档，00→D 累计链还包含 C 的两项已审查工具修复；不因该文档提交重跑全套业务应用。
