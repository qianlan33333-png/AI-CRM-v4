# PRD-07 群运营

状态：批准开发；先读 00-control.md；Terra high。

## 1. 基线与分类

V3 internal/groupops 已有计划、节点、Store、调度、效果绑定、素材准备和回执接线，但发送与目录 Provider 仍缺真正可执行适配。旧版来源以 `docs/migration/groupops/pr06-donor-manifest.yaml` 和 donor 哈希清单为准；复用 web/donors/groupops-v2 与现有 Host。早期 preparation-only 文档的授权限制不是本轮完整迁移的限制，实际实现范围以本 PRD 为准。

OneID：纯群计划/群绑定不涉及 Customer，不人为添加 customer_id；若旧行为确需客户资格读取则通过现有 Port 分类记录。持久化：本地 UoW、共享持久调度、Provider read、outbound 群写效果。

## 2. 用户流程与缺口检查

- 旧版计划列表、新建/编辑/启停/归档/删除草稿/预览，群及员工选择、素材/内容包选择必须完整映射真实 API。
- 支持旧版即时节点、延时节点、执行时间及有序消息；重启不丢后续节点，重复唤醒不重复发送。
- 执行前验证群绑定、发送权限、内容和素材准备凭据；图片/文件/小程序媒体凭据缺失或过期须明确失败，不能伪造 media_id。
- 素材管理复用 media Owner 与现有准备流程；不在 groupops 直接上传 Provider，也不新做素材页面。
- 取消/暂停、部分失败和未知结果延续既有合同；执行结果回到原记录页，不以仅排队为完成。

## 3. Owner 与接口

groupops 管计划/版本/节点/运行/业务回执，media 管素材与准备收据，outbound 管企微写，External Effects 管可靠执行。复用 groupops/port 的 dispatch/runtime/staff/material 相关契约与现有 API。

冻结节点内容、素材、群绑定、发送人政策和 effect binding；需要原子的业务状态、收据、事件及效果接受同 UoW。内部延时使用现有 River scheduled job，不建 groupops cron/lease/retry 系统。

## 4. 历史与前端

迁移已有群计划、节点、素材引用及允许的历史执行只读事实，复用现有 importer，历史记录不得自动恢复为待发送任务。无法确认 Provider 绑定的配置保留并标明不可执行。

原 UI 哈希/交互不变，Host 最小适配。源页面确实没有的扩展管理界面不开发。

## 5. 测试及验收

- 旧版计划所有可见操作及群/员工选择、素材预览、节点顺序与结果展示对照。
- 本地 Provider 完整即时→延时→下一节点 journey；故障注入与进程重启后只执行一次。
- PG 并发触发、暂停/取消竞态、事务接受失败回滚；unknown 不换键，终态不误重启。
- 媒体未准备/已过期、失效群、发送权限变化、Provider disabled 的确定行为。
- 历史计划/节点/绑定结果桶对账；相关 race/PG/协议测试通过；未合并 PR 交付，不向真实群发消息。

## 6. 总控验收证据定位

d6 `cmd/aicrm/group_ops_runtime_integration_test.go:TestGroupOpsPostgreSQLJourney` 证明真实Owner Store/EER事务和结果投影，但通过手动调用RunAttempt执行两个效果，没有实际启动River完成延时节点、重启、暂停/取消竞态。因此不能单凭该用例称完整群运营runtime已验证。

补完整真实共享运行时journey：即时节点执行→进程停启→到期后延时节点执行→后续节点顺序；已接受后暂停/取消及权限/群绑定变更应符合旧合同；同一效果未知不换键重发。复用既有运行内核，补实际 Provider 叶子适配，不能用测试适配器替换真实缺口后宣称完成。

## 7. 总控复查确认的实现缺口

在集成分支4fe92c1（群运营仍为d6基线）实际复查：composition 注册的 outbound.GroupMessageProvider.Execute 即使 enabled 也固定返回 final_failed/provider-not-configured，GroupOpsDirectory 仍为 providerDisabledGroupOpsDirectory。此前“已挂载Provider”的描述只代表对象接线，现更正为发送与目录适配未完成。

- 从只读旧系统冻结实际群写入与群读取协议、发送人政策、素材行为和未知结果处理；在 outbound 既有责任边界内补叶子 Provider，在 wecom 的稳定读取 Port 后补目录，不新建执行框架。只做本地协议服务验证，生产仍默认关闭。
- runtime.buildDrafts 当前把同组同一时刻的多个消息节点全部接受为独立可执行效果，仅 ScheduledAt 相同不能保证顺序。核对旧版即时/延时的顺序语义，以既有共享任务与领域运行事实补依赖检查；同组不能后节点抢先，前节点 unknown 不可被后续节点冒充整体成功。不同群是否独立按旧合同验证。
- 接受后暂停/取消、计划版本、群绑定及发送资格变化必须在实际发送前经既有 Owner Port 再核验；不只验证接受时状态。Provider 网络不得持有业务事务。
- 目录 RefreshOperationMembers/RefreshGroups 当前把Source读取放在UoW内，接真实网络时须先事务外拉取再原子保存完整快照；读取失败或分页不完整不能清空现有目录。
- 复用 `cmd/migrate-v2-config-definitions` 与现有历史导入工具，核对计划、节点、素材引用及历史只读记录的逐条结果；不要另造migrate-groupops框架。现有历史页有读取服务，不代表所有历史来源已导入验收。

已定位供体叶子：`aicrm_next/channels/integration_gateway/wecom_group_adapter.py`、`wecom_customer_group_client.py`，外层接纳在 `platform/platform_foundation/external_effects/adapters.py:WeComGroupMessageExternalEffectAdapter`，计划物化在 `automation/automation_engine/group_ops/scheduler.py`。旧写协议为 `/cgi-bin/externalcontact/add_msg_template`，目标字段 `chat_id_list` 必须精确包含冻结群；读取为 `groupchat/list` 与 `groupchat/get`。缺msgid、非空fail_list不能报全部成功，返回msgid仅表示企微任务已接受，不能宣称成员已收到。复用叶子协议时以V3未知结果原键恢复规则覆盖旧层将所有网络异常都标retryable的行为，不复制不确定写入的盲重试。

## 8. PR148 审核要求与已批准的必要补齐

第7节的 not-configured / disabled 是待修复现状，不是必须保留的实现限制。开发必须接通真实叶子适配，配置缺省仍关闭；本轮不打开生产开关。仅协议叶子加单测不能作为板块完成。

- 批准使用预留0078保存群运营自有 Provider 任务收据：唯一 effect_id、唯一 execution_id、msgid、冻结sender/chat、任务证据摘要、送达状态/证据与时间。经已有GroupMessageReceiptWriter/Reader访问；无原始响应、凭据或日志身份泄漏。与执行完成投影及领域后续动作需要原子的事实同UoW；Provider调用保持事务外。保存失败必须保留未知结果，不能产生可盲重发的假失败。
- 批准在现有 Completion Sink、groupops运行/节点事实和共享jobqueue之间补最小衔接。前一节点满足旧版继续条件后，同一事务记录结果并投递后续内部任务；任务重放检查既有唯一effect binding/节点状态，再以同一稳定标识接受后续效果。延时沿用共享scheduled job。无需River内建工作流功能，不另建队列、lease、重试或调度框架。unknown/暂停/取消不能放行后续节点；多群独立性按旧版合同测试。
- EER Attempt增加只读EffectID以供既有DispatchExecutionReader取冻结Owner事实可以接受；必须核对返回记录与Envelope的source/target/payload/policy四摘要全匹配。不能只比较记录内部的content/material摘要。不得改变effect标识或重试语义；补兼容测试。
- 区分发送前读取失败、确定拒绝、已尝试但未知、企微任务已接受及实际送达。补已存在的ProviderResultReceived事实；不能因为缺msgid将已经尝试的网络调用记成未尝试。发送前检查素材有效期、发送资格、群绑定与计划版本。
- 完成配置装配、原UI、PG真实River即时/延时/重启顺序与回执读取旅程后再申请板块审核。旧版历史配置/节点/只读执行导入仍须逐条对账。

2026-09-05复核[企微官方创建企业群发协议92698](https://developer.work.weixin.qq.com/document/path/92698)：群目标由chat_id_list指定，需企微终端4.1.10及以上正确支持；allow_select不能作为群目标保证。接口创建成员待操作的群发任务，不直接证明群成员收到；后续节点有序接纳与独立送达核验必须分别展示和验收。

## 9. PR148@299617b 送达读取审核与下一交付批次

本批次先完成可用的送达读取和冻结素材有效期，再交准确提交及测试证据；完成本批不代表整个板块通过。旧任务已结束，接手者独占同一干净clone，不与其他开发者同时写。

- [官方发送结果93338](https://developer.work.weixin.qq.com/document/path/93338)要求请求同时带msgid和userid；当前GetGroupMessageSendResult缺userid，必须使用Owner任务收据冻结的发送人，不能接受调用者覆盖。
- 当前仅unknown可ManualReconcile，而有msgid的正常记录是provider_accepted；未知网络结果又没有msgid。补现有结果/读取操作，使正常已接纳任务可查实际送达，不把EER已执行事实回退为unknown。没有msgid的未知结果如实保留，禁止假装查到或重发。
- Provider分页读取在UoW外；按冻结msgid/sender/chat验证证据，在新UoW核对原绑定并保存GroupOps送达事实。调用者无需知道受保护msgid或自行构造Provider证据摘要；摘要由可信读取Adapter产生。无独立证据保持pending/unknown，创建任务成功不能当送达成功。
- 复用Media的GroupOpsMaterialSourceCapturer、PreparationReader和既有准备凭据。必要的source/preparation digest与ReadyUntil随不可变业务意图持久化（仍用未部署0078）；实际调用前确认有效期及冻结附件匹配。不得临时上传、换用后来修改的内容包或新增媒体工作框架。
- 补协议用例：请求userid、分页精确发送人/群、正常provider_accepted可读取、无msgid未知保持未知、过期/未准备素材零写调用。真实PG覆盖结果持久化与漂移回滚。随后完整PRD仍需实际River即时/延时/重启、有序节点、旧UI及历史逐条对账。

6e63659增量复查：新material_source_snapshot同样经过JSONB，摘要和后续比较不能依赖原始字节的键序/空格。统一冻结与读回规范化后校验，实际PG round-trip必须能执行一次正确发送，漂移/过期则零调用。送达读取需保留重放和单调事实：较早pending回包不得覆盖已证实送达；查结果不更改原效果标识、不新发消息。

## 10. 后续运行、页面与历史批次的复用定位

0078收据批次通过后，沿原clone继续实际共享River即时/延时/停启旅程。现有`TestGroupOpsPostgreSQLJourney`仍手动RunAttempt，只证明Owner事务；不能替代真实运行时。独立两事务并发结果写入必须保留，不能以串行覆盖断言代替并发验证。

现有群页面由`internal/groupops/ui.go`挂载冻结`groupops.html`与`groupopsDetail.html`，历史通过`?history=1`进入。先以完整构建产物验证这些实际入口和Host API，不根据缺少单独新适配文件断言页面不存在，也不重新写前端。

历史读取由0017的四个`group_ops_v1_history_*`不可变表提供；目前有读取/页面，但总控搜索现有cmd、scripts和configmigration未发现这些表的实际导入写路径。`cmd/migrate-v2-config-definitions`现有模式只导入暂停的活动配置，成员映射留空、素材引用仅保存在legacy事实中，verify主要核数量/暂停状态。因此须在既有独立导入入口补明确的冻结历史模式及Owner写Port，保留旧窄模式的默认边界；不另建migrate-groupops框架。

先核对冻结供体历史计划、节点、群绑定、目录及旧版实际执行记录来源，区分活动配置与只读历史。每条来源有稳定键、内容摘要、目标或隔离原因；verify须读取实际目标字段与源事实逐条比较，并测试字段漂移、重复、故障回滚。导入后使用现有历史HTTP及冻结页面读取；不创建River任务或Provider效果，不把历史员工ID当当前Access主体。需要新增迁移时先由总控分配编号，现有0017不得重写。

## 11. 原可见操作范围确认

按现有冻结供体及dd8原group_ops.js复核，计划没有“复制计划”操作；“复制地址”只针对Webhook地址。删除第2节先前泛列的“复制”计划要求，不新增Clone能力。继续验证实际已有的创建、编辑、启用/暂停、归档、删除草稿、群/成员/素材选择、节点编辑及执行记录。本轮新增结果查询仍按Provider已接受任务与独立可信送达事实区分，不能将不可判定状态显示为送达。

## 12. 暂停后的重新启用（4e51后源码复查）

冻结controller对非active计划调用activate；当前Go Activate只允许draft，paused计划会返回state conflict，导致旧启停流程断开。下一增量允许已暂停计划在同一内容、成员及群校验后恢复active，沿用CAS、幂等收据与同UoW事件；archived仍终态。重新启用不得复活已终结/未知的旧效果，旧revision的任务仍被dispatch guard阻断。补实际Service/PG的draft→active→paused→active、重放、旧版本拒绝及无效配置，不能只用浏览器fixture回200模拟成功。


## 13. 未配置 Webhook 的多计划创建（0081）

真实 Store.Create 与 PostgreSQL 检查发现0012对 `group_ops_plan_webhook_descriptors.reference` 全局唯一，而新计划默认写入空串，第二个未配置 Webhook 的计划因23505失败。这是旧有多计划能力的阻断缺陷。批准0081归GroupOps：保留0012历史迁移，用 `reference <> ''` 的唯一部分索引替换全局唯一约束；空值代表未配置，非空opaque引用仍唯一，不生成假Webhook地址。沿用现有安装/迁移契约。实际PG覆盖连续创建多个计划、非空重复拒绝、清空后的引用复用；不涉及新身份或外部效果。


## 14. 历史导入收据补齐（0082）

0017四类历史事实已存在；HistoricalStore/HistoricalJournal已有稳定Port，PostgreSQL缺少写实现。现有0030配置导入的domain/target_table约束不接受历史，不能改造成另一个Owner或把历史伪装为活动配置。批准0082归GroupOps，仅补实现既有历史Port所必需的导入批次、行收据/映射与隔离记录。每行以来源作用域+种类+稳定键识别，保存源摘要、目标摘要或明确隔离原因，与对应历史事实同一UoW提交；verify必须回读全部事实而非数量。

独立入口仍为cmd/migrate-v2-config-definitions的明确历史模式；保留当前默认配置模式。不存在的历史员工不做当前Access外键或猜测映射；不解析/新建客户，不创建运行计划、内部执行任务、External Effects或外部发送。历史模式输出受保护的逐条核对结果，日志仅安全摘要与计数。真实PG覆盖四类成功/重复、未找到历史父计划、非法行隔离、重跑、目标漂移和事务失败不留下半条映射。无需新导入框架或治理页面。

供体目录的chat_id为文本，而0017对group_chats要求数字source_id；不得hash文本伪装来源数字。0082可前向修正此历史投影约束，原文本稳定键由明确来源字段/受保护行事实保留，source_id只保存真实来源数字。历史userid同理，不hash成owner_staff_id，不用当前Access代替历史；必要的创建/修改人类型适配须基于供体证据保留原值与兼容读取。

已核实供体plans.created_by/updated_by为TEXT，可为空。0082允许历史数值字段NULL，新增source_created_by_reference/source_updated_by_reference/source_owner_reference；当前业务Plan不动。TEXT即使是纯数字也保持来源引用，不因格式猜为StaffID。历史DTO以nullable兼容读取，原有数值历史不重写。

## 15. 0082真实来源与历史读取复核

供体0015的action_title/text_content独立于content_package_json；只提内容包会丢旧正文。完整受保护源快照须包含标题、正文、附件和真实空trigger_time_label；合法空标签不能伪造时间，0017非空CHECK须由0082前向适配。所有JSON字符串使用标准JSON编码，不用Go引号转义冒充JSON。历史列表和详情统一恢复字段，并在既有Host看到实际内容；OpenAPI同步nullable来源与新增读取事实。Preflight也经GroupOps Owner Port。验收必须真实源PG旧DDL→ExtractHistory→加密冻结文件→Apply/Verify→HTTP与历史Host读回，手写Snapshot只能作为附加测试。


## 16. 历史内容原页面验收

PR153@629e384已交付真实旧PG提取、密封、Apply/Verify及历史HTTP回读。web已有历史Host四GET的独立模拟，不足以证明同一导入记录在发布前端可读。执行者应复用既有构建Host/JSDOM与PG HTTP夹具，把同一批计划、群、节点事实带到实际Host中读取；同时验证原目录/计划进入节点的操作。

历史节点须沿旧只读内容组件显示标题、正文和原附件引用/可用状态，不让用户依赖原始content_package JSON辨认正文。优先复用冻结只读内容渲染器，仅在Host定义必要兼容DTO；冻结generated health schema、原业务JS/CSS哈希不修改。缺失或暂不可读取附件明确保留原事实，不因展示而创建发送、素材或客户。此次是旧历史读取能力接通，无新增诊断/编辑/导出产品。
