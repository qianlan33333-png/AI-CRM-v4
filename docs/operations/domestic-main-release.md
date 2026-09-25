# CRM v4 国内主仓发布

**生效条件：**预备机 `/var/lib/aicrm/domestic-main/state.json` 有通过 `verify` 的激活基线，生产机 `/opt/aicrm/domestic-main/state.json` 的 `main_sha/main_tree` 与其一致，且旧 `aicrm-domestic-release.timer` 和 service 均停止。缺少任一项继续按[旧流程](domestic-release.md)，不得启动新发布器。发布失败由一个执行者处理。

## 日常四步

1. 开发者先从预备机抓取国内 `main`，以它的准确 SHA 新建独立 worktree/`codex/<work-item>` 分支，再提交代码、相关测试和简短变更说明。只推到预备机 `/opt/aicrm/domestic/source.git` 的 `refs/heads/codex/*`。受限推送账号没有 shell、`main` 写权或生产密钥；本机可能落后的 GitHub `main` 不能作为新分支基线。
2. 将准确 branch、head、base 登记给单一发布器。base 必须等于登记时的国内 `main`，并位于候选的第一父链；若别的线先上线，原开发线将改动更新到新的国内 `main` 上并重新检查，发布器不 rebase 或解决冲突。
3. 发布器在锁内检查准确 SHA/tree，按影响范围运行必要测试，预备机构建并安装，再运行受影响的合成业务合同。未知路径、迁移、共享基础和检查策略变更保守全量。预发失败停在该候选。
4. 生产端先保存、验证可从空仓恢复的完整源码 bundle；之后才接受预发同一安装包。生产版本、完整文件摘要、服务和 `/readyz` 读回通过，发布器才以旧 SHA 为条件推进国内 `main`。每条线按队列顺序累计上线。纯文档提交只备份源码、推进源码游标，不重装应用。

普通页面或程序发布不备份数据库；生产迁移前按受保护主机角色备份，预备机只有可重建的合成数据。真实支付、扫码等业务结果另行验收，不能用技术健康代替。

## 人工 GitHub 归档

GitHub 凭据只在用户电脑。`scripts/manual_github_sync.py` 默认只读展示 GitHub 已同步 SHA、待同步提交和生产源码收据；仅显式 `--execute` 才尝试普通快进推送。它核对国内 `main` 等于生产 `main_sha/main_tree`，另核对最近一次应用安装收据、完整摘要和源码祖先。GitHub 若前进、分叉或推送后读回不符即停止，绝不强推。**无自动推送，也无固定同步周期；未同步不阻塞下一次技术发布。**

激活后，在含新同步脚本的本机 V4 工作树配置 `domestic` 远端为 `aicrm-release-push@aicrm-v4-stage-source:/opt/aicrm/domestic/source.git`，从该工作树运行命令。先用默认预览；只有你决定归档时，给同一命令追加 `--execute`。`--stage-host` 使用受限账号别名，`--production-host` 使用已验证的生产入口；两者均须有持久 Host Key pin。退出码 3 表示 GitHub 已读回但预备机 ACK 待核对，退出码 4 表示推送后 GitHub 读回失败、结果不明；两者均不能盲目重推。

```sh
python3 scripts/manual_github_sync.py --repo "$PWD" --production-host 124.220.53.183 --stage-host aicrm-v4-stage-source --ssh-key /Users/qianlan/Downloads/zhengshi.pem --known-hosts /Users/qianlan/.ssh/known_hosts
```

最新已验证的完整源码 bundle 留在生产机 `/opt/aicrm/source-backups/`，可用于预备机故障后恢复国内 `main`。在没有已验证的新 bundle 前不得删旧 bundle。两台国内机器在人工归档前同时丢失，GitHub 不保证找回期间代码。

## 停止与恢复

- 候选 SHA/tree、base、影响检查、预发合同或 bundle 摘要不符：停止，不触碰生产或国内 `main`。
- 生产安装明确失败：依安装器收据回滚到上一技术版本，并读回健康。数据库迁移不靠反向恢复真实库。
- 安装结果不明：停队列，只读对账生产 current、安装收据、源码 bundle、完整摘要、服务和健康。若已准确安装且健康，但国内 `main` 尚未推进，`reconcile` 只在原 SHA/tree 与收据全匹配时完成原本的 CAS；绝不重装。
- 预备机故障：在生产机只读核对最新 bundle、marker 和安装收据，将 bundle 恢复到新的空裸仓并比对 SHA/tree，然后重新建立受限推送与单一发布器。不能从可能落后的 GitHub `main` 静默覆盖生产源码。

切换演练须覆盖双分支顺序、过期基线、预发失败、生产健康失败、结果不明、bundle 空仓恢复、人工同步快进和远端分叉；2 核、2GB 预备机还须实测构建稳定性。未通过不得激活。
