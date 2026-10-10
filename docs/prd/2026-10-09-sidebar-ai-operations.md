# 侧边栏 AI运营：基础信息与辅助成交

用户已批准本范围，优先于 CRM 整体迁云。入口沿用蓝白卡片工作台，将核心画像改为 AI运营，二级为基础信息/辅助成交；旧 timeline API 与数据保留。基础信息展示上下文中心清洗摘要。辅助成交显示数仓已归档客户末句、发生时间与同步时间，销售核对当前企微会话后手动生成、复制、发送。刷新只读数仓现有投影；不拉取上游存档、不调模型。当前腾讯→数仓同步并非快速更新，不承诺30秒或迁云后无延迟。

## 已核实来源与范围

- 国内实际 main 6b28130afa0f2b8e784b3e7372faf3451d33cfda；独立 codex/sidebar-ai-operations。
- 迁移对话 01a11e64-e0ad-7d93-a114-c5121379276b 可访问8轮，用户/助手消息已核对；工具执行正文未读取，凭据行排除。最后结论为 PREPARATION ONLY。迁移准备目录 /Users/qianlan/Downloads/新CRM/migration/volcengine-production-20261009，清单及 workbench/18-PREPARATION_RESULT.json 是准备证据，不能视为切流批准。
- 腾讯现网124.220.53.183；火山CRM测试/迁移准备115.190.113.5；清洗/数仓115.190.115.55；旧生成来源122.152.209.11。后两项角色来自用户当前说明，SSH无密码认证被拒，未核验当前代码。
- 工作台路由 /Users/qianlan/Downloads/新CRM/release-control/workstation.json → 01a0f85a-9de6-7701-a12a-9c774080389b。已读工作台最近10轮、AGENTS.md及 domestic-main-release.md；工作台历史尚有更早页，不声称全部历史读完。
- 10月7日数仓扫描记录存在 user-context、user-source-records、sync-runs、copy/generate 路径，但只有 source_inventory_only 证据，缺请求/响应、身份归属和当前部署身份。不能把这些路径直接假定为新的CRM生产契约。
- 参考：[Microsoft官方销售邮件辅助](https://learn.microsoft.com/en-us/microsoft-sales-copilot/use-copilot-kickstart-email-messages)，CRM证据驱动的候选需人工核对/编辑后发送。

## 合同与边界

浏览器仅调用同源 GET /api/sidebar/v2/ai-operations 与 POST /api/sidebar/v2/ai-operations/suggestions。后端从既有 scoped context token 得到企业、员工及 canonical customer.id，同时重验当前HttpOnly员工会话并要求主体一致，禁止浏览器指定客户/员工/正文。数仓消费 Port 返回同一客户/员工范围的清洗摘要、双向私聊（最多40条）、最后客户消息、同步水位及源版本。响应必须与可信主体一致。未知同步时间显示未知，read_at 不能冒充同步时间；非文本末句明确不可理解。

生成使用签名快照（客户、企业、员工、内容摘要、有效期），显式确认、刷新后失效。生成前/后重新读取数仓并比较同一快照，新增/迟到消息或摘要变化返回冲突。调用远端生成器最多45秒；远端必须按冻结快照幂等，不能由CRM新建队列或重试调度器。超时标记结果未确认；不自动重发。返回只供复制，不调用企微发送。员工或客户变化、刷新、离开页面、过期均清除确认和旧话术。

数仓读取结束后必须重新验证当前员工身份，再返回摘要或进入模型；读取期间身份撤销/变更立即拒绝。请求取消或读取期限已过时，即使Adapter返回成功，也不接受其结果。两项拒绝行为由TestAIOperationsViewerChangeDuringWarehouseRead与TestAIOperationsCancelledWarehouseReadIsRejected覆盖，不新增权限或业务限制。

OneID涉及：复用既有客户context解析，数仓返回身份不匹配拒绝，不自建外部ID映射。持久化：本候选无新表/迁移/持久任务；模型调用是远端读取/生成候选，不是企微写入。远端幂等属于已有Agent迁移合同。无新增业务限制，仅落实用户要求的核对与隔离。

火山数据库仅私网。腾讯CRM通过55受保护服务接口或既有已授权网络路径读取，不公网直连私网数据库；火山测试CRM私网验收与腾讯生产分开。生产数据绑定必须在实际数仓合同核验后于Composition Root装配；缺绑定时界面明确未接通，不展示虚构摘要/话术、不回退到客户单方20句。隔离测试可装配合成Reader/Generator。真实读写链路未验收不称完成。

## 五项影响与验收

对外：新增侧边栏同源接口与冲突/未接通/未知结果状态。业务机制：只读数仓+人工生成，绑定可信OneID/员工，无资金/企微效果。关联：sidebar、webshell、Composition Root；不更改自动化原生成上下文、消息存档、订单或发票。页面：AI运营两个二级入口及复制；其他模块保持既有行为。验证：Go鉴权/签名/范围/水位/超时合同，JS异步竞态和确认清除，真实浏览器合成旅程；真实数仓/模型/企微接受与远程迁移仍单列未验证。

回退：工作台沿原锁/队列用上一完整应用构件回退，旧timeline数据保留；不需要数据库逆向迁移。发布由用户指定既有工作台处理，开发线程不安装预发/生产、不切DNS、不迁CRM。候选交接包含base/head/tree/ref、短测试清单与未验证项。
