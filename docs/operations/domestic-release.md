# CRM v4 国内构建与内网串行发布

新流程的唯一源码是 GitHub `qianlan33333-png/AI-CRM-v4`。PR 在 GitHub 审核；受保护 `main` 的准确提交 `check` 成功后，上海预备机 `10.0.4.6` 每两分钟按第一父链顺序处理。GitHub Actions 不生成或传输生产包。预备机本地构建并安装，基础健康通过后，`rsync --link-dest` 经内网将完整文件树的变化部分传至 `10.0.4.13`。生产重新校验完整摘要、原子切换和 `/readyz.release_sha`，成功后推进技术游标。后一个版本包含前一个版本。

**当前启用条件：** PR #22 维持暂停。先完成本文“切换”中所有核对，再把配置的 `production_enabled` 置为 `true` 并启用 timer。仅安装工具或启动预备机演练不会发布生产。

## 开发和 PR

新能力各用一个 PR；共享代码改动应协调。CI 保留稳定的必需 `check`：页面改动跑前端与相关测试，Go 改动跑受影响测试，未知、共享基础设施、迁移、构建/部署改动跑完整检查。PR 仅靠准确 head 的当前检查结果合并；部署器重新核对合并提交的 `check`。GitHub 不运行生产包构建，不使用原生 Merge Queue。

迁移必须前向兼容；破坏性变更在 PR 检查中拒绝。普通页面改动绝不触发数据库备份或 `aicrm-migrate.service`。生产安装完成只表示准确版本健康；真实支付、扫码结果另行登记，不占据后续技术发布队列。

## 预备机配置

机器需 Ubuntu x86_64、与 `go.mod` 一致的 Go 1.26.6、Node 24.18.0、PostgreSQL 16、`rsync`、`pg_dump`，并为 Go/npm 配置持久缓存。数据库用合成数据，禁止从生产复制。2 核 2GB 先测完整基线及峰值；不足则扩到 4 核 8GB 后再启用自动生产。构建临时工作树必须与输出目录位于持久磁盘；预备机的 1GB `/tmp` 内存盘不足以容纳主程序链接输出。GitHub 源码拉取必须持续可用，且 `origin` 指向官方 GitHub 仓库。官方 API 读取 main SHA 与检查；不可用时停队列，不从未校验镜像自动发布。

将独立源码库放在 `/opt/aicrm/source`，工作目录放在 `/opt/aicrm/domestic`。固定的发布控制器以 `ubuntu` 运行；它把每个待构建提交只读克隆到 `/opt/aicrm/domestic/build-worker/<SHA>`，由无 sudo 的 `aicrm-build` 账户执行源码、npm 和 Go 构建。该账户不能读生产 SSH 密钥、发布账本或运行时密钥；构建成功后控制器复制并核对产物。配置示例为 [`deploy/domestic-release.example.json`](../../deploy/domestic-release.example.json)。固定的 `scripts/domestic_release.py`、`scripts/domestic_release_build.py` 放入 `/usr/local/libexec/aicrm/`，固定的 `deploy/domestic-promote.py` 以 root:root、0755 放在两台机器的同一路径。当前预备/生产 SSH 账户 `ubuntu` 已有 sudo 权限，后续可收窄为专用部署账户。生产内网 SSH 使用专用密钥和经过核对的固定 Host Key，不关闭 Host Key 验证。`GITHUB_TOKEN` 如需提高 API 限额只给预备机检查查询使用，不能写入包。

一次性创建 `aicrm-build` 系统账户和独立的 `/opt/aicrm/domestic/build-worker/{cache,tmp}`，把 Go/npm 持久缓存复制给该账户；确认它无法读取 `/home/ubuntu/.ssh/ai-crm-v4-prod-deploy`、`/opt/aicrm/domestic/state.json` 且没有 sudo 权限。发布器本身的代码和 systemd 单元变更须先更新机器上的固定副本并完成预发演练，再合并相关 PR；业务源码由后续定时任务自动处理。

固定 helper 更新到支持 `retry-staging` 后，发布代理仅在预备机建立 root-owned `/etc/aicrm/domestic-release-role`，内容精确为 `staging` 并禁止 group/other 写入；生产机不创建此文件。缺失、符号链接、owner/mode 不符或内容不精确时，迁移 orphan retry 必须拒绝执行。

预备机安装同一完整目录到本机 `/opt/aicrm/current`，使用合成库验证受影响资源、服务和 `/readyz.release_sha`。首次预备机空环境会初始化合成库和服务单元；后续非迁移提交不运行迁移。生产接收区 `/opt/aicrm/domestic-incoming` 由传输账户持有；安装器在共享 `/opt/aicrm/install-release.lock` 下校验清单、当前 base SHA 与服务。迁移提交先做 `pg_dump -Fc` 备份，再运行迁移服务；非迁移提交跳过这两步。健康失败时切回上一版本并验证，数据库迁移保持前向兼容；若远程结果不明，状态置为 `outcome_unknown`，只读对账后人工明确结论，绝不盲目再次安装。

## 切换

1. 暂停旧 GitHub Actions `deploy` 入口及旧指挥台生产写入口。保留旧账本只读供审计。两条发布路径共享主机锁，但切换期间仍需确保只有新定时器可以发起安装。
2. 核对生产 `/opt/aicrm/current/release.env`、`/readyz`、原安装收据和完整清单。核对安装的 preview commit 与当前 GitHub `main` 的 Git tree 相同。旧观察待办不改写成真实业务验收完成。
3. 在预备机从该 `main` 做一次完整基线构建、文件清单和合成数据安装；记录实际构建时间、峰值内存及磁盘。运行固定安装器的摘要失败、基础健康失败及回滚演练。
4. 在 `production_enabled=false` 下执行 `python3 /usr/local/libexec/aicrm/domestic_release.py --config /etc/aicrm/domestic-release.json bind-baseline --prod-preview-sha <生产当前SHA>`。它读取生产状态和旧收据，核对双 commit 同树与预备机基线，建立一次性游标；状态已存在则拒绝覆盖。
5. 两个模拟 PR 连续合并的第一父链、页面无备份/无迁移、Go 受影响程序编译、摘要不符、预发失败、生产健康失败和结果不明的测试全部通过；实测 `.6 -> .13` 的 SSH 身份认证及增量传输。随后设置 `production_enabled=true` 并启用 `aicrm-domestic-release.timer`。记录首个真实合并到生产健康的耗时。

`state.json` 只记录技术游标：`processed_sha`、`deployed_source_sha`、`prod_installed_sha`、`status`。它不等于旧 `/Users/qianlan/Downloads/新CRM/release-control/state.json`，也不记录真实业务验收。状态缺失或损坏、检查未完成、源码拉取失败、基础/生产失败时默认停止。`outcome_unknown` 时保持 timer 停止，先做只读核对；符合本文恢复门槛后，运维必须显式给出 ledger 中同一个完整 SHA：

```sh
python3 /usr/local/libexec/aicrm/domestic_release.py \
  --config /etc/aicrm/domestic-release.json recover \
  --retry-blocked --sha <准确的40位blocked-SHA>
```

恢复器会再次核对 main 第一父链与准确 `check`、预备机包和进程、生产当前版本/健康/进程、目标回执和远端 metadata，并只允许一次安全复用符合 manifest 的非迁移 orphan。调用固定 helper 时还会传准确 release SHA 和 controller 已验证的 metadata SHA256；helper 在同一次 metadata 读取中先校验 digest 与目标 SHA，再允许任何安装副作用。若生产已经运行目标版本且目标成功回执有效，只补技术游标；现场互相矛盾、目标 current 缺少回执、旧版不健康或此前已尝试过恢复时均拒绝操作，保持队列停止并升级人工处置。禁止手工改写/删除 state.json、直接改 current 或盲目重复安装。恢复完成后先读回状态和生产版本，再单独决定何时恢复 timer。

### 仅限当前 5538/0206 事件的一次预备机重试

此入口只恢复本次阻塞提交 `5538d615a9abe2e25be799936866a7330b1d3af8` 的 0206 备份前失败，不是可供未来迁移复用的通用自动重试。账本必须准确记录该 SHA 为 `staging_failed`，且失败发生在迁移前；安装 helper 后可以请求一次受控重试：

```sh
python3 /usr/local/libexec/aicrm/domestic_release.py \
  --config /etc/aicrm/domestic-release.json retry-staging --sha <准确的40位SHA>
```

此操作要求该 SHA 是 `processed_sha` 后的第一父链下一提交、parent 精确等于 `processed_sha`、准确 `check` 成功，且它只有 0206 这一条迁移；随后验证本地构建产物、预备机旧版本健康状态、root-owned orphan、无目标回执/备份、0206 尚未应用且旧订单约束仍有效。任何不一致或无法完成独立读回都保持队列停止。成功后它只继续原有同 SHA 的生产复制、安装和精确 readback；只有生产读回成功才推进账本。重试尝试会先持久化一次性 guard，失败后不可盲目再跑此命令；`staging_verified`、`transport_failed` 或 `staging_retry_unknown` 均需只读核对。

## 回退与局限

静态文件也经当前单体服务提供，因此版本切换后会重启 API 和 effects worker 以让 `/readyz` 报新 SHA；它不重新编译无关程序。若改动涉及系统服务定义、密钥或商户平台设置，需要单独的运维变更；代码发布器不改商户平台配置。迁移只能前向兼容，代码技术回退不会还原数据库。PR #22 不由本流程自动合并。

Excel 批次组件的源码更新会重启其正在运行的 `aicrm-excel-batches.service` 并核对进程存活；预备机的合成环境目前未运行该可选组件。`requirements.txt` 依赖升级尚无可移植的离线 venv 安装路径，不能只凭主程序健康宣告该组件已发布；此类 PR 应先补齐独立的组件部署与预发验证，再进入自动合并。
