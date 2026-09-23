# PRD-05 自动化运营旧能力补齐

状态：批准开发；先读 00-control.md；Terra high；沿用“自动化运营”。

## 1. 基线与分类

沿用 `docs/13-PRD-自动化运营-OneID与持久执行.md`，复用 V3 segment、automation、outbound、media 和 frozen UI。旧版参照 ai_audience_ops、automation_agents、已有 send_records 与自动化绑定行为。

OneID：读取 canonical 客户，解析发送资格；不 Provision/合并。持久化：本地事务、受众与调度内部任务、Provider read、outbound 外部写效果。

## 2. 已有与缺口

人群包、计划/策略、话术、发送人、版本和执行内核已存在。当前装配的受众来源覆盖不等于界面全部条件覆盖；现有 ActiveWithin 使用目录 updated_at 不能解释为真实客户互动。生产未执行不等于缺少代码。

## 3. 需求

- 对照旧版实际可用条件，补齐人群定义、预览、刷新、发布、成员列表与入选原因；每个旧条件对应一个已有领域数据源 Port，不能允许任意生产 SQL。
- “活跃”按旧版已确认的业务事件口径移植；目录同步不能冒充用户活跃。来源缺失返回可解释的不可用，不造人数。
- 话术、素材、发送人、策略启停、时间及免打扰规则保持旧版；本轮不新做通用流程编辑器。
- 人群成员进入等旧版实际触发器形成唯一 enrollment，重复事件不重复执行；已有不支持/禁用的旧条件不得凭菜单文案编造能力。
- 手动群发和自动任务均先形成可预览的不可变目标/内容，沿用既有确认操作；实际执行前复核当前发送资格。
- Provider 的接受、执行、未知和结果证明分别回到原记录页面，失败可在现有内核恢复。

## 4. Owner、UI 与历史

segment 拥有人群定义/快照，automation 拥有策略/运行/enrollment，outbound 拥有发送意图和企微写；通过稳定 Ports 和既有版本化事件接线。审批/运行事实与效果接受按需要同 UoW。

保留原人群、Agent、策略和发送记录页面；仅改 V3 Adapter。旧配置及可归属历史记录以现有 importer 延续，历史运行强制只读，不因导入重新触发。新策略默认不自动向生产客户执行。

## 5. 测试及验收

- 每个迁入条件至少一组真实 PG 夹具，验证目录更新不扩大活跃集合、过滤交并行为与旧版一致。
- 预览/发布版本漂移、成员进入重复/乱序、停用、免打扰和发送上限。
- 事务失败全部回滚，重启继续原任务，unknown 不改键重发；测试 Provider disabled 零网络。
- 本地完整人群→策略→批准/触发→发送→结果 journey，旧版 UI 操作对照。
- 历史导入重放及 source 收据对账。输出未合并 PR 与测试证据。

## 6. 非目标

不实现新的指标平台、统一消费者治理、完整 Campaign、AI 生成、短信/邮件/朋友圈或新的身份冲突产品。确实需要补一个旧条件的数据读 Port 可在本板块完成，不扩大成平台重构。

## 7. 总控供体定位与语义冻结

供体 `extensions/ai/ai_audience_ops/template_registry.py` 实际有六种模板，逐项映射现有V3领域数据与Owner Port：

| 旧模板 | 必须保留的关键语义 |
|---|---|
| wecom_contact_registration | 负责人、active/deleted 联系状态、any/registered/unregistered 注册状态 |
| questionnaire_choice_answers | 首次完整提交；题间 AND、题内选项 OR，负责人范围 |
| paid_order | 商品集合、支付时间左闭右开、负责人、有效企微联系人选项 |
| channel_entry | 渠道集合、距进入时间上下界、负责人、有效企微联系人选项 |
| radar_first_click_elapsed | 多雷达 OR，以首次可归因点击为锚点；后续点击不重置；小时/天窗口 |
| member_usage_status | 负责人、服务期、注册、真实使用、会员层级/状态 |

原刷新选项 every_3m / daily_0200 / manual 已有V3持久调度机制，复用相应映射。受限SQL旧入口只允许供体 allowlist 读视图，不意味着可在V3直接跨领域表跑任意SQL；先核对已有DSL编译和兼容入口，保持旧表单操作并通过Owner只读Port获取事实。

d6 composition 实际只装配 `segmentadapter.CustomerSource`，仅处理 ActiveContacts；其 `customer/store.ActiveWithin` 基于目录 updated_at。执行任务需明确覆盖上述六模板，不能把仅有AST或菜单当作已接线。真实使用/注册若已有业务同步数据经其Owner读取；缺少可信数据时明确不可用，不能临时以目录同步或存档收到消息替代旧口径，也不把修复扩大为新分析系统。

paid_order补充冻结：供体模板只选择status=paid；供体迁移0065的orders_v1来自wechat_pay_orders.unionid，对应付款OAuth身份。因此使用可信PayerCustomerID，不能替换成权益受益人，也不擅自把部分退款加入受众。历史已支付订单先核对Order是否有可信paid_at事实；没有status_history时不加时间窗口仍可按paid状态纳入，有时间窗口但没有可信时间则不匹配并保留原因，不能猜时间。现有TestPostgreSQLPaidProductProjectionUsesHistoryAndExactFallback的fallback是商品ID到精确商品码，不是支付时间回退；不得据其推断source_metadata中存在paid_at。补付款人/受益人不同、部分退款、历史paid无状态历史反例。

本板块获准在WeCom、Survey、Order、Channel、Radar、HXCDashboard各Owner新增独立audience_read/port文件提供必要只读事实，由cmd适配到segment契约；Owner不反向依赖segment。共享商品支付及权益已有实现不重写。HXC无已发布投影时显示来源不可用，不把缺失数据冒充空人群。

## 8. 继续实现检查

现有独立工作区提交6cb9133、faa7149仅为局部实现，尚未装配或提交PR。PaidAudienceOrders目前SQL无占位参数却传入reference.UTC()，需修并用真实PG验证；六种Owner来源均须实际查询及边界夹具，不能以编译通过代替SQL执行。保留已有提交继续开发，不重写已冻结接口。

局部LegacyTemplateSource.paid仅在require_active_wecom_contact=true时过滤owner；false时指定负责人条件被绕过，可能扩大受众。负责人条件与联系人活跃条件独立执行，加入“指定负责人且不要求活跃”的反例。HXC来源尚未检查是否存在published版本，不能把未装载投影当空受众。不要把这些局部实现直接启用或标记可发送。

## 9. 接手基线更正与下一批次

最新独立clone实际已有2063669a0b2ece180c3286151ce095369b7fbbf2（在faa7149之后）：已装配六Owner来源并修正SQL多余参数、负责人条件及HXC未发布检查。不要重新实现这些代码。先复核该准确提交的真实PG测试和冻结前端表单到六模板的HttpApi旅程，补缺口后提交以main为base的PR运行PG16检查。根未批准该提交，现有单测不替代全板块。

后续必须补人群刷新/发布→策略/人工群发→批准或触发→共享River→真实本地Provider→原结果页，以及旧配置/历史只读导入逐条对账。此批与03共用Owner读Port仅新增独立文件，冲突及时交根协调；无生产启用、无第二套调度。

## 10. 2063669源码对照发现的未闭环语义

- 原模板参数为owner_userids，V3 AST为owner_staff_ids；冻结HttpApi必须验证边缘转换及Access作用域内标识映射，而非让旧原页面直接发送新自造字段。channel_codes当前与数字ChannelID比较，也须验证原选项值、精确渠道码和历史映射，不能静默空匹配。
- Channel AudienceEntries目前只读channel_history_contacts。必须包含已可信归属的V3原生入客事实，依Owner Port读取已有entrant/assignment/binding；历史与原生相同客户/渠道聚合最后入客时间，重放不重复。不能使上线后新增客户永远不进入渠道人群。
- HXC member当前仅用到期时间推断active，ExpiresAt为空即true；这样会把registered_no_active_membership且无到期时间者选成服务中。按旧is_member与membership_status合同获取明确会员状态，expired也须包含显式expired且日期缺失的事实，不能凭空补到期时间或把注册等同会员。
- 原paid_order按Order来源owner_userid过滤；当前以任意当前企微关系过滤付款人不是同一事实。复核供体0065及后续迁移实际视图、现有Order归属Port，来源缺失时保留明确不可匹配，不擅自用联系人负责人补造订单负责人。
- Radar原0165视图使用可归因authorized/authorized_click及带身份的landing；当前只选内容打开/跳转等后续事件，可能丢失成功授权后内容失败的首次点击。按现有V3已解析事实与原事件语义对照；不能放宽到匿名landing，不能把后续内容请求当新首次点击。owner_scope=all时不要无依据额外要求存在当前企微关系。
- 六Owner真实PG边界用例、实际旧表单预览及每条目标守恒必须随提交交付。以上是旧行为及已入V3事实的接通，不增加新分析平台。

## 11. 原表单复用与Owner类型复核（PR152）

实际V3 `internal/webshell/static/admin_console/admin_audience_detail.js` 的renderTemplates目前只写“AST以JSON保存”，没有渲染六个原表单；savePackage仍读取packageDefinitionInput。仅接收owner_userids的后台转换不等于原界面可操作。

已定位冻结dd8供体 `aicrm_next/app/admin_console/static/admin_console/template_parameter_form.js`，包含TemplateParameterFormController及PackageTemplateController。原页面为`extensions/ai/ai_audience_ops/templates/admin_console/ai_audience_package_detail.html`，已有templateParameterForm节点与脚本引用。优先按必要文件字节冻结复用此脚本及现有原模板，通过V3 Host映射现有闭集模板元数据与接口；不重新手写六套表单，不修改现有冻结业务文件。

- 原表单的questionnaire/conditions.question/options、products、channels、radars需要由各Owner稳定Port解析到可信本地ID/精确code。原可输入稳定ID/code或精确标题；歧义失败，不猜一条，不将旧数字ID直接当新ID。
- owner_userids通过Access解析后持久化本地owner_staff_ids，回填也走同一Access映射；禁止把本地StaffID与Provider userid放入同一个字符串集合。真实Provider userid可为纯数字，必须覆盖StaffID9→userid bob和另一个userid9的碰撞反例。原生Channel Owner提供显式StaffID，历史Provider引用分开处理。指定员工且依赖不可用失败；all scope的空owner_userids不应被422阻止。
- 预览与保存使用同一规范化规则，保存返回可重开回填；激活期间只读状态仍按旧逻辑。实际控件生成的参数进入真实Owner查询的结果，才算六模板Host接通。
- refresh_mode沿原every_3m、daily_0200、every_3m_plus_daily_0200、manual实际选项映射现有River；不能忽略旧增量下拉框或把0200本地时间误作UTC。没有新cron/ticker或自建刷新Worker内核。

本批先保证各Owner查询与字段归属真实可用，再补原表单组合journey。所有PG测试使用真实迁移，不以手造删减约束的表替代；fixtures必须命中需要验证的SQL路径。


## 12. 六模板 Host 的实际装配修正（856c421审核）

CI33964237463通过不代表新Host已验收。新Host与已有admin_audience_detail.js同时渲染templateParameterForm，120ms后补画不能确保慢接口或重新加载时稳定；明确唯一表单渲染所有者，保留冻结原控制器。测试必须加载完整实际脚本组合、慢接口和重新加载，不能只执行新增Host。

原“保存基础配置”负责名称、分组及刷新选择，新Host拦截后只保存definition造成回归；维持原保存语义与版本刷新。原预览与保存新版本分别处理，先核供体控制器原语义，不能把预览改成写配置。datetime-local输入须按原业务时区转为后端有效时间并可回填，实际Go AST验证与预览通过，不能依赖无校验fetch fixture。精确标题/别名解析与四种refresh_mode仍按第11节恢复。


## 13. 四种旧刷新模式的持久化补齐（0083）

供体scheduler.py明确分别处理incremental与daily，repository_packages.py:710-711为组合模式保存两类定义；正常配置由同一个模板编译，不能将组合模式在新UI静默降级。现有V3只有refresh_cron_utc、单配置的next_due及schedule occurrence。批准0083归Segment，按必要范围扩展既有配置/调度事实以保存manual、every_3m、daily_0200、every_3m_plus_daily_0200及相应kind的下一到期/幂等发生记录，不新建Scheduler或队列框架。复用现有AudienceSchedulePeriodicJob、ScheduledRefreshService、AcceptRefreshWithin和共享River。

每日0200按Asia/Shanghai，3分钟保持原周期；组合模式保留两种触发来源及各自到期事实，重叠时遵循现有刷新接受、发布diff和发送幂等，不因两次扫描重复发送成员。旧refresh_cron_utc已有任意合法表达式必须保持原语义（可明确legacy/custom模式），不能把已有UTC2点静默改时区；只有新旧UI明确选择daily模式时按旧业务0200转换。前端完整回填四模式；配置版本及调度游标同所需UoW，暂停/归档不新接受，停机后使用现有有界恢复。

实际PG/River覆盖四模式、上海跨日、两扫描竞争、重复发生、启停恢复和历史cron兼容。变更仅恢复旧能力，不增加自动化治理产品。

## 14. 首次归因与会员状态语义补充

供体template_registry.py:220-247按首次点击时间与稳定事件序选一行；0165视图中的owner优先identity.primary_owner_userid、再事件staff_id_snapshot、再staff_id，不是一律取事件当时员工，也不得以任意当前企微关系替代。通过对应Owner类型化返回可证实的原优先级事实，缺失明确不匹配，匿名/pending/conflict仍排除。会员expired仅来自显式过期状态或expires_at<=reference；不能把dashboard的registered_no_active_membership当过期，active必须结合原is_member与reference时刻到期事实。HXC最小真实字段共享契约另由总控协调，禁止Segment自行建HXC同步器。


## 15. HXC共享字段执行边界

会员状态及03原会员表共用HXC事实按13-shared-hxc-facts.md，0084归HXC；不得以dashboard stage代替明确源状态，既有OneID/只读源/发布代复用。由单一执行者维护HXC文件，Segment只作Port消费。

## 16. 刷新种类的旧行为对照

供体dd8的refresh_service.py:_apply_diff中incremental仅upsert新增/变化，daily才将本次未出现的活动成员标为exited。0083独立cursor与同occurrence幂等仅证明调度去重，不证明两种行为一致。执行者须核对当前V3全量快照行为与旧六模板的实际规则，在现有Segment Owner持久事实/任务内补必要兼容，不另造刷新框架。保留原有legacy_custom语义；同一时间daily/incremental重叠需明确完整日刷新优先且不重复触发运营效果，并以真实Owner/任务证明。


## 17. 原主负责人事实恢复（0086）

供体dd8 identity_bridge_service.py在完整follow_users替换后调用identity_bridge_repo.refresh_external_contact_identity_owner：非空userid按字典序取首项；空集合不覆盖已有primary。0165 Radar读视图优先当前primary_owner_userid，再取首次可归因事件员工事实，不是任意当前联系关系。

0086归WeCom，只在已有受信profile/owner事实内补必要primary与来源凭据，并由现有同步UoW更新。必须证明输入是同一corp scope的完整Provider follow-user集合；请求中的员工、部分页、失败批次和不可信资料不能选出primary。空集合保留已有可信值，旧存量没有完整来源凭据保持unknown，不猜测回填。稳定批量读Port仅接收规范CustomerID与明确作用域，返回known/unknown/ambiguous；不能按冲突的多个profile随机挑人。Radar按原优先级读取；V3无历史事件员工事实时明确不可匹配指定员工，不借另一条当前关系扩大人群。

真实PG和既有共享运行时验证完整/部分/失败同步、空集合保留、重放和多作用域冲突；原有每员工联系关系不得因恢复primary丢失。本变更仅恢复旧字段，未创建第二套身份匹配或同步框架，生产存量填充留到正常同步部署验收。


退出事件范围复核：dd8 outbound_service.py:27/234/244及automation_binding/precheck.py:178只消费entered。日刷新exited保留为不可变变化事实及原回读，不新增退出运营发送触发；0087不为此分配。增量未移除成员、日刷新移除成员与进入发送幂等必须通过实际共享River验证。

## 18. 人工群发复用既有 AI 待审计划

冻结dd8实际来源：automation/ops_enrollment/application.py:ExecuteUserOpsBatchSendCommand 在confirm后调用UserOpsReviewPlanGateway.create_pending_review_plan，要求pending_review/draft及broadcast_job_count=0。它与subscription.requires_approval不同：automation_binding/repository.py的专用Agent订阅默认execute/false；automation_agents/worker.py对need_human_review=true返回human_review_required，false才生成enqueue_automation_send_plan，由webhook_service.py的专用流程自动批准。不能凭字段同名把这些流程混成直接发送。

本次修复仅恢复上述旧人工流程，OneID涉及既有canonical目标和Access资格，持久化涉及Automation运行事实与AI待审计划；审批后的效果仍归AI/Outbound现有内核。

- 人工群发继续复用当前预览、冻结成员/话术/发送人和CSRF确认；确认创建既有AI助手待审计划，进入已有AI审阅页。确认本身零outbound/EER发送接受，不能显示已排队发送。
- 使用aiassistant/port.Intake及既有PlanReader/整单审批。AI CreatePlan当前开启自己的UoW，而平台禁止嵌套事务；需要同事务时仅新增稳定的CreatePlanWithin窄Port，复用现有validateCanonicalRecipients/createWithin与Owner Store，缺事务明确失败。禁止把两次独立commit称原子提交。
- Automation保存自身运行与opaque AI PlanID关联；源snapshot/内容/发送人摘要、幂等收据、审计及AI计划同事务。相同确认或响应丢失重试返回同一计划，源变化明确冲突；保持实际接纳的目标/素材快照，不创建第二套客户或审批表。
- 为必要的现有运行关联/待审状态预留0087，Owner Automation；只扩已有业务事实，不新增队列、审批引擎或执行Worker。若既有字段已足够则无需迁移，报告实际方案。AI计划状态/执行结果通过既有稳定只读Port回到运行页面，不在Automation重发或复制AI执行事实。
- Host只做原操作按钮/反馈/进入既有审阅页的必要适配，冻结业务文件保持原样。上限遵循现有两个Port，超限明确拒绝或按旧已验证分批合同处理，不静默截断。
- 自动Agent仅保留已验证专用enqueue契约：need_human_review要求不被绕过；通用Webhook requires_approval不得被误当已人工批准，未有相应已实现契约时明确不可启用。不要为此新增通用Webhook/审批能力。
- 同一实际PG/HTTP旅程：预览确认→pending plan且零效果→内容审阅零发送→整单批准→共享River/本地WeCom协议→运行记录回读；重复/并发确认与审批不重复，UoW故障无孤立运行/计划。原自动entered和unknown恢复另行保持回归。

## 19. 注册条件按旧生产实际来源接线

2026-09-05总控在150.158.82.186的openclaw_wecom通过BEGIN READ ONLY、statement_timeout及pg_get_viewdef核实：实际audience_read.registration_status_v1只有external_contact_bindings JOIN people，is_registered为people.mobile非空，source为people.mobile。0059代码中的其他可选分支没有进入该环境实际view。六模板中的WeCom注册条件读取此view，而非HXC用户行。此前Segment用SharedFacts.Registered（当前HXC存在用户行恒true）会令未注册分支静默为空，必须修正。

- WeCom模板通过Customer稳定批量只读Port获取已canonical客户的目录手机资料是否存在；复用customer_directory_projection与现有更新/清除链，只有Customer Store查询自身表。不跨域查Identity表，不返回手机号或其hash，不新增持久化字段/同步器。
- 目录存在且手机资料非空为旧业务registered，目录存在且为空为unregistered；缺目录或读取失败为unknown/unavailable，不能猜成false。使用既有有界批次和当前UoW，返回安全来源/更新时间。
- 这是资料存在的业务判断，declared资料也不因此变verified；不能用该布尔做客户匹配、合并或建客。HXC用户是否存在与该条件分开测试：有HXC无手机、无HXC有手机、空手机、无目录均须守恒。
- member_usage_status仍要求已确认MembershipRecordFound且MembershipSource非空，避免any筛选把普通注册行算会员。它的registration来源在旧投影中另含HXC及people.mobile，需按已经明确的canonical Owner事实组合并记录unknown；不得把WeCom的注册字段与HXC注册事实混为同一来源。没有可归属事实的旧会员仍保留隔离结果，不以手机号重新匹配。

只读来源聚合用于范围核实，不代表数据已经导入V3：旧活动代22553行无会员/注册来源，963行会员来自user_ops_hxc_dashboard_snapshot且注册来源为people.mobile加snapshot，273行会员来自snapshot且注册来源people.mobile，131行仅people.mobile。上述是来源标签计数，不等于对应is_member或is_registered为true；本轮没有读取身份值、复制配置或修改生产事实。

## 20. 固定话术原素材的执行闭环

供体dd8 automation_agents/application.py及engagement/send_content允许图片、小程序、PDF附件和客户群邀请；PDF校验是原能力，不能仅保留编辑器再在发送时一律material-unsupported。旧专用enqueue_automation_send_plan将规范化content_package交给原发送计划并幂等审批，private adapter把附件随一次Provider请求提交。V3现有自动MessageProvider只支持文字、人工reviewContentBlocks拒绝所有附件，这两条路径尚不完整。

- 先列出既有Media、AI及outbound的可复用Port与原各类型实际叶子合同，给总控最小补齐方案；不得新建发送/重试/素材队列框架，也不以新AI生成能力替代固定话术。
- 人工发送沿第18节既有整单审阅；自动发送沿旧已确认自动执行契约。仅修最小内容表达和原写入Adapter，复用准备好的Media事实，接纳时冻结不可变内容与素材版本；等待期间重新发布Agent/修改素材不能改变原意图或导致换键发送。
- 同一逻辑目标的原内容包按旧协议组合发送；不得逐附件多次Provider写而只记录一个效果，避免中间未知后重复已发部分。确需拆分时只能复用已有独立效果粒度并先向总控说明旧合同依据。
- 图片、小程序、PDF和邀请至少各有完整PG/本地Provider协议证明，混合内容包、原数量与长度上限、禁用/缺素材零调用、素材漂移/进程重启、未知不重复均明确验证。原不支持的动态卡保持原拒绝，不把配置字段当作新功能授权。

最小存储方案批准：0089归Outbound，仅在已有outbound_message_intents补业务意图的不可变内容/Media来源快照及摘要，不新增执行表/队列。接纳者经现有Media Source Capturer获得同UoW可校验事实，内容快照、intent、EER接受和业务绑定同事务落盘，网络准备在事务外；EER仍只有opaque引用和摘要。已存原格式意图没有素材快照时不得把当前可变素材当历史补造，纯文字兼容路径与不可恢复记录分别明确。自动和人工复用现有PrivateMessage payload准备与Provider写入；执行前源素材校验失配明确失败，不重新生成意图或更换效果键。

## 21. 既有历史对账命令的最后核验

复用 cmd/migrate-automation-operations 与其 Shadow，不增加导入框架。现 Reconcile 只核对收据数量和导入时效果计数，尚不能替代原定逐条目标事实对账。member_grid 从冻结 f57d4c2 独立工作区承担此目录，automation_sources 继续运行时/素材/原UI；本批不需要新迁移。

- Reconcile 从同一受保护 source snapshot 读取并验证批次绑定，在标记 reconciled 前复用既有 Shadow 的配置/成员/历史目标字段与隔离记录校验；不要只检查持久化 source digest。
- 保留源收据但改变目标 state、occurred_at、readonly、成员归属、snapshot，或删除目标、改隔离原因/摘要时，真实命令必须拒绝；恢复后通过。
- 实际PG fixture使用既有River迁移；不能以自建 river_job 空表替代。历史 apply/replay/reconcile 不创建新的效果或持久执行任务。
- 新PR注明依赖PR152准确基线，只有依赖实现和本增量分别通过审核后才纳入总集成，不提前执行生产导入。

补充最终审核合同：0083已恢复旧刷新模式，历史配置导入必须显式保存同一规范模式并参加配置去重、目标逐条核验；同definition而刷新模式/时刻不同不可复用成同一配置。缺字段快照沿原明确兼容语义，不识别的模式明确隔离或拒绝，不静默猜成manual。真实PG覆盖manual及旧四模式、同definition不同刷新设置、目标refresh_mode漂移拒绝；继续保持导入后暂停且零River/Provider效果。本项由PR159沿既有Segment规则修复，无新迁移。
