# Audience inbound Webhook contract correction

## 业务判断

人群包的外部事实接收方必须调用已经部署在 Composition Root 的 canonical 路径：

```text
POST /api/integrations/ai-audience/{package_key}/membership-facts
```

候选 OpenAPI 误把它描述为 `/api/ai/audience/packages/{package_key}/webhook`。该旧路径没有 runtime mount，继续公开会让调用方获得 404，并错误地把一条不存在的 HTTP 合同当成可验收能力。本修复只把文档合同对齐真实 handler；不增加 alias，避免绕过既有签名边界或扩大兼容面。

调用方提交严格 JSON `kind`、`scope`、`value`、`source`；必须同时给出 `X-AICRM-Timestamp`、`X-AICRM-Event-Id` 和 `X-AICRM-Signature`。签名为小写十六进制 HMAC-SHA256，输入是精确的 UTF-8 字节：`timestamp + newline + event_id + newline + package_key + newline + raw_body`，其中每个 `newline` 都是一个 LF 字节（`0x0A`）。handler 在解析事实前验证签名、五分钟时间窗、package key 和 32 KiB body 上限。成功返回 202 receipt；重放及冲突由既有 receipt 语义处理。

## 架构分类

```text
OneID: resolves identity — segmentadapter 仅在 HMAC 验证和严格 JSON 解析后构造 VerifiedFact，WebhookService 经既有 identity AudienceVerifiedResolver 解析；本修复不 provision、link 或 merge。
Persistence: local transaction / existing internal durable refresh — 现有 WebhookService 在既有 Unit of Work 中记录 receipt，并仅沿用既有 refresh 接受路径；本修复不改表、迁移或事务。
External Effects: not involved — 这是外部入站事实，不发起 Provider 读取或写入，也不创建 queue、worker 或 retry kernel。
```

## GitHub 参考

采用 [OpenAPI Specification 3.1.1](https://github.com/OAI/OpenAPI-Specification/blob/main/versions/3.1.1.md) 的 path template、operation security requirement 与 parameter/reference 表达方式：路径参数、请求体和认证 header 必须共同属于同一个 operation。未采用任何外部 webhook runtime，因为本仓已有签名 verifier、OneID Port 和 replay receipt 合同。

## 最小变更与验收

1. 将 canonical `api/openapi.yaml` 的旧路径替换为真实路径，保留现有 operation ID，显式记录三项 header、严格 body、202 receipt 与 handler 实际可见的失败状态。
2. 按仓库 source-view 合同 materialize `internal/config/http/openapi.yaml`，保持它与 canonical source 字节一致；不提交其忽略的生成视图。
3. 新增 Go contract test，解析 OpenAPI 并断言路径、参数、签名 security scheme、body/receipt schema 与 runtime handler 的 package-key/body 限制对齐，且旧路径不存在。
4. 运行 OpenAPI validation、segment HTTP/OpenAPI contract tests 和 Go type compilation。无真实 Provider、数据库或部署操作。
