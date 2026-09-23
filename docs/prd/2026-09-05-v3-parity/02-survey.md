# PRD-02 问卷旧能力补齐

状态：批准开发；先读 00-control.md。模型 Terra high；沿用“问卷”。

## 1. 基线与分类

复用 `docs/06-PRD-问卷全能力与历史数据迁移.md`、V3 internal/survey 和已冻结问卷 UI。旧版参照 AI-CRM 的 `aicrm_next/extensions/forms/questionnaire/`。旧 PRD 的“没有 survey 领域”、切流与生产完成描述已过时；当前已有领域、迁移和大量处理能力。

OneID：可信 OAuth/企微上下文解析与既有显式 Provision；历史归属只解析不猜测。持久化：本地事务、内部持久任务、Provider read、必要的提交后 Provider write；复用现有 CompletionIntentAccepter 与效果内核。

## 2. 当前差异

| 能力 | 基线事实 | 本轮要求 |
|---|---|---|
| 定义、提交、历史导入 | 已实现并存在历史数据 | 以行为矩阵验证完整性，修复遗漏 |
| OAuth 入口 | 代码存在，生产开关关闭 | 完成本地可信会话和配置错误场景，配置应用留待部署 |
| 历史归属 | 大量 unresolved，另有真实匿名记录 | 有证据才重解析；两者不混为错误 |
| 外推和后续动作 | 有业务协议及效果绑定 | 验证真实接线至本地测试 Provider，不能仅保存 queued |

## 3. 用户流程

- 后台创建、编辑、复制、题目排序、预览、发布/停用、结果查看、分析与导出；题型、校验和旧版已有评分行为对照供体，不新增测评产品。已实现的 V3 评分功能不得倒退删除。
- 公开 `/q/{slug}` 复用旧 UI 与既有 OAuth，授权回调经可信 Adapter 获取带 scope 身份后建立受限提交会话；不信任前端 customer_id 或自报 verified。
- 提交冻结题目、选项、答案、版本及结果；编辑问卷不改历史答案。重复同载荷返回原结果，漂移冲突。
- 配置的后续外推/动作复用现有冻结引用、事务接受与状态读取；历史外推日志只读导入，不重新发送。
- 客户档案/侧边栏通过 Survey Port 读取已确认 customer_id 的记录；历史匿名和无法关联的答案完整保留。

## 4. 契约与 Owner

Survey 拥有定义、版本、提交、答案、配置引用、业务效果绑定、导入收据；身份由 identity 解析，企微写由 outbound 所有。提交业务事实、收据、审计、Outbox、后续效果接受需同 UoW；网络在事务外。

保持现有 admin/public/sidebar 路由与 DTO，在 Host Adapter 兼容旧页面。completion Port 只传 opaque reference/digest，不将答案、URL、密钥塞入 External Effects。

历史 source key/digest 保持幂等；旧问题定义不存在时仍按提交快照可读。可信 UnionID 必须有开放平台 scope，不将缺 scope 或手机号答案作为自动归属证据。

## 5. 测试及完成条件

- 覆盖四种旧版题型、必填/other/边界长度、两种答题交互、发布版本漂移、历史答案保留。
- OAuth 错误 state、过期/重放 code、错误 scope、冲突身份、匿名绕过；Provider disabled 零真实调用。
- PostgreSQL 并发重复提交、效果接纳失败整批回滚、外推进程重启和 unknown 不换键。
- 历史快照的定义/提交/答案/日志逐行对账；无身份与定义失配答案不能静默丢失；历史数据不得生成新发送任务。
- 前端 journey 对照 frozen donor，相关 race/PG/契约测试通过；输出 PR 与逐项证据。

## 6. 排除与依赖

不新做问卷引擎、通用身份治理台、Radar 或支付；不修改 customer 同步文件。若既有外推连接器存在真实缺口，补最低限度 Owner Adapter 和协议测试；未开发的新治理方案先报告总控。

## 7. 外推兼容合同补充

- 供体 `forms/questionnaire/external_push.py` 保留 phone_number（无值为 NULL）、day、frequency、expires_at_ts、type、remark、custom_params 和已有 assessment_result_snapshot；题目标题及选项文字按提交快照序列化。
- 问卷供体明确使用 registered_webhook_client，最终 `platform_foundation/external_effects/adapters.py` 的 WebhookAdapter 调用 `auth_platform/webhook_hmac.py` 的 WebhookHmacSigner：HMAC-SHA256 正文为 Unix秒、换行、稳定 event_id、换行、原始正文；请求头为 X-AICRM-Client-Id、X-AICRM-Timestamp、X-AICRM-Event-Id、X-AICRM-Signature。`platform/external_push/service.py` 的点号签名在这一执行链被覆盖，不能仅据该辅助函数推导问卷最终协议。使用接收方 verifier 的冻结向量验证。
- 旧 user_id 是外部身份语义，不能直接替换成 `customer:<id>`。配置引用明确目标需要的 kind/scope，通过现有可信 Identity Port 读取并冻结；缺失证据保留可解释状态，不猜测。
- migration 0067 仅用于 Survey-owned 兼容配置元数据及不可变外推快照。含答案、手机号或外部标识的快照沿用现有加密设施；URL/Secret 仍留在受保护配置映射，EER 仅存 opaque 引用/摘要。
- 配置修改不能改变已经接受的外推正文或目的地版本；请求不跟随跳转，错误不得泄露 URL 或正文。数据库读取暂时失败与 Provider 不确定结果分别处理，未知结果不盲目重发。

## 8. 总控复核补充：旧版测试外推

旧版 `operations.py:queue_external_push_test` 和 `api.py` 的 `/operations/external-push/test` 是实际能力：配置启用后，以 `user_id=questionnaire_test`、空答案、`phone_number=NULL`、`is_test=true` 与独立 test_run_id 形成合成测试请求，经原效果内核签名发送。它不读取真实客户答卷，过滤含身份语义的自定义参数。

V3 本轮初次外推 PR 的该路由仍固定调用 `RecordDisabledOperation`。这不能代表配置启用后已复用上述旧行为。补齐该旧入口：默认关闭时保留明确拒绝；启用且有效配置时，生成不可变合成测试快照并复用现有 Outbound/EER、同事务收据与幂等；不借用或创建 Customer，不让测试触发真实会员开通。测试仅连接隔离本地接收端。需验证管理员认证/CSRF、重复与载荷漂移、关闭配置零网络、敏感自定义参数过滤、签名及结果回读。

## 9. 旧页面保留与验收反例

`web/src/admin/controller.ts`、`web/src/api/admin.ts`、`web/src/shared/api/client.ts`均属于冻结供体，修改在V3 Host实现，禁止更新哈希/白名单绕过。不能把整页stage换成新外推表单，原提交后动作、渠道码选择、导航目标、分享入口、统计、两页签与日志筛选必须保留。

在原外推卡片/日志容器做最小挂载或事件接管，原位置按钮只发一次POST；不能保留先发出请求再因旧DTO而提示失败的入口。新记录回读真实执行事实，旧未采集字段保持未知；显示日志不能硬编码queued。完整测试必须加载实际冻结模板和runtime，验证连续保存、页签重绘、提交后动作以及当前/全局日志，不以手写相似DOM替代旧整页行为。

- 冻结测评预览页 `#v2-publish-save` 的按钮文案承诺发布；其供体点击只保存定义时，由 V3 Host 在该按钮的成功保存后复用既有 `public-publish → detail readback` HTTP 合同。Host 不伪造 active：仅在 Owner 回读 `status=active`、`enabled=true` 及 `public_path` 后展示公开链接。普通 `#save-btn`，包括 `is_disabled=true` 的保存，不进入该发布链，仍由 Owner 保持 draft/disabled。冻结普通编辑器对刚保存草稿发出的 `/enable` 被 Owner 拒绝时，Host 仅重放为 `public-publish`；已有不可变定义的重新启用仍直达 `/enable`。
## 10. 总控历史导入验收补充

既有 `cmd/migrate-survey-v2` 只含SQL文本断言和加密单测，尚无真实PG整份快照导入/重放/对账证据。其 `reconcile` 当前只比较source_map行数，未验证逐行源摘要和目标事实，却输出silent_loss=0等结论，不能作为本轮验收通过证据。这是既定历史导入要求的缺陷修正，不新增数据治理范围。

- 保留原独立加密快照工具，使用冻结合成快照，在隔离PG16实际执行validate/import/replay/reconcile；目标默认disabled、历史日志只读、无新Customer/身份/Provider效果或任务。
- 逐行核对source/table/pk、record_digest、目标存在及该类型的不可变业务事实；隔离记录同样有结果。不允许只数映射表就称零丢失或零误绑。
- 同批次键对应不同manifest须明确拒绝。已映射问卷的重复导入也需核对选项及评分规则，不能因跳过父问卷而忽略子行漂移。输入校验失败返回安全错误，不panic或忽略错误继续提交。
- 在冻结快照中覆盖正常问卷/答卷、缺定义答案、未解析身份、无归属旧日志；再注入源选项漂移、目标删除/事实漂移及事务中途失败，确认逐条对账或明确失败且无部分提交。已有工具的rollback能力保持原安全边界，不执行生产导入或回滚。
- PR146预审细化：旁存的源digest不等于重新验证目标事实。必须核对实际定义、题目/选项/规则、答卷归属/身份状态/结果、答案得分/选项/受保护正文、历史回执状态/时间/关联及结果token绑定。正文对账复用已有Cipher，可要求受保护data-key-file；缺密钥不能声称全文对账通过。结果token冲突若属于另一答卷须明确隔离或拒绝，不能ON CONFLICT DO NOTHING后报告无丢失。测试须含保留digest却修改目标业务字段及错误客户绑定的反例，并在确已插入新事实后注入故障验证整批回滚。

## 11. 最终用户流程联合验收

总控对当前集成复核：冻结e2e已有后台创建编辑、H5 all/one、字段校验和模拟HTTP授权；137/146已有外推与历史实PG证据，不重做。复制/排序/预览/发布停用/结果分析/CSV导出缺完整动作证据，OAuth回调到同一答卷亦缺实PG/HTTP链。先补既有能力的Characterization/Journey，发现确定缺陷再最小修复；不新建问卷引擎或改冻结前端。

- 从准确已审核集成版本创建独立工作区。使用实际迁移、Survey/Identity Store及现有HTTP/Host；唯一外部替身为隔离OAuth/外推协议接收端。
- 实际冻结后台页面创建、编辑题目及排序、复制、预览、发布/停用，验证每个动作的实际DTO/状态回读；结果、分析、CSV内容与同一提交一致。复用现有JSDOM/Host测试工具，新脚本放V3自有目录。
- OAuth state签发→本地Provider兑换→可信同作用域OneID→受限提交会话→公开all/one提交→result token读取；不能以mock customer resolver或手工插入session冒称整链。覆盖错误state/重放/错误scope与匿名绕过。
- 同载荷并发提交只一份答卷/业务收据，改载荷冲突；发布后编辑不得改历史题目/答案/结果。沿用已有验证器，失败回滚依实际PG证明。
- 只读分析及导出不创造任何效果。记录本地PG跳过和CI实跑分别结果；已有测试覆盖则引用，不堆重复测试。

## 12. 实际 OAuth 状态落库的前向修复

完整PG/HTTP旅程已证实0018的survey_oauth_states_redirect约束在标准SQL字符串中使用了双反斜杠，合法Host路径无法创建OAuth state。0090分配Survey，以前向迁移修正该既有约束，保留all/one页面与slug允许范围；不改变身份机制或OAuth入口。原0018不改。同步Store PG fixture、实际约束readiness与发布迁移清单；验证两个合法入口及非法/外站路径拒绝，继续完整OAuth、同键提交、结果授权与历史版本旅程。0089已由Outbound使用，不得占用。


## 原测评键的兼容性修正

实际冻结测评保存被V3通用ASCII opaque校验拒绝。只读供体证据：repo_support.py的_text不改原字符串，repo.py保存题目/选项维度与类型key到TEXT；冻结编辑器保留中文key，原默认值包含“用户维护”“暖男/女型”，新增维度可为“维度 1”。这些是config、type_priority与题目/选项之间的业务引用，不是OneID或安全凭据。

按旧能力优先，仅Question.AssessmentDimensionKey、Option.AssessmentTypeKey、AssessmentDimension.Key、AssessmentType.Key改用Survey专用业务键validator：有效UTF-8、非空且无首尾空白、最多128字符、无控制字符，原值原样持久化。维度/类型唯一性、priority成员和题目选项引用校验保持；通用validOpaque、标签、sidebar字段、template/asset及任何外部身份/完成参数不放宽。不添加中文到英文/摘要的映射机制。

验证旧中文、内部空格、斜线键保存与重读不变，重复/错配引用继续拒绝；实际冻结管理页保存发布、修改后再发布及真实PG结果维度/类型关联回读。Owner仍Survey本地UoW，无新队列或外部效果。

真实PG随后证实0018的两个dimension/type CHECK仍只允许ASCII。总控分配0091_survey_assessment_business_keys.sql，仅以前向迁移替换这两条Survey约束，保留已有0018不变。数据库与领域允许原中文/内部空格/斜线键，保留非空、128字符上限和控制字符/首尾空白边界；禁止用永久NOT VALID绕过历史校验。Readiness必须识别旧约束不满足交付，新迁移进入现有安装清单。真实PG覆盖旧库前向迁移、原键保存重读和非法键拒绝；不新增映射。

保存并发布的Host适配必须绑定同一次V3 Adapter保存Promise，使用该成功结果中的准确ID/版本发布并读取Owner状态确认。禁止用500ms等时间窗口猜测后续哪个fetch属于发布手势。原冻结UI不改；普通保存、校验失败后的普通保存不得发布，异步请求延迟不丢失原发布意图。边界仍是现有questionnaireEditorV3保存接口与Host，不新增任务/队列/持久化机制。导入器对原中文维度/类型键逐字保留；非空非法键在既有snapshot校验前置拒绝，不能转为空串后标记成功。
