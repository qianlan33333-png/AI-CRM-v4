# 自动化运营：已有能力的生产可用收口

基础 PRD：docs/prd/2026-09-05-v3-parity/05-automation.md；供体 automation、extensions/ai/automation_agents、ops_enrollment 人工群发命令。V3 internal/automation、segment、outbound 与 AI port。
分类：读取 canonical 客户与受众，OneID 只沿已有 Port；预览/确认/审计/AI 待审计划同 UoW，自动触发及 Provider 写复用 jobqueue/outbound/External Effects。
已完成：人群、话术、发送人、策略、进入事件、人工预览确认生成 AI 待审计划。待接通：生产 mode 默认 disabled，未配置运行策略可能导致“界面保存但不运行”。
用户流程：建/选人群→条件与话术/素材/发送人→预览真实选中及剔除原因→人工群发进入 AI 待审核或按旧已批准自动触发规则执行→逐项结果/失败恢复。不得把人工确认直接变成自动发送；不得擅自改旧策略/素材或启用存量任务。
开发者核实际冻结合同及部署候选，补缺少的配置读取、界面提示、运行装配与测试；能通过现有配置解决的只交准确启用清单。安全验证须覆盖版本变化、重复 entered、时间窗、上限、运行中停用、结果未知、人工审批零提前效果。与配置板块统一运行上限/模式的版本与消费语义。

## 固定约束与验收
旧供体 AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f，只读检出 /private/tmp/aicrm-release-fallback-donors/sidebar；V3 起点 cc4bb8c7e32d4d7d89f6a9a0b0a928bb562584bc，实施前刷新 origin/main。先读 skills/aicrm-v3-development/SKILL.md。实现者在本 PRD 附录逐字段/命令列出原样复用、Go 等价、V3 已有和待补齐的来源证据，之后再编码。前端照搬供体结构、样式、操作顺序，允许 JS 转 TS，仅在 Host/Adapter 做接口适配；遵循已上线单源供体清单机制，不整目录复制。
每板块完整 PR：实际后端/页面/运行装配、必要迁移、架构/编译/专项、真实 PostgreSQL 事务并发恢复、Chromium 操作、协议测试及完整 CI。先修具体失败再跑全量，不削弱冻结或业务断言。历史数据迁移不在本轮。部署只发布审核通过的准确 HEAD，核 API/Worker 版本、dist 和配置实际生效，保留回滚路径。生产网络配置及凭据只经受保护文件；日志、代码、文档不含秘密或 PII。真实发送/支付需有明确业务目标与内容，不为验收擅自发送。
不恢复旧 56 条机器接口，不新建 OneID 匹配、队列、Worker/重试内核，不顺带开发 Campaign、客服权限或客户个人免打扰。发现本板块旧真实流程依赖未覆盖项，报告具体证据，不伪造成功。
