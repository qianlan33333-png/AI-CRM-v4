# AI运营侧栏：准确候选与启用交接

原开发任务 `01a12136-1d2f-7186-9b90-62a976bc40bd`；父任务 Dot `01a1189c-2bc0-76da-97db-8b03e2cb2039`。本候选已对齐只读核验的国内 main `82edf652065beecf77409723c0a25a7581d0764d`，分支 `codex/sidebar-ai-operations`，仓库 `/Users/qianlan/Documents/Codex/2026-10-09/task-2/crm-sidebar-ai-operations`。最终 head/tree/ref、检查与投递结果以仓外 `candidate-receipt.json` 为准；不使用旧 HEAD 的绿灯证明最终候选。

代码候选已包含标准侧栏、受保护数仓 Reader、手动 native Generator、隔离的数仓安装材料。真实腾讯固定导出器更新、55 活动安装、配置装配和真实 Provider 验收尚未执行；业务链路尚未接通。没有切流、生产数据库连接、旧 Agent 停服或新定时任务。

## 已核实角色和源码

|角色|准确位置|实际证据|
|---|---|---|
|当前腾讯 CRM|`124.220.53.183`；本机权威项目 `/Users/qianlan/Downloads/新CRM/main`|迁移线程范围为前置准备；本候选使用国内 v4 源码，不读取其他审查 worktree|
|火山 CRM 演练|`115.190.113.5`|准备副本及独立演练，未切流，也不是持续更新的生产镜像|
|数仓/上下文中心|`115.190.115.55:/opt/daru-v3-warehouse-candidate`|已复用用户本机授权会话只读验证；部署目录无 `.git`；活动 unit `daru-v3-warehouse-candidate.service`，用户 `daru-ops`，Python API 仅监听 `127.0.0.1:8797`，既有 Nginx HTTPS `https://agent.lhbl.com.cn`|
|旧话术 Agent|`122.152.209.11:/opt/daru-ops-console`，实际登录账号 `ubuntu`|已只读核验源码；部署目录无 `.git`；包 `daru-ops-console v0.4.4`；活动 unit `daru-ops-console.service`，用户 `daru-ops`|

两台目标的实际会话均已验证，不再标为认证缺失。只读、隔离 staging 使用原有会话；没有读取、复制私钥、环境变量值或认证文件。旧服务器其他业务与黄小璨分身不属于整体迁移范围。

## 实际数据合同及依赖

浏览器只访问 CRM 同源 GET `/api/sidebar/v2/ai-operations`、POST `/api/sidebar/v2/ai-operations/suggestions`。当前 HttpOnly 员工会话及既有 scoped context 提供 canonical `customers.id`、企业、员工；生成前后及读取结束重新核验主体。浏览器不能指定这些身份或原始正文。

CRM Composition Root 已装配可选 HTTPS Reader 和 Generator，分别默认关闭。目标固定路径为 GET `/api/local/v3/sidebar-ai-operations`（`crm-sidebar-ai-operations.v1`）和 POST `/api/local/v3/sidebar-manual-generation`（`crm-sidebar-manual-generation.v1`）。既有 API 顶层认证及 admin/operator 判断不变；没有扩大 agent_read/agent_operator 的路径权限。双向正文仅在服务端传递；公开读响应只有清洗摘要、末句、真实水位和签名快照。

真实生产消息导出目前缺少 `corp_scope`、`staff_provider_user_ids`、`participant_count`、`sender_actor_type`。候选增加这些来源字段和 `crm-private-message-scope.v1`，保留原列和原行。Reader 必须验证企业、唯一当前员工、双人私聊、发送方角色，再取最多40条双向文本并保留最后客户消息；群聊、其他销售、未解析或歧义范围不可冒用。

55 的 `ops/export-crm-production-read-facts.py` 在 bridge 模式只调用远端固定命令 `crm-production-read-facts.v1`，实际 SQL 在 CRM 端执行。**仅改 55 文件不会更新腾讯固定导出器。** 腾讯固定入口实际文件/安装账号尚未核验，不能凭 bridge 标签推断它一定已指向 `124.220.53.183`。`DARU_CRM_READ_SSH_HOST` 等只核对名称，未读取值。

最新成功发布的数据是 `/releases/data/20261009T011758Z-archive-refresh`：CRM `source_as_of=2026-10-09T09:15:27.087634+08:00`，归档生成 `2026-10-09T01:16:21.065Z`。最近 refresh 失败于 `hxc-sync.py`，未发布/清洗；旧脚本未保存有效失败阶段，历史根因未知。候选仅为今后失败增加脱敏原因码，不把该修复冒称为已查明历史原因。既有管道支持仅 `--refresh-crm`，可以保留旧 HXC 水位；本任务没有运行它。

腾讯 CRM 可通过 55 现有受保护 HTTPS 消费投影，架构上无需等待整体迁云。实际腾讯→55 认证请求、TLS/权限、更新后的生产范围字段仍需核验；不开放私网 RDS、不新增隧道/防火墙权限。接口读取时间不是同步水位。

## 真实 Agent 迁入材料

已从旧 Agent 校验22个纯提示词/解析/质量函数及常量的来源 SHA，记录在 `integrations/context-center/sidebar/native-migration-manifest.json`。没有复制旧环境、CLI 授权、客户语料、SQLite、批量 worker 或恢复队列。55 已有较新的 `prompt_runtime.py`、`copy_engine_v2.py`、`prompt_asset_versions.py`；候选沿它的 `sales_assist` 资源合同映射已核验快照和 provenance，历史清洗摘要保持历史参考属性。

模型路径只使用原有 Ark/Kimi API 协议单次调用，不调用 CLI、自动重试、兜底模型或企微发送。实际提供方及凭据是否装配尚未读取/验证。手动请求先在既有私有 COPY_CACHE_DIR 排他、落盘 claim，超时/中断保持 `outcome_unknown`；同 key 不再调用，完成后复用结果；不复用旧批量任务的自动恢复逻辑。

55 最终隔离候选：`/opt/daru-v3-warehouse-candidate/candidates/sidebar-ai-manual-20261010-47ff24367bfe`。bundle SHA256 `47ff24367bfeaf0a7c9d92baa6335c20f39c8b07d6c030e7cf9b6ace16ee107d`，构建及 Linux 合成检查见 `integrations/context-center/sidebar/candidate-stage-receipt.json`。该目录不是活动安装，也不是真实生成收据。

## 执行前的三项具体计划

### 1. 腾讯固定只读导出器

先由既有发布/运维入口核实固定命令确实绑定腾讯主机、root-owned 文件及实际安装路径；该路径尚不明确，不猜测，不读 authorized_keys、私钥或环境值。比较准确源码与候选 SQL 后，只更新现有固定导出器的 SQL/版本校验，不改入口授权、数据库表、任务或 timer。每次固定命令启动 Python，预计无需 CRM 应用重启；若实际入口的打包方式不同，应由工作台补充影响。

更新只读事务增加4项来源字段与版本标记，读取后 ROLLBACK；不写业务表。随后如获准仅执行一次既有 CRM-only refresh，保留 HXC 水位并由原发布机制激活投影。它会重读生产快照及更新数仓发布数据，不能算本次已完成。回退保留旧固定导出器源码和上一已发布数仓版本，沿原机制恢复；不删除生成 claim，不把旧数据标成新同步。

### 2. 55 安装候选

安装前再比对活动3文件 SHA：exporter `972b5abe…`、local API `11884412…`、archive refresh `db292b5a…`；任一变化需重基和验证。候选替换这3个 `ops` 文件，并增加 `sidebar_ai_reader.py`、`refresh_diagnostics.py`、`sidebar_native_copy.py`、`sidebar_manual_generation.py`，复用已核验55 native runtime。不建服务、timer、队列或数据库迁移。

新路由加载需要重启既有 `daru-v3-warehouse-candidate.service`。已核实其 `main` 会调用 `resume_agent_ops_workers_after_restart`、`resume_copy_batch_workers_after_restart`，可能恢复原有待处理工作；本任务没有读任务数据，也不能宣称重启只影响新接口。工作台/操作者应先核对现有任务和停机影响，再决定实际重启窗口。新手动生成器不参加这些恢复函数。

回滚沿现有 owner 的文件/发布机制恢复原3文件和原运行配置，再重启同一 unit；保留所有手动 claim/结果收据。不得删除未知请求以获得“重试成功”。没有 PostgreSQL/SQLite schema 变更，无需数据库逆向迁移。

### 3. 既有账号与 API 配置安全装配

CRM 侧需现有保护 API 的 operator 账号、HTTPS origin 和分别启用 Reader/Generator 的开关；55 侧需 DARU 手动生成开关、既有 Ark/Kimi API 配置及现有私有 COPY_CACHE_DIR。完整变量名称在集成 README，无值。SSH 登录身份不能当成 API 账号；CLI 授权不能当成 API 凭据。

不创建账号/角色/新密钥，不扩大 agent 路径 allowlist，不改防火墙或 RDS。配置值由人通过原生终端或现有安全配置入口接管，工具/聊天不搬运、不读回。即使账号已存在，把 operator 凭据持久提供给新的 CRM 服务也可能扩大访问面；须明确其权限和是否有既有授权，涉及新持久权限/安全设置时取得本次批准。不能把已授权 SSH 会话等同于已批准该装配。

## 候选影响与验证边界

OneID：读取现有 scoped customer，经已有数仓 CID/alias/fold lineage 转换；不猜昵称/电话，不建新主键或隐式客户。CRM 持久化：无新表/迁移/后台任务。远端手动生成的最小持久 claim 由 Context Center native owner 持有，只用于一次调用防重；没有任务重试框架。企微业务写、订单/裂变/分销/开票、资金归属不改。

页面为实际标准 Host + overlay 的 AI运营“基础信息/辅助成交”。冻结 dd8 源字节未改；备用模板复用同一模块。共享 Host 变化要按主线映射验证真实消费者，不能用备用页截图替代。

本地最终 fast、全仓测试编译、受影响 Go/JS/Host 检查由仓外最终收据绑定。55 Linux 收据证明合成投影、原子 claim、并发/重启/超时、实际 native v2 资源合同和 stub Provider，不能证明生产字段、真实模型或配置已接通。真实腾讯→55、活动安装和实际 Provider 输出必须单列验收。

国内候选推送后重新读取 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json`，实际投递指定《CRM v4 发布工作台 · 新流程》，包含准确 base/head/tree/ref、五项影响、收据和以上启用边界。只交付本功能，沿原队列/锁/attempt；不自行安装共享 CRM 预发/生产。消息失败要记明，不把文件准备等同于投递成功。
