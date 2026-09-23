# CRM 核心运营第一批发布

业务：五个核心产品绑定现有人群包；客户在目标包之间只有一个当前归属，普通包不受影响。问卷触发和手动批量推荐复用模型配置；试运行不入包，已有归属不被自动覆盖；人工转包、标记购买后重评、历史记录与监督节点推送计数一并交付。

用户已批准两批发布：本批只发布 CRM 自有能力；黄小璨 R1/R2/R3 后续单独适配和真实链路验收，不阻塞本批。访问数返回 null/not_connected，页面显示“未接入”。本批不注册黄小璨同步任务、不提供日课同步按钮。

分类：OneID reads canonical customer，复用 customers.id 和 Identity Port。Persistence：PostgreSQL 同一 UOW、内部 jobqueue、模型 Provider 外部效果。Segment AI 复用既有 External Effects 接受、执行与完成事务；企微写仍归 outbound。不新增身份匹配器、任务或重试内核。

GitHub 参考：此前已查阅 Dify prompts.py（提示词与结构化输出）和 Mautic contact_monitoring.md（统一采集与活动明细），仅参考设计，不引入运行依赖。

实现：新增 0183 Segment 核心运营表与 0184 AI 效果种类迁移。提示词和产品配置按版本冻结，模型结果严格校验；并发 CAS/唯一索引保证一个当前归属。监督节点按 source+push_id+customer_id 去重，多个素材计一次，状态按版本更新，原包历史保留。前端扩展现有管理员页面和成员明细，不新建产品包系统。

接入接口：`/api/admin/ai-audience/core/*`；机器接口 `/open/v1/audience/core-products`、`/open/v1/audience/packages/{package_id}/members`、成员 `operations/history`、`POST /open/v1/audience/push-records`。复用管理员 CSRF 和机器 scope/OwnerScope。

验证：fast → compile → PostgreSQL 领域专项与 Chromium Host 旅程 → PR 完整 Linux CI → 合并后发布 → 精确 release_sha 与认证页面回读。局部验证不称完整 CI。迁移只新增数据结构；回滚应用前需确认已有 core_ai_product 包的兼容性，不删除业务数据。
