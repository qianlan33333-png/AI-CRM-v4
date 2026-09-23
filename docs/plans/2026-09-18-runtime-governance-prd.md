# 新 CRM 长期运行治理 PRD

状态：按 2026-09-18 用户批准计划实施；验收事实另行记录，本文不构成部署或送达证明。

## 业务判断与架构分类

- OneID：只读规范身份事实与冲突统计，经 Identity Port；不新建、合并或修复客户。
- Persistence：AdminOps 拥有巡查、报告、问题及过程明细；内部持久任务复用 River。领域事实由各 Owner 的 Port 提供。
- External Effects：飞书属于 Provider 写入，新增 `adminops/feishu_ops_notification_v1`，小时报告与效果接受同一 PostgreSQL Unit of Work。来源、目标、载荷和策略均冻结摘要；发送超时保留结果未知，禁止盲目重发。
- 业务故障只报告和跟踪。唯一自动维护是经登记白名单和保护检查的纯过程数据及发布产物清理。

## 用户可观察结果

1. 管理员在统一工作台查看检查覆盖、当前异常、问题跟踪、报告、数据保留和容量；未知、过期与未覆盖不可显示为正常。
2. 每小时执行一次持久巡查，并在每小时 05 分向原飞书群提交一个唯一报告窗口；不再设置 5 分钟自动巡查。报告分开呈现上一完整小时流量和当前积压；未来预约任务不算积压。
3. 排查编号关联请求、版本、领域操作、任务、效果与收据；不记录 Token、Cookie、身份原文、手机号和原始请求。
4. 纯过程数据滚动保留 720 小时，在下一轮小时清理删除；业务数据及幂等、身份、资金、正式发送和对账依据永久保留。未解决问题也不延期保存原始日志。
5. 发布包保留当前版本和两个有成功发布凭证、经产物校验且与当前 schema 兼容的回滚版本，额外保护运行中、发布中和固定版本。备份完全不在本次范围。
6. 变更识别 Owner、消费者及旅程；保持性测试覆盖 B 覆盖 A、乱序、重放、并发和重启；完整 CI 仍是合并门禁。
7. 超级管理员可在治理页面显式采集 API 进程 5 秒 CPU 样本，显示版本、结果及到期时间。全局并发 1、每小时全局 6 次/每人 3 次、单份 2 MiB/合计 64 MiB；Worker 进程仍标未覆盖。详细判断及 GitHub 参考见[受控采样规则](2026-09-18-runtime-cpu-profiling.md)。

### River 队列心跳误报修正

业务判断：队列没有完成任务也可能正常运行；近期有完成任务也不能证明当前 Worker 在线。原两分钟阈值短于 River 的原生队列上报周期，正常 Worker 会在两个报告之间被反复判为未知。

OneID：不涉及，读取的是队列运行证据。Persistence：仅调整平台只读巡查，不新增表、写入、调度器或持久任务；原有 River 上报机制不变。External Effects：不涉及，不改变飞书发送、幂等或业务执行。

依据固定版本 [River v0.24.0 producer.go](https://github.com/riverqueue/river/blob/v0.24.0/producer.go#L962-L999)：队列启动时写入 `river_queue.updated_at`，随后每十分钟上报；首轮有零到一秒抖动，单次数据库报告超时十秒。[Client 配置接线](https://github.com/riverqueue/river/blob/v0.24.0/client.go#L2074-L2109)没有公开上报间隔覆盖项，因此不修改依赖内部配置，也不另造心跳。

平台将已审原生周期固定为十分钟，另加两分钟调度/数据库余量：距最后报告不超过十二分钟仍为新鲜，超过十二分钟为陈旧。结合既有五分钟巡查，失联会在最后报告后十二到十七分钟的正常巡查窗口被观察到；不承诺主机不可达时仍能自行报告。每个预期队列独立计数，缺失立即为未知，暂停单独计数，其他队列或每小时完成量不能掩盖缺失/陈旧。证据是队列级原生观察，不是每个进程存活或外网可达证明。

验收：真实 PostgreSQL 16 覆盖三分钟、十分钟、上报抖动/SQL余量、十二分钟及后一微秒边界；混合新鲜/陈旧/暂停/缺失；160 个近期完成任务不能掩盖陈旧队列；真实空闲 River 启动且零任务时仍有有效报告。配置契约测试只读检查三种平台构造入口创建的实际 producer 间隔；固定版本没有公开读取 API，测试反射遇到上游字段或周期变化必须明确失败并要求复审，禁止跳过或默认通过。

影响：仅 `internal/platform/jobqueue` 的观察阈值与专项测试；无 Composition、OpenAPI、源码归属索引、迁移、队列并发或外部效果契约修改。共享平台变更仍按当前 HEAD 执行 fast、完整编译、真实 PG 专项及后续完整 CI，专项不等于生产修复。

## 接口与权限

- `/api/admin/ops-inspections`：摘要、检查、运行、小时报告、问题、心跳。
- `/api/admin/ops-diagnostics`：错误聚合与关联链，仅暴露脱敏必要信息。
- `/api/admin/ops-diagnostics/cpu-profiles`：固定参数的 API CPU 采样、720 小时内列表/详情/下载；原键重查不会重新采样。
- `/api/admin/ops-retention`：固定策略、预览、执行记录、容量。不得接受任意 SQL、表名或文件路径。
- 复用现有管理员会话、权限、CSRF 与审计；普通用户不能读取明细或启动清理。
- `POST /api/admin/ops-inspections/runs` 返回 `202 Accepted`，表示已接受幂等持久任务，运行结果需读取详情；达到小时限额返回 `429` 与 `Retry-After: 3600`。接受、执行完成、报告提交与飞书送达是四项不同事实。

## 数据生命周期

独立规则、机器资源清单及 Owner 实现共同构成清理授权。未分类资源不自动删除。混合表仅删可丢弃载荷，永久保留业务事实、摘要、幂等收据；单并发、每批最多 1,000 行、短事务、锁超时，删除时重新检查状态与引用。River 终态记录使用原生 cleaner，三种终态统一 30 天。数据库可复用空间与文件系统实际回收量分别统计。

## 成熟方案与采用依据

- [OpenTelemetry 日志模型](https://opentelemetry.io/docs/specs/otel/logs/)：采用关联字段，不在首期建设额外日志存储平台。
- [PostgreSQL 16 清理维护](https://www.postgresql.org/docs/16/routine-vacuuming.html)：索引及小批删除，交由 autovacuum 回收可复用空间；自动维护不执行 VACUUM FULL。
- [River](https://github.com/riverqueue/river)：复用已有持久执行与终态 cleaner，不增加调度底座。
- [oasdiff](https://github.com/oasdiff/oasdiff)、[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)、[Gitleaks](https://github.com/gitleaks/gitleaks)：固定免费 CLI，分别检查 API 兼容性、可达漏洞及密钥；工具失败属于未验证。
- 复用已有免费腾讯云监控；不能核验的外部主机/网络覆盖列为缺口。

## 实施与验收

并行交付巡查、生命周期、CI；共享 Composition、EER、jobqueue 和页面由集成人统一合并。每一轮验证绑定准确 HEAD/tree；专项测试、完整 CI、部署版本、业务读回、Provider 接受和群内可见分别留证。

必须验证：29/30/31 天边界，业务与防重依据不变；清理后重放不能产生重复效果；并发使用对象受保护；中断恢复；当前与两个可用回滚版本保留；权限与 30 天在线读取限制；注入跨功能覆盖和错误清理规则被门禁阻断。24 小时验收需要真实形成 24 个唯一报告窗口及群内可见证据，不能由一次演示代替。

### 2026-09-18 实际证据与交付边界

本节按证据范围记录进度，后续源码变更必须重新验证，不能继承旧版本绿灯。

| 项目 | 已有证据 | 交付边界 |
| --- | --- | --- |
| 初次完整集成 | 公开提交 `bb6b21891281477b2d781cac4a4054344ac5fefa` 的 [GitHub CI 35334414417](https://github.com/qianlan33333-png/AI-CRM-v3/actions/runs/35334414417) 全部 required lanes 和 check 通过 | 并行 PR #385 先合入 main；后续已同步并补 CPU 采样及发布成功凭证，必须在最终 HEAD 重跑完整 CI |
| CPU 采样 | 真实五秒采样、标签净化、数据库互斥、配额、取消、未知重放和 TTL 专项通过；API 与前端已集成。真实 Host 旅程覆盖服务端完成后丢响应，原键重查仍只有一次采样 | 专项与本地编译不是最终 Linux CI 或生产启用证明；Worker 采样不在首期覆盖 |
| 安全门禁 | 固定 Gitleaks、npm audit、govulncheck 和 oasdiff 在 `c25ced7a3fbb0d9ad019e2e74f53c183c875d938` 均通过，证据 `/private/tmp/aicrm-profile-integrated-security/security.json` | 后续最终提交仍须通过完整 CI；不宣称零未知漏洞 |
| 生产发布包盘点 | 只读 inventory、服务与进程引用已核验；本地证据 `/private/tmp/aicrm-release-inventory.zE7KA0/README.md` | 未删除历史包；没有两份真实成功且兼容的回滚证据时保持阻断，不能用目录时间或完整包校验冒充成功发布 |
| 独立审核 | 代码和高风险维护脚本已有独立代理审查；main 目前要求严格 required check | GitHub required approving review count 为 0，CODEOWNERS/last-push review 未强制；作者声明及代理审查不等于受保护分支独立批准 |

临时证据位于执行机器；CI artifacts 与最终生产验收分别留证。生产部署、清理、Provider 接受、原群可见和连续 24 小时验收不能由这些本地结果替代。

真实生产 inventory 的拒绝是安全保护结果，不能改写为“无异常”：当时 current 为 `cb274776148b8b300e05ff0ae4fb18bd57e6885c`，数据库 migration 178 条、最高 `0184`，当前 SQL 文件的 version/name/checksum 与只读快照逐项一致。但 current 含 480 个未登记的 macOS `._*` sidecar，严格产物所有权校验失败；165 个 release 条目中，独立保守盘点得到 79 个完整包通过校验，却没有任何一个同时满足已安装 schema 一致的回滚候选。inventory 因 `current_package_or_installed_schema_unverified` 拒绝。`plan-v2.json` 是失败后空文件，禁止作为 apply 计划；`blocked-plan.json` 只是所有条目受保护、候选 0、候选字节 0 的诊断记录。通过完整包校验的 14,099,388,641 字节并非可删除量。备份始终未进入候选。

最初进程引用查询观察到 current 和旧 `142d58a183f64e4c82f177c1cabdb2cb657d647d`；之后仅可执行文件查询未再发现旧版本，不能推导 cwd、fd 或其他引用全部消失。下一次正式计划仍需新鲜 schema、服务与进程引用证据，以及两个完整且 schema 兼容的回滚包；“保留当前加两个”目前是执行保护约束，尚未达到生产可清理条件。共同安装锁只协调遵守该锁的发布/维护进程，不保证任意不合作进程在最终引用检查后不会打开旧文件。

生产只读容量盘点支持首期优先治理发布产物、按Owner小批清理数据库的选择，无需立即大改业务表或部署分区扩展。具体主机地址、容量原件和主机配置核验记录保留在受控运维证据中，不进入公开源码。备份建设、验证和清理不在本次执行范围。

2026-09-18补充群内只读核验：已在原群实际看到原机器人的“系统运营巡检小时报”历史；群名及消息内容仅保留在本地验收记录。原系统唯一enabled且valid的Webhook与新机固定凭据逐字节核对，root 0600的目标证明只保存凭据摘要。该核验未发送消息、未启用治理；新V3报告的Provider回执及群内可见仍须部署后分别验证。

最终交付仍须依次取得：最终干净提交的精确 HEAD/tree 与完整 GitHub Linux CI；已部署版本及迁移读回；管理员鉴权与真实页面/后台链验证；飞书 Provider 接受及目标群内可见回执；至少连续 24 个唯一小时窗口的真实验收。业务故障仍只报不自动修复，`outcome_unknown` 不盲目重发。生产权限、配置或外部资源不可核验时必须显示缺口，不以本地测试或 HTTP 202 代替实际完成。

## 明确不在范围

备份建设、恢复目标及备份清理；自动修复身份或资金；新增收费服务；引入 Redis、Kafka、Kubernetes 或另一套队列与外部效果内核。
