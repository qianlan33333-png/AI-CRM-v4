# AI 计划提交调用端接通
CRM端已经启用AI派发及ai-review-production OAuth2调用方（凭据仅生产/etc/aicrm/integrations/ai-review-production.json）。调用端按用户实际AI/Agent服务定位，采用V1 REST POST /open/v1/ai/review-plans 或MCP create_ai_review_plan；六项目录不扩。只创建pending review，客户用OneID，原稳定幂等键与每人内容保留，机器无审批/发送权。接通实际token获取/刷新与错误回读，保管凭据、最小授权，测试不可将正文/Token写日志。须核实际部署/调用端请求抵达并保留0提前发送；未知调用端位置时向用户问具体服务或配置路径，不假称已接好。用户真实内容和人工发送验收另行。


## 2026-09-08 生产协议核对

在 V3 服务器内使用现有受保护调用方配置完成 OAuth2 client_credentials 取令牌，HTTP 200；REST `GET /open/v1/capabilities` 返回 200；MCP `tools/list` 返回 200、六个工具且无协议错误。请求不跟随重定向，验证输出仅保留状态和数量，不记录令牌、凭据或客户内容。本次创建业务计划 0、发送 0。

这证明 CRM 的机器调用入口与现有凭据可用；尚未证明用户实际 AI 服务已经配置这些入口。实际 AI 服务/Runner 执行机器和目录仍待明确，不能以服务器内协议检查替代上游接通或真实计划验收。
