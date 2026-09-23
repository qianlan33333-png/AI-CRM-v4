# 数据生命周期与发布物清理契约

业务判断：业务事实、原始素材、幂等与结算证据永久保留；过程日志、诊断明细和无业务价值的已终止执行数据最多保留 30 天。未知资源拒绝自动删除。不能因为名称包含 `cache`、`tmp`、`log` 就认为可以删除。

OneID：不涉及身份匹配、建客、关联或归属变更。持久化：Media Owner 在 PostgreSQL Unit of Work 内清理自己拥有的分片；共享 River 负责可恢复的调度，不增加领域 Worker、重试表或进程内 ticker。宿主维护脚本只处理已登记的发布物与专属过程目录。没有 Provider 业务写入，不产生新的 External Effect。

## 资源分类及自动化边界

机器清单为 [retention-registry.json](retention-registry.json)。本次合并清单覆盖 **449 张表**。389项补充审定的字段、引用与Owner存取证据见 [逐表分类报告](retention-classification-review.md)。目前1项仍为 `protected_unclassified`、19项为 `protected_mixed_payload`，两类均禁止自动删除；未知来源不能伪装成已完成分类。永久事实、当前投影、协调状态与安全TTL分别登记。只有已实现并验证的Owner清理入口才能进入运行白名单；分类本身不会启用删除。数量以校验器输出为准。

`python3 scripts/check-retention-registry.py` 检查所有迁移声明、平台迁移账本和固定 River 表；新增表未登记、来源不匹配、业务表被套用具名可删策略都会失败。`--live-tables table-names.json` 接受只读取得的 public 表名 JSON 列表，额外的生产表拒绝放行。`cleanup` 表明是禁用、Owner Port 已有还是仍待平台配置，不能将“已登记”当作“清理已启用”。

超管只读 `GET /api/admin/ops-retention/resources` 读取注册表绑定的资源覆盖目录，包含Owner、理由、精确入口与缺口；九项白名单执行健康单独呈现。修改清单后运行 `python3 scripts/check-retention-registry.py --write-runtime-snapshot`，默认checker拒绝摘要或内容漂移。目录是已提交资源清单，不伪称生产实时盘点；安全TTL授权失效与物理清理缺口分开。完整DTO与状态语义见[覆盖设计](../plans/2026-09-18-retention-coverage-design.md)。

文件系统也有明确 `filesystem_prefixes` 清单。校验器扫描 `deploy/internal/cmd` 的受支持源码与 unit 配置，检查 `/opt`、`/etc`、`/var/lib`、`/var/log`、`/run` 绝对路径及 systemd `StateDirectory/LogsDirectory/CacheDirectory/RuntimeDirectory`；新增前缀未登记会失败。父容器如 `/opt/aicrm` 只允许精确自身，不会自动批准任意新增子目录。`--live-roots roots.json` 验证只读宿主盘点发现的真实存储前缀。动态拼接的任意路径不能仅靠静态扫描证明，发布时仍须真实目录盘点。分类器不执行 SQL，也不直接删除任何表。

| 资源 | Owner / 保留规则 | 允许的动作 |
| --- | --- | --- |
| 客户、订单、问卷、标签、会话业务内容、财务及业务审计 | 原 Owner / 永久 | 无通用删除 |
| Media 正式 blob、素材、源快照、上传头、完成收据 | Media / 永久 | 分片清理不可触碰 |
| 支付、退款、External Effects 收据、尝试、Outbox 与幂等证据 | 原 Owner / 永久 | 不能用执行缓存策略删除 |
| `media_attachment_upload_parts` | Media / 超过 30 天 | 仅上传已过期超过 30 天的重复/废弃分片 |
| River 终态内部任务 | Platform / 终态满 30 天 | 由共享 River 清理器执行；未终态任务不删 |
| AdminOps 巡查结果、诊断明细、运行明细 | AdminOps / 发生时间满 30 天 | 详细过程数据到期删除，陈旧未完成巡查也不延期；业务收据永久保留 |
| AdminOps 小时报表正文 | AdminOps / 创建时间满 30 天 | 仅清正文；effect 绑定、摘要、回执和永久问题台账保留 |
| 配置使用观察 `config_runtime_usage` | Config / used_at满720小时 | 仅使用过程观察；配置值、发布、审计、收据和Automation冻结决策保留 |
| 会话存档同步过程 `message_archive_sync_runs` | MessageArchive / finished_at满720小时 | 仅succeeded/failed；running、正式消息、msgid和同步游标保留 |
| HXC 历史看板行 | HXC / 现有 Owner 投影策略 | 已有版本替换规则不变；版本头、来源和收据永久保留 |
| Session / OAuth state | 原 Owner / 原有较短安全 TTL | 原 `expires_at`/撤销校验不变；30 天策略绝不延长授权 |
| CRM 专属过程 journal | Platform / 30 天 | 仅 `aicrm` journal namespace；不清理同机其他服务 |
| CRM 专属过程临时文件 | Platform / 超过 30 天 | 仅已标识目录、已非活动的普通文件，每批 1000 |
| 数据库备份、Secret、业务导出和上传原件 | 运维 / 不纳入本清理 | 不递归扫描或删除 |

`permanent` 只用于已核实的永久事实；尚未审定的资源用 `protected_unclassified`。安全状态单独标 `security_ttl`，不改变 Owner 原有授权过期规则。比如 Radar 访问 session 同时承担历史归因，过期后仍可能被历史事件引用；不能连同行为事件一起清掉。这里没有 incident hold，也不以延长详细过程日志来保留问题台账：问题状态、处理动作、摘要和业务证据另行永久保存。

当前明确的接入缺口：`admin_sessions`、`distribution_browser_sessions`、`payment_h5_oauth_states`、`payment_sessions`、`radar_oauth_states`、`radar_view_sessions`、`survey_identity_sessions`、`survey_oauth_states`、`wecom_oauth_states` 保持较短授权 TTL，但物理清理没有接入本调度；`admin_login_rate_limits`、`wecom_provider_cache_metadata` 是运行协调状态，仍需 Owner 判定锁定/过期条件再提供清理入口；`customer_directory_projection`、`customer_timeline_projection` 维持 Owner 原有更新，未启用通用历史清理。全量审定已完成389项补充分类；19项混合表的纯过程载荷仍待Owner拆分，`payment_shop_materials`仍缺生产读写来源证据，均保持禁止自动清理。监控应暴露这些覆盖缺口，不能报告“全系统所有缓存已实现 30 天清理”。

AdminOps 的新表随其 Owner 迁移登记，精确可删 allowlist 为 `adminops_inspection_runs`、`adminops_inspection_results`、`adminops_diagnostic_events`、`adminops_retention_runs`；`adminops_inspection_reports` 仅允许正文策略。`adminops_inspection_issues`、`adminops_inspection_issue_actions` 永久保留。清理实现必须由 AdminOps Owner 提供，不允许运维 SQL 越界清表。

旧 `adminops_diagnostic_snapshots` 已补齐 `SnapshotRetention.CleanupDiagnosticSnapshotsWithin`：调用者必须提供同一 UoW，固定不早于 30 天的删除边界、advisory lock、每批 1000；读取直接过滤 30 天之前的明细。删除和调用者计数收据可一起回滚。`adminops_release_projections` 是长期发布依据，完全保留。共享调度接入和生产启用需在集成验收中确认。

## Config 与 MessageArchive 过程清理 Port

两个Owner分别提供 `ProcessRetention.CleanupProcessDetailWithin`，AdminOps只绑定固定策略 `config_usage`、`archive_sync_runs`；Composition转换各自Port值对象，不跨域访问表。预览最多一批1000条并由Owner返回剩余标志，字节数是候选行的逻辑估计。清理再次校验数据库时间边界和终态，短锁等待、`SKIP LOCKED`，引用由数据库约束保护。过程删除与AdminOps清理计数在同一事务中提交；注入记录写入故障可验证两者一起回滚。

0189保留配置使用观察的UPDATE/TRUNCATE禁止规则，只将DELETE替换为逐行720小时保护。配置观察在线读取同样过滤超过30天的记录。配置业务审计与冻结后的发送决策不受影响；会话存档正式消息和游标不进入清理。

## Media 清理 Port

`internal/media/port.UploadPartRetention.CleanupExpiredUploadParts` 接收 `{Before, Limit, Apply}`。`Before` 不能晚于数据库当前时间减 30 天，`Limit` 必须为 1–1000。`Apply=false` 仅报告候选数和字节数；`Apply=true` 在同一事务中获取 Owner advisory lock，使用 `FOR UPDATE ... SKIP LOCKED`，删除至多一批，返回 `Candidates/Deleted/Bytes/Remaining`。

候选必须同时满足分片创建时间和上传过期时间早于 cutoff。已完成上传还必须能找到 digest/size 匹配的正式 blob。缺失正式原件时保留分片并交给巡查诊断。上传头和幂等收据始终保留，已完成操作重放继续返回原素材。无需新的数据库迁移。

共享调度以单并发调用每个 Owner，每次一批，`Remaining=true` 留给后续批次。不能把 `Before` 传成当前时间，不能按表名执行通用 SQL，也不能凭 `Apply=false` 的数字声称实际清理成功。

## 应用日志与过程文件安装

先查看明确的安装与维护协议：

```sh
python3 deploy/install-runtime-retention.py inventory
python3 deploy/install-runtime-retention.py apply
```

`apply` 仅在 Linux root 下可用，要求 systemd 245+。安装器写 `/etc/systemd/journald@aicrm.conf.d/60-retention.conf`，并给脚本中列出的 8 个 CRM 服务安装专属 drop-in：`LogNamespace=aicrm`、专属 `TMPDIR` 和可写过程目录。只重载 unit 配置及已经运行的 `systemd-journald@aicrm`；应用服务由正常发布窗口重启，重启前不能声称其日志已经进入新 namespace。不会重启其他服务，不会安装全局 journal 策略，不会创建第二个业务调度器。

发布安装器在正常服务重启之前调用 `install-host-maintenance.py`：将固定 helper 复制到 root 所有的 `/usr/local/libexec/aicrm-retention`，安装无 timer、无 `[Install]` 的 `aicrm-runtime-retention.service` oneshot，以及仅允许 `aicrm` 用户启动此精确 unit 的 polkit 规则。不会允许 restart、stop、其他 unit 或修改 unit 文件。Worker 原有 `NoNewPrivileges=true` 不变，不引入 sudo。

Composition 绑定 `internal/platform/hostmaintenance.New()` 到 `platform/port.HostMaintenance`，由现有 River 小时任务调用 `Run(ctx)`；它同步等待固定 `systemctl --no-ask-password start aicrm-runtime-retention.service`，不接收调用方的路径、unit、命令参数或环境。固定 helper 每次至多清理 1000 个过程文件，再清理 `aicrm` journal namespace。`HostMaintenanceReader.ReadLatest(ctx)` 是无副作用读取，超过两小时或未完成结果视为证据缺失。

helper 原子写 root 所有、应用只读的 `/var/lib/aicrm-maintenance/runtime-retention.json`，只含时间、状态、聚合计数与白名单错误码。先写 running、后写完成结果是最近一次执行观测，不是新队列或重试状态机；杀进程留下 running 时不会报成功。Adapter 校验 root 所有权、权限、非 symlink、64 KiB 上限、严格 JSON、时间新鲜度及计数范围。部分失败保留已确认计数并返回失败；不把未知回收量编造为零。

`runtime.bytes`、发布结果 `deleted_bytes` 是已确认删除文件的逻辑长度；不是物理磁盘回收量。两种结果额外记录 `space_observations`，分别采样维护前后文件系统的 `f_bavail × f_frsize`。`available_bytes_net_change` 可为负，包含并行业务写入、数据库和其他宿主活动，不能准确归因于本次清理；同一设备上的不同资源采样不能相加。路径不存在、采样失败或设备变动时返回 `unavailable` 与 null 差值，不编造零。报告只使用固定资源名，不包含设备路径或业务文件名。

运维人工盘点入口仍为以下固定命令；应用不会直接执行它们或取得 root：

```sh
python3 /opt/aicrm/current/deploy/cleanup-runtime-files.py inventory --limit 1000
python3 /opt/aicrm/current/deploy/cleanup-runtime-files.py apply --limit 1000
journalctl --namespace=aicrm --rotate --vacuum-time=30d
```

文件清理只接受 `/var/lib/aicrm/process-tmp`、`/var/lib/aicrm/process-diagnostics`。两个目录必须有安装器写入的 `.aicrm-disposable-v1` 分类标识，所有权为 `aicrm`，且不允许组/其他用户写入。安装器拒绝接管已有的非空未知目录。业务产物不得存入这两个目录；正式导出、附件与源文件必须进入各 Owner 持久存储。

文件入口默认仅盘点，固定 30 天、不接受调用方缩短，普通文件的 mtime 与 ctime 均须超过 30 天；刚复制的旧文件不会因为旧 mtime 被误删。每批最多 1000 个，单 flock 并发，使用目录文件描述符删除，不跟随 symlink、不跨 mount、不删 hardlink、外来 Owner、活跃进程 FD 或已关闭 FD 后仍保留的 mmap，拒绝业务/备份/secret 子目录。每个 unlink 前重扫活动引用，再比较 inode、大小、时间和 link 数；任何引用盘点不完整都停止。只输出聚合计数，不输出文件名或正文。不是 incident hold：文件仍在使用时属于未终止执行数据。

TOCTOU 边界：文件与发布目录保护都是删除前实时重查，无法阻止不遵守协调协议的进程在最终检查后立即重新打开路径。发布切换/维护必须使用同一 installer lock；过程目录必须只存可丢弃执行数据，业务数据始终使用 Owner 存储。目录 FD 防止 symlink 跳转，引用重查缩小检查与删除之间的时间窗，但不宣称对任意外部进程具有原子“无人引用后删除”保证。超过 60 秒的文件扫描由宿主 helper 中止，结果为失败/未知，不报告为已完整回收。

journal 以文件为单位回收，`MaxFileSec=1hour` 限定轮转粒度，`MaxRetentionSec=30day` 限定保留窗口；容量保护可能更早回收。定期 namespace rotate/vacuum 处理低流量时的归档文件。历史混在默认 namespace 的 CRM 日志不能按 service 单独 vacuum，本方案保留它们由宿主既有策略处理，不扩大清理到其他服务。原全局 `/tmp` 或旧日志目录也不自动迁入；必须先分类。

## 发布目录 inventory / apply

`deploy/cleanup-releases.py` 仅清理明确验证过的 SHA 发布包；曾盘点的 164 个目录 / 约 29 GB 只是候选统计，不能当作可删列表。生产脚本需要 Linux root，以完整检查 `/proc` 与服务引用。

先用只读 PostgreSQL 连接取得真实迁移账本快照，内容无业务数据：

```sql
SELECT json_build_object(
  'current_sha', :'current_sha',
  'captured_at', clock_timestamp(),
  'migrations', (SELECT json_agg(json_build_object(
    'version', version, 'name', name, 'checksum', encode(checksum,'hex')) ORDER BY version)
    FROM platform_schema_migrations));
```

`current_sha` 来自 `/opt/aicrm/current` 的真实目录及 `/readyz` 一致读回。快照应写为本机仅 root 可读的 JSON，时间不能超过一小时；脚本会重新验证当前发布和迁移文件校验和。

```sh
python3 deploy/cleanup-releases.py inventory --root /opt/aicrm --schema-evidence /run/aicrm-schema.json > /run/aicrm-cleanup-plan.json
python3 deploy/cleanup-releases.py apply --root /opt/aicrm --schema-evidence /run/aicrm-schema.json --plan /run/aicrm-cleanup-plan.json
```

两种模式都持有与安装器相同的 `install-release.lock`。保留当前包及两个具备受控成功发布凭证、所有文件校验通过、迁移账本与当前真实 schema 完全相同的回滚包；没有两份证据就阻断所有删除。schema 相同是本脚本采用的保守兼容条件，不代表执行了旧程序的完整业务回归。

成功发布末尾已有自动 hook `post-release-retention.py --sha "$release_sha"`。安装器先拒绝 symlink 控制目录，将 `/opt/aicrm` 及 `releases` 父目录收紧为 root:root 0755；只封发布控制目录，不递归改变应用运行数据。发布包在最终 checksum 复核之前收紧为 root 所有且组/其他用户不可写；应用保留读取与执行权限，不能替换 root 即将执行的维护代码。它核对继承的 fd 9 与 installer lock 同 inode，并保持同一锁，避免递归抢锁；在 root 进程中仅解析数据库配置键，以 PG 环境变量和只读连接查询真实迁移账本，不把 DSN/密码放进参数、输出或日志。清理成功才记录回收数量；不足两个验证回滚版本时产生 `two_verified_schema_compatible_rollbacks_missing` gap，不删除目录，也不回滚已经成功启动的版本。失败原因和确认的局部进度进入 `/var/lib/aicrm-maintenance/release-cleanup.json`，未知数量为 null。

`platform/port.ReleaseMaintenanceReader.ReadReleaseLatest(ctx)` 只读此文件，不执行清理、不读取数据库配置。Adapter 验证固定路径 root 所有权、权限、严格字段/错误码、时间不在未来，且前后两次读取的 `/opt/aicrm/current` 均与结果 SHA 一致。相同发布的观察结果按版本有效，不套用小时任务的两小时过期门槛；缺失、切版或非法证据返回 `ErrEvidence`，保留回滚版本不足等保护结果返回报告与 `ErrPartial`，供巡查和飞书展示 gap。

保护集合包括每个进程的 exe/cwd/root/maps/cmdline/fd、已安装及瞬态 service 的执行和工作目录、环境文件与服务文件路径、根目录 symlink、在途安装脚本/压缩包引用，以及显式 `--protect-release SHA`。权限不足导致引用盘点不完整时拒绝清理。不会打印进程命令行、环境内容或 Secret。

只有完整 `release-files.sha256`、标准包目录、所有文件校验匹配且未被引用的目录可进入计划。未知目录、额外业务/备份/Secret 文件、symlink、mount、hardlink、缺失 manifest 均保护。apply 重新执行完整盘点，并在每个删除前重查引用和内容；计划后新增引用或文件变动会拒绝执行。安装/维护参与者必须遵守同一 installer lock，禁止绕锁手工切换目录。

清理前后将安全摘要 fsync 到 `/opt/aicrm/release-cleanup-audit.jsonl`，该证据位于发布目录和过程清理目录之外，不被本策略删除。部分完成会报告已删清单；进程中断留下 `delete_started` 而无 `delete_completed` 可识别未确认动作。重复执行同一计划对已经不存在的目录返回 `already_absent`，不影响保留版本。

## 参考与验证

沿用 [River JobCleaner](https://github.com/riverqueue/river/blob/v0.24.0/internal/maintenance/job_cleaner.go) 的终态清理模型；宿主日志使用 [systemd namespace 配置](https://www.freedesktop.org/software/systemd/man/252/journald.conf.html) 和 [systemd 官方临时目录指引](https://github.com/systemd/systemd/blob/main/docs/TEMPORARY_DIRECTORIES.md)。不引入通用清表框架，不把业务证据当作可重建缓存。

专项命令：

```sh
python3 scripts/check-retention-registry.py
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_retention_registry.py' -v
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy -p 'test_cleanup_releases.py' -v
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy -p 'test_runtime_retention.py' -v
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy -p 'test_host_maintenance.py' -v
go test ./internal/media/store -run TestPostgreSQLRetention -count=1 -v
go test ./internal/adminops/store -run TestPostgreSQLSnapshotRetention -count=1 -v
go test ./internal/platform/hostmaintenance -count=1
```

Media 和 Snapshot 测试必须提供独立 PostgreSQL 16 的 `AICRM_DATABASE_URL`，数据库用例跳过不能算通过。覆盖真实上传完成重放、原件及收据不变、精确边界、锁冲突、并发上传保护、批次 1000 和幂等空跑。Python 用临时目录、真实 JS 规则运行及模拟宿主引用验证规则；它不是 Linux systemd 安装或生产回收验收。宿主需要 systemd 245+、polkit rules 支持、psql；安装前检查这些前提，缺失不降级为放宽 Worker 权限。生产启用、共享 River 调度、飞书实际回执与磁盘回收前后证据由集成发布分别验收。

## 分批启用与停用

通过完整 CI 并部署相同 SHA 后，固定运维入口 `deploy/configure-ops-runtime.py` 在安装器同一文件锁内更新三项治理开关。默认 `--mode inspect` 只核对安装版本，不写配置。入口只接受完整 SHA、固定模式及 retention on/off，不接受环境路径、表名、SQL 或任意命令；原飞书凭据必须已放在应用所有的0600固定文件中；另有root 0600的 `/etc/aicrm/ops-feishu-target.json` 绑定经旧系统核验的凭据摘要和原群别名，不能只凭文件名判断接收目标。

```sh
sudo python3 /opt/aicrm/current/deploy/configure-ops-runtime.py --sha <installed-sha> --mode enable --retention off
# 独立核验巡查完成、Provider 回执与原群实际可见，再启用已验证白名单：
sudo python3 /opt/aicrm/current/deploy/configure-ops-runtime.py --sha <installed-sha> --mode enable --retention on
```

脚本只重启API与effects-worker，同时核对readiness的SHA、两个真实进程exe及当前发布链接。变更前先落盘root 0600事务恢复记录；配置替换后报错或SIGINT/SIGTERM会恢复原始字节、重新启动并验收原版本。SIGKILL等遗留记录阻断后续启用和发布，只允许 `--mode recover --sha <same-installed-sha>` 恢复；第三方配置变更或不同SHA拒绝自动覆盖。原配置正文仅存在受保护的短期恢复记录，不进入输出；成功或已验证恢复后删除记录。该记录是本次配置事务的崩溃恢复状态，不是另建备份体系。不支持的多行配置格式在写入前拒绝，其他配置原字节保留。安装器与此入口共用同一内核文件锁，锁忙时保留包并退出，不杀其他持有者。其他 Provider 写开关不变。`--mode disable --retention off` 关闭治理调度和发送；已经持久化的清理任务执行时也重新检查禁用开关，避免只停新增调度却继续运行旧清理任务。禁用不会撤回已接受的 Provider 效果，也不能改变已发生的业务事实。

## 受控 CPU 采样补充

`adminops_cpu_profile_receipts` 由 AdminOps 永久保留最小操作与防重收据，不保存原始 profile。净化后的 CPU 样本只进入已登记的 `/var/lib/aicrm/process-diagnostics`，遵守现有 720 小时宿主清理；在线列表、详情和下载到期即拒绝。仅 API 角色且巡查与过程清理均启用时，超级管理员可显式触发固定 5 秒采样。单文件 2 MiB、全局存量 64 MiB，同时约束数据库预留和实际目录占用；Worker CPU 采样未覆盖。
