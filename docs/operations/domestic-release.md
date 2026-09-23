# CRM v4 国内构建与内网串行发布

新流程的唯一源码是 GitHub `qianlan33333-png/AI-CRM-v4`。PR 在 GitHub 审核；受保护 `main` 的准确提交 `check` 成功后，上海预备机 `10.0.4.6` 每两分钟按第一父链顺序处理。GitHub Actions 不生成或传输生产包。预备机本地构建并安装，基础健康通过后，`rsync --link-dest` 经内网将完整文件树的变化部分传至 `10.0.4.13`。生产重新校验完整摘要、原子切换和 `/readyz.release_sha`，成功后推进技术游标。后一个版本包含前一个版本。

**当前启用条件：** PR #22 维持暂停。先完成本文“切换”中所有核对，再把配置的 `production_enabled` 置为 `true` 并启用 timer。仅安装工具或启动预备机演练不会发布生产。

## 开发和 PR

新能力各用一个 PR；共享代码改动应协调。CI 保留稳定的必需 `check`：页面改动跑前端与相关测试，Go 改动跑受影响测试，未知、共享基础设施、迁移、构建/部署改动跑完整检查。PR 仅靠准确 head 的当前检查结果合并；部署器重新核对合并提交的 `check`。GitHub 不运行生产包构建，不使用原生 Merge Queue。

迁移必须前向兼容；破坏性变更在 PR 检查中拒绝。普通页面改动绝不触发数据库备份或 `aicrm-migrate.service`。生产安装完成只表示准确版本健康；真实支付、扫码结果另行登记，不占据后续技术发布队列。

## 预备机配置

机器需 Ubuntu x86_64、与 `go.mod` 一致的 Go 1.26.6、Node 24.18.0、PostgreSQL 16、`rsync`、`pg_dump`，并为 Go/npm 配置持久缓存。数据库用合成数据，禁止从生产复制。2 核 2GB 先测完整基线及峰值；不足则扩到 4 核 8GB 后再启用自动生产。构建临时工作树必须与输出目录位于持久磁盘；预备机的 1GB `/tmp` 内存盘不足以容纳主程序链接输出。GitHub 源码拉取必须持续可用，且 `origin` 指向官方 GitHub 仓库。官方 API 读取 main SHA 与检查；不可用时停队列，不从未校验镜像自动发布。

将独立源码库放在 `/opt/aicrm/source`，工作目录放在 `/opt/aicrm/domestic`，并由非 root 构建用户持有。配置示例为 [`deploy/domestic-release.example.json`](../../deploy/domestic-release.example.json)。固定的 `scripts/domestic_release.py`、`scripts/domestic_release_build.py` 放入 `/usr/local/libexec/aicrm/`，固定的 `deploy/domestic-promote.py` 以 root:root、0755 放在两台机器的同一路径。构建用户通过 sudo 调用固定安装器；当前预备/生产 SSH 账户 `ubuntu` 已有 sudo 权限，后续可收窄为专用部署账户。生产内网 SSH 使用专用密钥和经过核对的固定 Host Key，不关闭 Host Key 验证。`GITHUB_TOKEN` 如需提高 API 限额只给预备机检查查询使用，不能写入包。

预备机安装同一完整目录到本机 `/opt/aicrm/current`，使用合成库验证受影响资源、服务和 `/readyz.release_sha`。首次预备机空环境会初始化合成库和服务单元；后续非迁移提交不运行迁移。生产接收区 `/opt/aicrm/domestic-incoming` 由传输账户持有；安装器在共享 `/opt/aicrm/install-release.lock` 下校验清单、当前 base SHA 与服务。迁移提交先做 `pg_dump -Fc` 备份，再运行迁移服务；非迁移提交跳过这两步。健康失败时切回上一版本并验证，数据库迁移保持前向兼容；若远程结果不明，状态置为 `outcome_unknown`，只读对账后人工明确结论，绝不盲目再次安装。

## 切换

1. 暂停旧 GitHub Actions `deploy` 入口及旧指挥台生产写入口。保留旧账本只读供审计。两条发布路径共享主机锁，但切换期间仍需确保只有新定时器可以发起安装。
2. 核对生产 `/opt/aicrm/current/release.env`、`/readyz`、原安装收据和完整清单。核对安装的 preview commit 与当前 GitHub `main` 的 Git tree 相同。旧观察待办不改写成真实业务验收完成。
3. 在预备机从该 `main` 做一次完整基线构建、文件清单和合成数据安装；记录实际构建时间、峰值内存及磁盘。运行固定安装器的摘要失败、基础健康失败及回滚演练。
4. 在 `production_enabled=false` 下执行 `python3 /usr/local/libexec/aicrm/domestic_release.py --config /etc/aicrm/domestic-release.json bind-baseline --prod-preview-sha <生产当前SHA>`。它读取生产状态和旧收据，核对双 commit 同树与预备机基线，建立一次性游标；状态已存在则拒绝覆盖。
5. 两个模拟 PR 连续合并的第一父链、页面无备份/无迁移、Go 受影响程序编译、摘要不符、预发失败、生产健康失败和结果不明的测试全部通过；实测 `.6 -> .13` 的 SSH 身份认证及增量传输。随后设置 `production_enabled=true` 并启用 `aicrm-domestic-release.timer`。记录首个真实合并到生产健康的耗时。

`state.json` 只记录技术游标：`processed_sha`、`deployed_source_sha`、`prod_installed_sha`、`status`。它不等于旧 `/Users/qianlan/Downloads/新CRM/release-control/state.json`，也不记录真实业务验收。状态缺失或损坏、检查未完成、源码拉取失败、基础/生产失败时默认停止。`outcome_unknown` 用 `readback` 子命令核对现场，再按事故处置更新状态，不能删除状态从头跑。

## 回退与局限

静态文件也经当前单体服务提供，因此版本切换后会重启 API 和 effects worker 以让 `/readyz` 报新 SHA；它不重新编译无关程序。若改动涉及系统服务定义、密钥或商户平台设置，需要单独的运维变更；代码发布器不改商户平台配置。迁移只能前向兼容，代码技术回退不会还原数据库。PR #22 不由本流程自动合并。
