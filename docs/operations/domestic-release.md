# CRM v4 国内发布操作

## 日常发布

开发者提交 PR。GitHub 受保护 `main` 只认当前 PR head 的必需 `check`；`check` 工作流始终运行，不配置路径过滤。

| 改动 | 检查 |
| --- | --- |
| 已登记的业务能力 | 影响到的后端、前端或浏览器旅程 |
| 发布器、安装器、CI 白名单工具 | 发布器、分类器、安装器和恢复合同；不跑全量 Go/UI/浏览器 |
| SQL、共享基础、构建规则、未知路径 | 完整检查 |
| 纯文档 | 轻量一致性检查 |

合并后，预备机 `10.0.4.6` 按 `main` 第一父链顺序拉取通过检查的提交，在国内构建并用合成数据验证，再把同一版本经内网晋级生产 `10.0.4.13`。发布器串行执行；预发失败停止队列。生产安装失败使用上一技术版本回退；结果不明时停止队列并只读对账，不重复安装。

**备份按机器角色决定。** 预备机只使用可丢弃的合成数据，不做整库备份或恢复。生产库含真实业务数据，只有数据库迁移才在迁移前自动备份并核对备份文件；普通页面、程序和资源发布不备份、不运行迁移。角色由 root 管理的 `/etc/aicrm/domestic-release-role` 固定为 `staging` 或 `production`；缺失、权限不符或内容不精确时停止。PR 参数不能关闭生产备份。

技术发布成功须读回准确 SHA、完整文件摘要、服务和 `/readyz`。真实支付、扫码等业务结果独立记录；它们不阻塞下一次技术发布，也不能由 CI、预发或健康检查代替。

## PR38 一次性预发烟测恢复

PR38 的原烟测夹具在隔离 schema 中运行 migration SQL，却没有写 `platform_schema_migrations`。已安装 #36 API 的 `/readyz` 因而保持 `503 not_ready`；这是测试夹具缺陷，不能据此判断 #36 应用包异常。PR41 `e8456a9` 补上了迁移台账，但其最后一条断言错误地要求 loopback Alipay HTTP gateway 有请求。WAP/Page handoff URL 由 effects worker 本地签名生成，不会调用 `alipay.trade.query`。烟测在此断言前已通过 handoff、URL 结构与支付持久化检查。PR43 `948ffd3` 将预期改为零个 gateway API 调用，并新增签名与篡改校验。恢复入口只允许准确 PR38 候选使用完整、已合并的 PR43 源码快照，不修改 PR38 源码或快照。

只有当本变更已经合并、该准确 main 版本的固定发布器已安装并校验、发布 timer/service 已停止，且**同一个已安装 #36 二进制通过准确 PR43 fixture 的直接预发烟测**时，才可由单一发布执行者运行下面的一次性命令。2026-09-25 的直接演练已在 85.3 秒内通过，详情和摘要见 PRD；它没有改账本、消耗 one-shot marker 或部署。命令会重新检查队列头、PR38 的准确 `check`、PR43 祖先关系、预发/生产的 #36 SHA 与完整摘要、健康状态，以及两机都不存在 #38 收据或数据库备份；任一证据未知就停止。通过的 PR41 烟测收据不能代替 PR43 验证。

```sh
sudo -u ubuntu /usr/bin/python3 /usr/local/libexec/aicrm/domestic_release.py \
  --config /etc/aicrm/domestic-release.json recover-pr38-smoke \
  --sha 32043f2ecdb814270245dbf2b3840eb868e0a33f \
  --main-sha <当次核对的准确 origin/main SHA>
```

此命令只在预发机对当前 #36 安装二进制运行完整的支付宝虚拟结算烟测，并原子记录 candidate #38、fixture PR43、检查过的 main、两个主机的负向读回及结果。成功后状态为 `ready`，仅将 `processed_sha` 推进至 #38；`deployed_source_sha` 与 `prod_installed_sha` 仍是 #36，不构建、不安装、不传输、不调用生产晋级。原失败尝试和本次 one-shot marker 均保留；普通 `poll` 从 #38 后面的第一父提交继续按序处理，不跳过其间提交。若烟测、main 读回或任一主机读回失败，状态保持阻塞；marker 一旦写入即不可重试，转为只读对账。

## 受影响验证与已安装行为合同

构建范围以 `deployed_source_sha..candidate` 为准，保证候选包从真实已部署版本构建；验证范围单独以已处理的第一父提交 `processed_sha..candidate` 记录。构建元数据保存两组 SHA、树和路径，并把 `actual_ci_baseline_verified` 设为 `false`，直到有能绑定准确 CI 收据的接口。发布器不会把本地分类结果或部署基线冒充 CI 实际测试基线。支付合同是否重跑由可信固定策略判断处理范围内的路径，PR 内容不能关闭合同。

支付相关候选须在预备机上对当前准确安装包运行 `TestDomesticReleaseInstalledAlipayCheckout`：测试启动已安装二进制和效果 worker，使用独立合成数据库 schema、合成 WAP/Page 商户参数及本机 TLS 假 Alipay 端点，并读回订单和支付意图。它不发起真实支付。Go 源码单测或测试构建通过不构成这项已安装行为收据。收据绑定被测源码 SHA/tree、安装 SHA、manifest 和二进制摘要、固定 helper 摘要、预发角色及时间；缺少、失败、摘要不符或结果不明都会停止发布。正常晋级和孤立包恢复都执行这一检查。

固定 stage helper 使用 `GOPROXY=off` 和 `GOSUMDB=off`，运行用户 `ubuntu`、Go `-p=1`、`GOMAXPROCS=2`。执行演练前，固定 Go module/build cache 必须已有当前提交 `go.mod`/`go.sum` 锁定的所有依赖；helper 不联网下载，也不创建新缓存体系。缺依赖时在固定工具切换流程中准备和复核锁定依赖，然后重做预发演练。

固定工具切换后，在 `VM-4-6-ubuntu` 上从 root 执行一次实际安装包合同演练。`<MERGED_SOURCE_SHA>` 必须是包含该固定测试工具的已检查 `main` 提交；其余两个摘要须从当时准确安装版本和检查过的 helper 源文件读回：

```sh
sudo /usr/local/libexec/aicrm/domestic-promote.py --run-staging-smoke \
  --source-sha <MERGED_SOURCE_SHA> \
  --expected-sha <INSTALLED_RELEASE_SHA> \
  --expected-manifest-sha256 <INSTALLED_MANIFEST_SHA256> \
  --expected-helper-sha256 <CHECKED_HELPER_SHA256>
```

成功时只输出 JSON 收据，至少应包含 `status=passed`、`contract=alipay_checkout`、`test_name=TestDomesticReleaseInstalledAlipayCheckout`、准确的 `source_sha`/`source_tree`、`installed_sha`、manifest/二进制/helper 摘要、`stage_role=staging`、Go 工具链与资源限制和 `verified_at_utc`。失败时只依据 root-only 诊断文件路径在预发机本地排查，不复制数据库连接或 provider 配置。本段是待执行入口；实际 rehearsal 完成前不得记录为通过。

## Main 全量回归暂停

候选准确 SHA 的必需 `check` 变绿后，发布器才读取公开的 `.github/workflows/ci.yml` main workflow run 与准确 attempt jobs；pending check、空队列不会额外轮询 runs/jobs。仅用公共 run/job 元数据，不下载 artifact ZIP，也不配置服务器 GitHub token。发布器按 run SHA 和 attempt 核对 `plan`、五个验证 lane、`governance` 与 `check`。`Record exact main full regression result` step 的存在只标识新机制；该报告 step 本身为尽力记录，自己的结论不替代必需 jobs。

旧 main push 没有该标识时不纳入回归历史。新 schedule 或显式 force-full run 缺少标识、required job 缺失/未知、attempt 不精确或 required job 未成功，都按未知证据暂停；实际 full lane 失败也暂停。schedule 的 `verified` 跳过只保留信息，不能清除此前记录的失败。只有同一 SHA 或其第一父后继上，准确 attempt 的五个 lane、`plan`、`governance` 和 `check` 全部成功，才清除之前较早的失败/未知。若没有已记录回归失败/未知，保持现有准确 SHA `check` 门禁，不新增首次全量要求。

暂停状态观察 main 当前准确 `check` 的 run ID、开始/完成时间和结论；同一 SHA 的 check rerun 也会因签名变化重新核验。GitHub run/jobs 读取超时或限流会标为暂时读取错误，暂停晋级并在 5 分钟后只读重试，无需新提交或重跑全量 CI。真正的失败或不完整 evidence 仍等待新的准确成功 check，并且只有新的 full 成功可以解除。

## 首次切换或发布工具升级

只由一个发布执行者操作。修改固定发布器或安装器前，先暂停 timer，并停用旧的生产写入口；只读确认当前生产版本、健康、主机角色及无结果不明部署。把准确已合并提交中的工具安装到预备机和生产机，核对文件摘要及权限。预备机以真实服务用户、真实目录、受限 `PATH` 和 systemd 环境运行主机合同检查；固定 helper 使用 `--check-host-contract --expected-helper-sha256 <准确已检查提交中该文件的 SHA256>`，并应读回 `host_role=staging`、PostgreSQL 16 和 `database_connection=verified`。同时通过两个 PR 连续排队、阶段失败、摘要不符、生产健康失败和结果不明的合同测试。确认生产角色标记精确为 `production` 且主机身份为 `10.0.4.13` 后恢复 timer。切换过程不改应用 `main`、数据库或当前版本；结果不符就保持 timer 停止并只读排查。

若 helper 改动涉及生产主机权限合同，合并前须对生产做只读预检并记录结果：角色标记须为 root:root 且内容精确；`/etc/aicrm` 须 root-owned 且无组/其他写；`aicrm.env` 须为 root-owned 普通文件、root 可读且无组/其他写或任何其他用户权限，组可读时只能属于 `aicrm`；`aicrm` 须可遍历 `CURRENT` 并执行程序文件；三个 systemd unit 须配置必需的 `EnvironmentFile=/etc/aicrm/aicrm.env`。只记录路径、权限和检查结论，不读取或记录秘密值；此预检不改数据库、不备份、不安装。

## 失败处置

- `check` 失败：按准确 head 修复并重跑，不人工绕过 required check。
- GitHub `main` 拉取超过 30 秒：本轮结束，不改发布游标、不构建或安装；timer 保持原两分钟间隔重试。
- 预备机安装/迁移失败：停止该候选并保持 timer 暂停；不要对失败候选重试，也不要把“可丢弃”理解成可盲目删除。恢复仍由授权操作员引导，发布器没有自动重置数据库的能力。
- **预备机数据库恢复门禁（人工，不自动）：**任何 scratch 建删前暂停 `aicrm-domestic-release.timer`，确认 timer/service 均为 `inactive`；对准确检查提交中的固定 helper 运行 `--check-host-contract --expected-helper-sha256 <源文件 SHA256>`。只允许身份全部匹配时继续：主机 `VM-4-6-ubuntu`、受保护角色 `staging`、地址 `10.0.4.6`、PostgreSQL 16、解析后的连接身份 `127.0.0.1:5432 / aicrm_test / aicrm_test_baseline_5d15`。不要显示或复制连接密钥；任何一项不符、目标库/owner 不符或存在活动会话就停止，不执行 CREATE/DROP。隔离验证只可使用独立命名、owner 为 `aicrm_test`、template0 且 locale 与基线一致的 scratch；仅用本机 PostgreSQL 管理连接执行普通 DROP，禁止 `DROP ... FORCE`。这组门禁不授权删除持久的 `aicrm_test_baseline_5d15`；其现场恢复仍需单独的操作员判断，发布器没有自动重置能力。
- **隔离演练记录：**2026-09-24 在 `10.0.4.6` 使用 `aicrm_pr29_rehearsal_dee494a257e0` 和已核对的 `832a8d38…` 迁移二进制。单文件 9999 除零故障按预期回滚，未留下 ledger 行或 probe 表；普通 DROP 后重建，201 个迁移（最高 `0207`）与五类合成夹具全部通过读回，约 16.8 秒后 scratch 已清理。基线库前后 owner、大小、ledger 和夹具计数完全一致。**本演练没有重置基线库，不能作为现场基线恢复已经验证的证据。**实际恢复后重新跑迁移和合成夹具读回，并为新候选生成新证据，不复用失败候选收据。详细读回结果见 PR #29 描述。
- 生产迁移前备份或迁移失败：停止队列；根据备份和迁移读回判断，未经专项修复不重试。
- 生产健康失败：自动切回上一技术版本并读回。数据库迁移保持向前兼容，不反向恢复真实业务库。
- 部署状态为 `outcome_unknown` 或读回互相矛盾：停止 timer，只读比对账本、当前链接、版本、摘要、服务与健康；得出确定结论前不重新安装。

## 时间记录

现有发布收据记录检查、构建、预发、传输、生产安装和健康读回耗时。接下来十次发布统计目标：工具 `check` ≤5 分钟，普通业务 PR 约14分钟，从 check 变绿到生产健康 ≤10分钟。目标未实测前不报告为已达成。

旧 merge-preview、handoff、观察占位及手工发布/恢复入口均为历史审计材料，不是新发布门禁。5538/0206 专项故障记录见[归档](archive/2026-09-23-5538-0206-staging-retry.md)。
