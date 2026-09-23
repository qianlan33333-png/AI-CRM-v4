# 运营 Runner 实际接入

本文保留实施前合同与环境调查；最终实现及验收细则见 [Runner 独立 PRD](../2026-09-07-operation-runner-runtime-closeout.md) 和 [安装运行手册](../../runbooks/operation-cycle-runner.md)。后续已确认的协议修正以这两份交付文档为准。
旧来源 extensions/hxc/operation_cycles/local_connector.py 与 docs/operation_cycles/local_codex_connector_runbook.md；V3 internal/operationcycle/http 已有固定Token独立接口，六项OpenPlatform范围不扩。只用已有真实执行器与本机安全配置，先核执行器安装位置、线程绑定、心跳/领取/结果协议差异，不发明执行目标。运行状态/任务由现有领域持久化，禁止另建CRM任务队列。连接器如需Go等价适配，由独立PR交协议、权限与恢复测试；不能只生成Token就宣称Runner在线。没有用户行动请求时不得启动Codex业务执行或外部发送。旧系统执行器保持独立，不将旧待执行动作切到V3。


## 2026-09-07 22:55 源码核对与实施前合同

固定供体 `AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`，V3 基线 `3f5ea38c702b91e37746c2ad1b7bf4f5174e5a2a`。旧源只读：`aicrm_next/extensions/hxc/operation_cycles/local_connector.py`、`tools/run_operation_cycle_codex_connector.py` 及原 runbook。V3 既有 Owner 为 `internal/operationcycle`，HTTP `serveRunner`、app `Claim/RecordActionEvent/Heartbeat`、store `repository.go`。

OneID：本连接器只领取运营动作、调用指定本地 task、回传脱敏结论，不解析或创建客户；后续客户读取/待审计划只能通过已批准 V1 Operation。Persistence：CRM 动作、事件、租约和线程绑定沿用 operationcycle 的 PostgreSQL UoW；连接器是现有客户端协议适配，不新增 CRM 队列或外部发送内核。调用 Codex 创建/恢复 task 是有状态操作，必须保留 request→thread→turn 关联和未知结果，不能重复启动。

已定位的真实差异，不能通过复制旧环境参数解决：

1. 旧客户端使用 OAuth operation_runner，V3 当前 Runner 独立 Bearer ServiceToken；不得复制旧 Token 当作 V3 有效授权，也不得扩充六项开放平台范围。
2. 旧 claim 请求 wait_seconds=25，V3 只接受 0；旧客户端从 `claim.request.request_id` 读取，V3 Claim 返回扁平 `request_id`。需适配现有版本协议。
3. V3 `RecordActionEvent` 校验到期租约；目前只支持 thread_bound/turn_started/completed/failed，Heartbeat 只刷新 runner 行。须验证超过 ActionLease 的实际长任务如何续租、进程重启如何重新取得同一个动作并恢复已有 thread/turn。现有代码只领取 queued，不能将几秒内模拟完成冒充可恢复长任务。必要修复只扩展现有 Owner 的协议/事务，不另建调度或 lease 框架。
4. 固定 Codex 版本与 Unix socket 握手、指定本地 binding keys→目录、只回传脱敏结果仍采用旧合同；错误版本/断线不能自动退到 codex exec 或猜选项目。
5. 旧生产查询已确认没有已登记 Runner，也没有运行机器/目录/socket 绑定参数。实际部署位置仍待用户明确；先交付可安装、可验证的实现，不能伪造生产心跳。

实施先审阅旧测试和当前 stable Port，形成最小协议差异，再独立 PR。验收覆盖真实 PostgreSQL 的租约到期、并发领取、重启恢复、相同事件重放/内容冲突、长任务续租及终态；固定版本协议测试覆盖 socket 不可用、创建/回传失败、已绑定 thread/turn 不重复创建；本地实际握手与生产在线心跳分开记录。没有用户业务动作时不得为验收创建运营内容或发送。
