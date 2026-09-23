# AI-CRM v3 自动化运营 PRD：OneID 人群、持久执行与企微效果闭环

- 状态：Approved for implementation
- 产品范围：仅自动化运营
- v2 供体：`AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`
- 关联 ADR：`docs/adr/0005-自动化运营-OneID-持久快照与外部效果边界.md`
- 实施计划：`docs/plans/2026-09-03-automation-operations-oneid-durable-execution.md`

## 1. 产品结论

在 v3 单一管理后台提供完整的人群配置、OneID 人群快照、固定话术与发送人绑定、自动策略、人工群发、持久运行、企微写入和证据化对账。v2 只提供冻结行为、测试、协议和前端外观，不成为运行依赖或正常数据源。

本板块不建设通用工作流、完整 Campaign、AI 自动生成、短信、邮件、朋友圈或身份合并。`groupops` 保持独立，只经稳定 Port/EER 复用已有设施。

## 2. OneID 与持久化分类

- `customers.id` 是唯一收件人主键；外部身份归 Identity，必须带 kind、scope、value、assurance、source。
- 本板块只读取 canonical Customer 和解析可信 scoped identity；不得建客、绑身份、猜测客户、自动合并或复制 v2 customer ID。
- 无唯一可信证据时进入 unresolved/conflict/invalid/quarantine，不得入群。
- 本地状态、幂等收据、审计、Outbox 和 effect acceptance 在同一 PostgreSQL UoW 提交。
- 可恢复内部工作用 River；Provider read 是只读 Adapter；企微写只经 Outbound，并由 EER 持有 attempt、fence 和 reconciliation。
- Provider 网络调用不得持有数据库事务。

## 3. 用户、权限与目标

用户包括自动化运营管理员、审核人、客服发送人和审计/运维人员。所有管理写操作要求有效 Session、RBAC、CSRF、幂等键和显式版本；并发写使用 CAS。

目标：

1. 管理员可从闭集模板创建、复制、预览、刷新、暂停、激活和归档人群包。
2. 每次执行使用不可变的人群、内容、发送人和策略版本。
3. 重启、重复投递和并发不会造成重复入组或重复外部效果。
4. 页面能准确区分本地接受、排队、调用、Provider 接受、投递证明、未知和已对账。
5. v2 当前配置和历史可审计迁入，迁移自身产生零 River job 和零 EER effect。

成功指标：成员集合排序稳定且摘要可复现；重复请求结果一致；100,000 人快照可分批物化；身份错配为零；所有真实发送都有 effect、attempt 和 Provider/对账证据；任何未知结果均不被展示为成功。

## 4. 功能范围

### FR-01 分组和人群包

支持分组增改删、稳定排序，以及人群包创建、复制、暂停、激活预检和归档。人群包生命周期为 paused/active/archived；归档不可执行。

### FR-02 闭集定义和不可变配置

只接受版本化闭集 AST 与固定模板，不执行任意 SQL、表达式或脚本。每次保存创建不可变配置版本，保存 canonical JSON digest、刷新模式和 UTC cron。定时配置由 River 扫描持久游标；停机后折叠到最新到期时间，不补发历史洪峰。

### FR-03 预览、刷新和发布快照

预览只返回数量、摘要、reference time、source watermark 和身份桶，不返回全量客户标识。刷新经 River 执行，排序去重 canonical customer IDs，分批写 staging；完整性和摘要校验后，事务内原子发布新快照并保留旧快照。

### FR-04 签名成员事实

Webhook 要求 HMAC、时间窗和 event ID 防重放。请求中的身份只能以 declared 进入，后端经可信 Identity Resolver 得到唯一 canonical Customer。未命中或冲突只记录收据/审计，禁止建客和入群。

### FR-05 Agent、固定话术和发送人

复用现有 Automation Agent，只保存 published version/content digest/materials digest 的不透明引用。首版仅允许已发布、active、材料受支持的 `fixed_script`；`agent` 类型不可执行。发送人只保存内部 `staff_id` 与资格版本，企微 userid 在 Outbound 执行时即时解析。

### FR-06 策略与 Enrollment

策略和策略版本不可变，包含 package、trigger、action snapshot、quiet hours、single-run limit 和 approval staff。首期生产触发器仅 `audience.member_entered.v1`；`customer.tag_applied.v1` 保持 disabled。事件与策略版本形成唯一 enrollment，重复事件不重复执行。

### FR-07 人工群发

必须先生成 15 分钟有效 preview，冻结 package version、published snapshot、content version、sender set 和 target digest；确认时重新读取并逐项比对。任何版本漂移、收件人数变化或超过环境上限均要求重新预览。

### FR-08 Outbound/EER 闭环

Automation 在同一 UoW 创建 run、recipient、message intent 和 EER effect binding。网络调用在事务外执行，执行时重查 OneID 出站身份、发送人 provider identity 和 published content digest。

状态必须分离：accepted、queued、attempted、provider_accepted、delivery_proven、retryable_failed、final_failed、outcome_unknown、reconciled。调用后超时进入 `outcome_unknown` 并停止自动重试；只允许原 effect 查询、可信回调或携带 generation/fence/lease/evidence digest 的人工对账。

### FR-09 前端

页面入口为 `/admin/automation-conversion` 和 `/admin/automation-conversion/packages/{id}`。使用现有单一 webshell；冻结 donor 文件不承载 v3 业务，真实 API、状态映射和扩展控件归 v3 Adapter。禁止 Mock、sessionStorage 和 hardcoded member。

页面必须显示 loading、empty、stale、conflict、forbidden、not-ready、unknown 和 reconciliation；accepted/queued 不显示为发送成功。

### FR-10 v2 迁移

先用只读凭据盘点 schema/count/source key/identity/effect，再生成 AES-256-GCM 加密快照。按分组、配置、Agent 引用、发送人、当前快照、策略、历史迁移。旧 active 一律 paused；旧历史只读、不可重放，不创建当前 run/job/effect。

每个 source row 必须进入 imported/duplicate/mapped/unresolved/conflict/invalid/quarantine 之一，满足数量等式。完整重放零新增。Shadow 在同一 reference time 对比配置和 canonical member digest。

## 5. 领域所有权与稳定接口

- Segment：分组、人群包、配置版本、refresh run、published snapshot、成员、Webhook 收据、审计、Outbox、调度游标。
- Automation：Agent、policy version、enrollment、run、recipient、action snapshot、运行对账。
- Outbound：message intent、执行读取模型和企微 writer adapter。
- EER：effect、attempt、generation、fence、未知结果和对账。
- Identity/Customer：canonical customer 和可信 scoped identity 解析。
- Access：内部 staff 资格和运行时 provider userid 解析。

稳定接口包括 Audience Snapshot Reader、Audience Execution Configuration Reader、Automation Published Agent/Policy Reader、Identity Audience/Outbound Resolver、Staff Eligibility Resolver、Outbound Transactional Message Accepter 和 EER Transactional Accepter/Reconciler。禁止跨域表访问、跨域 Store 引用和级联外键。

## 6. API 契约

- `/api/admin/ai-audience/*`：分组、包、模板、配置、预览、刷新、快照成员、Agent 绑定、发送人和激活预检。
- `/api/admin/automations/*`：策略创建、版本更新、激活/暂停/归档。
- `/api/admin/automation-runs/*`：preview、confirm、列表、详情、收件人、取消、未知效果候选和对账。
- `/api/webhooks/ai-audience/*`：签名成员事实。

分页必须稳定；错误使用明确 400/401/403/404/409/413/422/503；写入使用 `Idempotency-Key` 和预期版本。OpenAPI 两份镜像必须字节一致。

## 7. 数据、隐私与非功能要求

- 数据库、EER 和结构化日志不得保存手机号、openid、unionid、external_userid、消息正文、Token、Secret 或 Provider 原始响应。
- 固定正文只归 Automation 内容模型；EER 只保存不可逆摘要和 opaque receipt。
- source snapshot 与 key 文件必须 `0600`，不得进入 Git；数据库 URL 只从具名环境变量读取。
- 人群最多 100,000；批次可重启、可幂等重放；快照发布原子；旧快照保留。
- 所有运行带 release SHA、correlation/effect IDs 和可审计时间，不记录身份值。

## 8. Provider 上线策略

自动化运营使用独立于全局 EER 的三态门：disabled、probe、limited。默认 disabled。probe 必须同时启用全局 EER 与 WeCom、提供窄权限 `fixed-script-send-authorized`，并强制每次最多一名收件人。limited 需要明确审批后的上限；业务服务在 preview 和 confirm 两次校验。

单收件人探针只有在 Shadow 全绿后进行。Provider receipt 与 EER 最终状态是验收证据；若身份错配、数量漂移、证据缺失或出现 outcome_unknown，立即恢复 disabled 并停止扩量。

## 9. 验收 Journey

1. 新建人群、保存闭集配置、预览并物化，重复刷新得到相同 digest，旧快照仍可读。
2. 定时刷新跨进程重启后只执行最新到期项；新成员事件重复投递只生成一个 enrollment/effect。
3. 保存 fixed_script 与 staff，版本漂移或 staff 失效使激活/发送 fail closed。
4. 群发确认必须使用同一 preview digest；目标变化或超过 rollout cap 返回 conflict/not-ready。
5. Provider disabled 时零真实调用；调用前失败可重试；调用后超时进入 unknown 且不盲重试；过期 fence 无法对账。
6. 前端只调真实 API，所有异常状态有可见反馈，未知不显示成功。
7. v2 dry-run、apply、replay、reconcile、shadow 通过；所有源成员唯一映射，导入零任务/效果且 active 均变 paused。
8. 发布后真实 route、release SHA、capability readiness、单人 Provider 调用和对账证据全部可复核。

## 10. Definition of Done

完成不以目录、接口骨架、HTTP 200、queued、green CI 或 merged PR 单独判断。必须同时具备：冻结供体校验、真实 API/页面、PostgreSQL 事务测试、River 重启/重复测试、EER unknown/fence 测试、OpenAPI、迁移加密快照与重放、Shadow、生产 release SHA、真实路由、默认 disabled、经审批的单人探针证据，以及迁移/探针 Runbook。
