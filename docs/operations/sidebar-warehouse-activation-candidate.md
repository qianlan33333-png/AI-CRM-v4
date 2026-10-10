# 侧栏真实接通候选：漂移源码与服务权限

沿用已批准的 `docs/prd/2026-10-09-sidebar-ai-operations.md`。腾讯 CRM 技术应用已安装；本候选只准备 Context Center 的当前源码补丁、最小服务权限及合成验证，不启用活动服务或配置秘密。

分类：OneID 继续读取既有 customer/corp/employee 范围，不创建、匹配或合并客户。Persistence 继续使用已实现的单次手动生成 claim/result，不新增队列或 worker。External Effects 仅是独立批准后的单次模型 API 调用，unknown 不重试；没有企微发送。UI 不变。

对外合同保留 GET `/api/local/v3/sidebar-ai-operations` 与 POST `/api/local/v3/sidebar-manual-generation`。新增可选 Basic 服务身份 `crm-sidebar`（role `sidebar_api`），仅允许这两个方法/路径；复用 CRM 现有 Basic Adapter，无旧 Agent token 传递。密码未装配时账号不生效。既有 admin/operator、Agent scopes、startup 恢复逻辑不扩权；错误配置与已有用户名冲突时启动前拒绝。

限制必要性：把已有 operator 凭据持久交给 CRM 会同时授予策略/批量等无关能力。新增的最小约束仅作用于新的服务身份和两个接口，保持正常员工范围与完整生成链路。参考 [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) 的最少权限、默认拒绝和逐请求检查。

五项影响：合同字段/方法未变；业务机制增加可选最小服务授权；直接消费者为55 HTTP handler、CRM现有Reader/Generator、固定exporter；无页面变更；验证覆盖真实新候选的旧逻辑保留、HTTP身份/方法/路径、私聊范围、冻结快照及持久幂等。

活动 API 在准备期间再次更新，最终绑定 `159821ea35a1bff271d2dece742563ca2c70d7429fd51d99932604e5079a9820`；旧候选基线失效。构建仍按准确摘要拒绝漂移，不用旧文件覆盖。独立候选、当前源码副本与测试均不含环境、数据库或凭据。最终stage收据另附准确head/tree及逐文件摘要。

需要用户决定的是：是否为腾讯 `124.220.53.183` 的 CRM API持久开通新账号 `crm-sidebar` 的两个侧栏能力。需要安全输入新密码；无需读取或搬运旧token。复用已有operator不新增服务器角色，但仍将宽权限授予新的持久调用方。没有可不增加服务访问又完成当前服务端自动读取/生成的现成路径；保留人工在Context Center操作可保持原权限，但不是目标全链路。

代码准备和合成测试已授权。新账号/持久配置、活动服务安装重启、生产数据发布与真实模型验收在具体候选与影响审阅后执行；未获得必要批准前不操作。重启前重复只读核验队列，保留暂停批次和所有unknown收据；恢复原源文件与配置时同样重核任务，不回退或删除已落盘claim/result。

## 可审查候选与实际验证

独立 worktree `codex/sidebar-warehouse-activation` 从国内 main `43beb48370de0e5ae5c40931aa9fb29b417a38df` 开始。55 隔离目录 `/opt/daru-v3-warehouse-candidate/candidates/sidebar-ai-rebased-20261010-470f99eb9b07`，内容包摘要 `470f99eb9b07ee877aef2dc3c8c25fdadfeaa4a85e547fd5686c204b68379e6e`。待安装 API 摘要 `71fc75d46bacf6dc0c65c0de14ecd9f233bc161d659e154b6363ca2315972eaf`。逐文件和当前 native 依赖摘要见 `integrations/context-center/sidebar/candidate-stage-receipt.json`，旧候选收据另存 historical 文件，不能用于覆盖活动源码。

31 个相关合同通过、无 skip：实际候选 HTTP 方法/身份/注册保护路径拒绝 11 项，原有生成及幂等 8 项，Reader/诊断 7 项，published snapshot 1 项，完整 AST 保留 1 项，当前八模块兼容 3 项。首次运行的授权夹具不能解析实际 `frozenset` 常量，类初始化报错，原始失败保留；其他20项确实通过。只修复夹具后，按失败范围重跑11项；复用20项分别绑定相同测试、运行时和当前 native 源码摘要，不把首次整次失败说成通过。网络连接被禁用，Provider 使用合成 stub；没有真实模型调用。完整 AST 对比证明现有 Agent allowlist、全部原有处理器、原生生成改动及启动恢复函数保留。

实际调用链为 `NativeEngine → PromptRuntime.build → copy_engine_v2.build_bundle → hxc_writing_inputs`。本次按当前四个依赖源码运行，而非沿用旧 copy engine 摘要；合成资源覆盖已冻结双向私聊、历史摘要不升格为当前确认、持久完成收据复用、缺主场景不调用模型、产品目录过滤。共享运行时、真实提示素材和客户数据没有被复制进 v4 业务模块或改写。

普通清单仅四个直接 Python 合同，不改 Go、Host/UI 或 CRM 应用载荷，也不重复上一技术包的 Chromium/构建/安装。55 的真实 Linux 兼容与 AST 收据随本候选交付。隔离候选 README 是构建时审计快照；本次后补文档和 stage 收据不属于活动运行载荷，构建器、测试、SQL和四个模块均按收据摘要绑定。

## API 授权方案

| 项目 | 明确方案 |
| --- | --- |
| 新身份 | API Basic 用户 `crm-sidebar`，role `sidebar_api`；不新建系统/SSH账号 |
| 允许读取 | GET `/api/local/v3/sidebar-ai-operations`；已有 customer/corp/employee 身份范围的一致 published 资料、最近最多40条本员工双向私聊和最后客户消息 |
| 允许生成 | POST `/api/local/v3/sidebar-manual-generation`；人工确认冻结快照后单次生成，持久 key 复用，unknown 禁止重试，结果只用于复制 |
| 其他权限 | 现有保护 API、策略、批量、Agent操作等全部403；公共 health与匿名的既有公开入口无新权限 |
| 调用机器 | 腾讯 `124.220.53.183` 的 CRM API；不是 worker 或未切流的新火山 CRM |
| 凭据输入 | 人在原生安全配置入口生成并装配同一新密码到55 `DARU_SIDEBAR_AUTH_PASSWORD` 和CRM `AICRM_SIDEBAR_WAREHOUSE_BASIC_PASSWORD`；账号项为 `crm-sidebar` |
| 模型凭据 | 仅55既有模型运行时使用原配置，不读取/搬运 Ark/Kimi key 或旧 Agent token；此次不输入旧token |
| 最少权限替代 | 复用旧operator仍把宽权限持久授予CRM，不是无扩权方案。人工在既有Context Center核对/生成/复制不扩权，但未完成CRM直接接通 |

CRM继续由合法当前员工会话决定身份范围，禁止用户注入员工或客户归属。新服务账号只缩小方法/路径，不取代该身份守卫。实际现有账号名和密码未从服务环境读取；源码默认名不能当实际账号证明。新用户名与现有账号冲突时在恢复任务之前拒绝启动，安全配置者须先确认不冲突。

## 重启风险、部署与回退

2026-10-10 09:09:23 UTC只读事务：可重启恢复的 Agent queued/running为0；copy batch pending/running/regenerating为0；1个paused父批次下6个pending子项。现有恢复函数不会选paused父批次，但每次启动会将running Agent转queued并启动所有queued，恢复选中批次并重置其running子项。此刻0不是未来保证。当前main有HTTP graceful shutdown，systemd `TimeoutStopUSec=3min 30s`、`KillMode=control-group`；强制停止仍可能中断模型请求并留下unknown。

该队列检查期间观察到其他操作者使服务PID从491789变为493365（17:09:12 CST启动），随后核验源码及四个native依赖摘要仍一致。不是本任务重启；本任务不更改现有队列或服务。安装前须与现有55运维持同一维护窗口，确认没有在途模型请求，并重新核对摘要、队列及暂停项；出现新queued/running或不明执行立即暂停。

1. 获得本方案的API持久访问/安全配置批准及活动55安装重启批准后，由唯一工作台协调55 owning release流程。先读回当前源码、服务、队列；只保存当前3个来源文件与配置回退元数据，不备份/迁移普通数据库，不处理别人的进程。
2. 使用本候选7个文件的准确字节。以当前基线摘要校验后原子安装 owned文件，保留当前其他模块和copy engine。新身份、模型、CRM两个开关保持关闭；安全输入新密码由人接管，助手不读取或传递。
3. 维护窗口内由owning operator正常停止/启动原unit，等待请求排空并读回PID、摘要、认证/两接口和队列。状态不明先只读对账，不盲目重启。保持既有paused批次、claim/result及unknown记录。
4. 先完成下述导出桥映射，再单独更新腾讯固定exporter为同一摘要 `3344a491…bab77`，核验root所有及固定命令权限。获数据发布授权后才执行范围字段刷新；既有 `--refresh-crm --run --publish`还会`sync-onboarding-identity-links.py --apply`及`agent-user-schedule.py`刷新人工审查队列，不能称为只读scope刷新。
5. 验证新published版本具备`crm-private-message-scope.v1`与四项私聊范围字段、CRM/warehouse一致版本，再仅启用Reader；真实销售会话核对最后客户原话和时间水位。生成开关继续关闭。
6. 获得真实模型单次验收批准后才启用两端生成，人工冻结并单次生成；同key重放复用，未知结果不换key补调用，不发送企微消息。单列真实Provider及销售业务验收收据后才说明全链路完成。

回退：先关闭CRM生成/Reader和55新身份/生成开关，保留全部claim/result。仅恢复本次安装前捕获的3个准确当前来源文件并撤回4个新文件的路由引用；不回退到旧`118844…`或`2603fb…`版本。凭据撤销/配置回退由安全操作者处理。按同一窗口再次核对队列、排空请求后正常启动，并独立读回既有API/认证和暂停状态。需要回退exporter时同样恢复本次准确原文件，不逆向修改已经发布的数据或删除收据。

## 导出桥未验证项与批准边界

唯一工作台已复用其既有腾讯发布连接，只读确认固定入口 `/usr/local/libexec/aicrm/crm-production-read-facts-v1.py`，root/0644/摘要 `972b5abe…`，没有私聊scope版本与四项字段。sshd有效配置没有ForceCommand，限定wrapper/收据范围也未发现绑定；公钥条目`command=`未检查。因此55目标host/执行账号/端口/固定远端命令与该文件的真实绑定仍未确认，不能用发布账号ubuntu证明。

拥有者需在受保护配置中核对55 `DARU_CRM_READ_SSH_HOST`的非秘密目标/账号/端口，以及腾讯对应授权条目固定命令最终执行的账号和路径，只返回这几个非秘密结论；不提供私钥、公钥条目、密码、token或完整环境。自动审批已拒绝助手读取`/proc/PID/environ`和`/etc/daru-*.env`，原因是完整内容可能含凭据，即使只输出白名单仍有不必要凭据探测风险。该动作未执行、不重试；此查证由拥有者安全接管。

允许立即继续：准确源码提交/国内推送、正常工作台交付、已有合成验证与只读取证。需必要决定后继续：新 `crm-sidebar`持久权限和安全输入；55 active安装/重启；固定exporter与生产数据发布的实际影响；单次真实模型验收。旧CRM技术发布授权不自动覆盖这些新增持久访问及55服务行为。真实侧栏接通状态仍为未完成。

附加全仓fast运行失败：`cmd/aicrm/commerce_recovery_webkit_postgres_test.go`在platform/config之外读取环境；该文件与本候选base一致、未修改，原日志保留。不能把相关31项通过称为fast或全仓回归通过。
