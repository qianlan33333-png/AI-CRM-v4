# AI-CRM-v3 外部只读 v1 接入指南

代码基线：`origin/main` 已包含 PR #264，提交为
`7f448ccaa0c0a073b3702ab3af07cc99a5c145a9`。本文维护的是该提交的外部读取合同；
其中“真实 PostgreSQL/HTTP 已验证”的表述是原维护方测试报告，不等同于当前环境的
重新执行结果。生产仍是旧版本，本次新增/变更的 `/open/v1` 能力尚未部署到生产。
代码、CI、部署、有效 Client、Token、真实 HTTP 读取和工作台同步是独立验收项。

生产覆盖快照见：[2026-09-13 脱敏覆盖报告](external-read-v1-coverage-2026-09-13.md)。
它只记录聚合数量，不包含手机号、UnionID、客户 ID、订单号或其他客户字段。

## 1. 先看结论

这套接口的稳定主键是 v3 OneID 的 `customer_id`。外部身份只能用显式的
`kind + scope + value` 解析；UnionID 没有正确开放平台 scope 时不能跨渠道匹配。
解析不会建客、绑定或合并身份。订单、问卷、Chat 和 Radar 点击先解析到
canonical `customer_id`，再调用各自 Owner Port；Radar 链接是独立内容实体，不能
硬绑客户。

当前工作树已经有订单列表/详情、身份读取、问卷提交、客户详情、Chat 和 Radar
click/link 的 Catalog/HTTP/MCP（MCP 复用同一动态 Catalog）入口，以及共用的
`Available/Invoke`。`api/openapi.yaml` 当前也包含这七项能力的路径和响应 schema。
订单嵌套 DTO 已显式映射为 snake_case/string，客户详情已组合真实 WeCom
Owner/remark/follow 投影，Chat 已组合本地 archive 与 staff projection。Terra 的
真实 PostgreSQL + HTTP journey 已验证这些路径；工作台回读、合并和部署仍是独立
门槛。下表是当前代码事实，不是目标功能承诺。

| 能力 | Operation | 当前 REST | 当前 MCP | 当前 capability/scope | 工作树状态 |
|---|---|---|---|---|---|
| 能力目录 | `platform.capabilities.list` | `GET /open/v1/capabilities` | `list_capabilities` | `platform.capabilities.read` + `read` | 已接线，返回本次调用者可用项 |
| 身份解析 | `customer.resolve` | `POST /open/v1/customers:resolve` | `resolve_customer` | `customer.resolve` + `read` | 已接线；显式 kind/scope/value |
| 客户安全摘要 | `customer.context.get` | `GET /open/v1/customers/{customer_id}` | `get_customer_context` | `customer.read` + `read` | 已接线；当前是 Sidebar 安全投影 |
| 客户活动摘要 | `customer.activities.list` | `GET /open/v1/customers/{customer_id}/activities` | `list_customer_activities` | `customer.activity.read` + `read` | 已接线；支持 message/survey/radar/order，默认最多 50 条（最大 100） |
| 订单列表 | `order.list` | `GET /open/v1/orders` | `list_orders` | `order.read` + `read` | 已接线；真实 PG/HTTP journey 已验证 101 lookahead/source pair |
| 订单详情 | `order.get` | `GET /open/v1/orders/{order_id}` | `get_order` | `order.read` + `read` | 已接线；真实 HTTP 已断言 items/refund_records/timeline 的 snake_case/string |
| 身份事实 | `identity.get` | `GET /open/v1/customers/{customer_id}/identities` | `get_customer_identities` | `identity.read` + `read` | 已接线；真实 PG/HTTP 已验证 canonical、scope、declared/verified、多值、wrong scope 和非 CN phone 保护 |
| 问卷提交 | `questionnaire.submissions.list` | `GET /open/v1/questionnaire-submissions` | `list_questionnaire_submissions` | `questionnaire.read` + `read` | 已接线；签名 keyset，真实 PG/HTTP journey 已验证 |
| 客户详情 | `customer.detail.get` | `GET /open/v1/customers/{customer_id}/detail` | `get_customer_detail` | `customer.detail.read` + `read` | 已接线；真实 PG/HTTP 已验证 profile/Owner/follow/remark |
| Chat 明细 | `chat.records.list` | `GET /open/v1/chat-records` | `list_chat_records` | `chat.read` + `read` | Catalog/HTTP/MCP/Access/OpenAPI 已接线；真实 PG/HTTP journey 已验证 archive/staff/media 语义 |
| Radar 点击 | `radar.clicks.list` | `GET /open/v1/radar/clicks` | `list_radar_clicks` | `radar.click.read` + `read` | Catalog/HTTP/MCP/Access/OpenAPI 已接线；真实 PG/HTTP journey 已验证 |
| Radar 链接 | `radar.links.list` | `GET /open/v1/radar/links` | `list_radar_links` | `radar.link.read` + `read` | Catalog/HTTP/MCP/Access/OpenAPI 已接线；真实 PG/HTTP journey 已验证 |
| 现有 AI 工作流 | `ai.review_plan.create` | `POST /open/v1/ai/review-plans` | `create_ai_review_plan` | `ai.review_plan.create` + `write` | 现有授权能力；不属于本只读 Client |
| 操作状态 | `operation.get` | `GET /open/v1/operations/{operation_id}` | `get_operation_status` | `operation.read` + `read` | 已接线，主要服务已有 AI 写工作流 |

表中没有 REST/MCP 路径的行在四层接线前不可用；已有路径也要经过真实 PG、真实
HTTP、工作台回读和部署验收，不能只看函数或 Mock。不能因为旧 `/api/external`
适配器仍存在于源代码，就把它算作 v1 能力。

## 2. 认证和专用 Client

### 2.1 机器调用

业务调用使用 TLS 下的短期 Bearer Token：

```http
Authorization: Bearer <short-lived-access-token>
```

Token 使用 OAuth `client_credentials` 换取，固定 audience 为
`external_integration`，调用者只能请求已经授予该 Client 的 scope。服务端每次
业务请求都会重新读取 Client 当前状态和 grant；停用、轮换或 grant 变化会使旧
Token 失效或失去相应能力。服务端不签发 refresh token，过期后重新换取。

Token 请求可以使用 HTTP Basic，也可以使用表单中的 `client_id` 和
`client_secret`。建议使用 Basic，Secret 只从 Secret Store 注入环境变量：

```bash
export AICRM_BASE_URL='https://<deployed-aicrm-host>'
export AICRM_CLIENT_ID='<dedicated-client-id>'
export AICRM_CLIENT_SECRET='<dedicated-client-secret>'

TOKEN_RESPONSE="$(curl --fail-with-body --silent --show-error \
  --user "$AICRM_CLIENT_ID:$AICRM_CLIENT_SECRET" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode 'audience=external_integration' \
  --data-urlencode 'scope=read' \
  "$AICRM_BASE_URL/oauth/token")"
export AICRM_ACCESS_TOKEN="$(jq -er '.access_token' <<<"$TOKEN_RESPONSE")"
jq '{token_type,expires_in,scope}' <<<"$TOKEN_RESPONSE"
```

不要在命令、日志、URL、提交或聊天中写入真实 Secret 或完整 Token。本文只使用
占位符，也不使用旧凭据。

### 2.2 创建、激活和自检

这是当前 v1 管理路径；需要已认证的 super-admin session、CSRF cookie 和
`X-CSRF-Token`。它不是机器 Client 的调用路径：

```bash
curl --fail-with-body --silent --show-error \
  -X POST "$AICRM_BASE_URL/api/admin/open-platform/clients" \
  -H 'Content-Type: application/json' \
  -H 'X-CSRF-Token: <admin-csrf-token>' \
  -H 'Cookie: <admin-session-cookie>; <admin-csrf-cookie>' \
  --data '{
    "client_id":"<dedicated-client-id>",
    "display_name":"External AI read-only",
    "purpose":"external_agent",
    "audiences":["external_integration"],
    "scopes":["read"],
    "capabilities":[
      "platform.capabilities.read",
      "customer.resolve",
      "customer.read",
      "order.read",
      "identity.read",
      "questionnaire.read",
      "customer.detail.read",
      "radar.click.read",
      "radar.link.read",
      "chat.read"
    ],
    "allowed_cidrs":["<workbench-egress-cidr>"],
    "token_ttl_seconds":1800,
    "owner_scope":{"corp_id":["<actual-corp-id>"]}
  }'
```

将 `<workbench-egress-cidr>` 和 `<actual-corp-id>` 替换为真实值；若只给一个客户，
使用非空的 `"owner_scope":{"customer_id":["<canonical-customer-id>"]}`。本轮不
新增 `scope_mode` 或另一套权限引擎：`CreateV1` 的实际入口是
`POST /api/admin/open-platform/clients`，它接受 `purpose=external_agent`、
`audiences=["external_integration"]`、已登记 V1 capability 的子集和 `read`/
`write` scope。服务端当前不会替调用者强制只读或非空 owner scope，因此运行手册
必须固定使用上面的精确 read-only capability 集合、`scopes:["read"]` 和非空
`owner_scope`，并在自检中确认结果；不能把 `CreateV1` 的宽松校验写成 profile 已
自动生效。上面十项 capability 是本轮专用只读 profile 的精确集合；已有的
`customer.activity.read` 属于既有客户活动摘要能力，不在本轮专用 profile 内，需
另行授予和验收。
已有 AI Client 的 `ai.review_plan.create` write grant 保持原业务兼容，本项目不
全局删除或升级它。

如果要跨企业全量读取，`corp_id` 必须是当前 Access 配置的实际企业 ID；这只是本
企业范围约束，不等于空 map。空 `owner_scope` 在当前代码中表示 unrestricted，
专用 Client 禁止省略或传空对象。创建后管理员应读取 Client 摘要和审计记录，确认
scope、capability、CIDR、corp/customer owner scope 均为预期值。

创建成功会返回一次性 `secret`。仅在安全通道交付并立即注入工作台；管理页面和
后续 Client 查询只显示 credential hint。新 `external_agent` 默认停用，必须先
通过自检再激活：

```bash
curl --fail-with-body --silent --show-error \
  -X POST "$AICRM_BASE_URL/api/admin/open-platform/clients/<dedicated-client-id>/activate" \
  -H 'Content-Type: application/json' \
  -H 'X-CSRF-Token: <admin-csrf-token>' \
  -H 'Cookie: <admin-session-cookie>; <admin-csrf-cookie>' \
  --data '{"client_secret":"<one-time-secret>","copied_confirmed":true}'
```

激活后使用 Token 自检实际 grant，而不是读取管理员 routes：

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  "$AICRM_BASE_URL/open/v1/capabilities"
```

`/api/admin/open-platform/routes` 是管理员诊断面，返回静态完整 Catalog，不能
当作该 Client 的有效能力。只有 `/open/v1/capabilities` 或 MCP `tools/list`
才代表当前 Token 实际可调用项。

### 2.3 不可用的旧路径和模板

`legacyClientTemplates` 中的 `external_api` 仍然包含 `read/write`、
`external_read/external_write` 和 `/api/external`；它是遗留兼容数据，不能作为
本项目教程或 grant 来源。旧 `/api/external/*` 路径在 v1 挂载层不提供新合同，
不得使用旧 Secret、旧 direct API key、旧固定 Token 或旧 `person_id`。当前文档
也不指导 MCP 遗留模板，因为它同样包含超出本次只读范围的能力。

## 3. OneID → 业务查询的统一流程

### 3.1 先解析身份

请求体必须明确写出 `kind`、`scope` 和 `value`，不能让服务端从字符串猜类型。
本企业生产快照中的 UnionID scope 是
`wechat-open-platform:wx0ca836834b18e989`；这是开放平台范围标识，不是 Secret 或
个人 UnionID。接入配置和请求必须以实际已授权 scope 为准：

```bash
curl --fail-with-body --silent --show-error \
  -X POST "$AICRM_BASE_URL/open/v1/customers:resolve" \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "references":[
      {"kind":"unionid","scope":"wechat-open-platform:wx0ca836834b18e989","value":"<unionid>"}
    ]
  }'
```

响应数据当前为：

```json
{
  "customer_id": 123,
  "identity_id": 456,
  "status": "found"
}
```

`status` 由 OneID Resolver 给出。`found` 才能进入业务查询；无匹配、pending、
conflict 或 scope 不足必须保持相应状态。Resolver 只解析，不隐式创建 Customer、
写入身份、替换已有绑定或自动合并 Customer。

手机号也必须走同一流程，且 scope 不能省略。生产当前的事实是
`phone:cn11` + `declared`，不是 `phone:e164` + `verified`；客户端必须保留事实
原值、scope、assurance、source 和 status，不能依据字段名硬编码 assurance。

### 3.2 用 canonical customer_id 查身份和业务事实

身份往返使用：

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  "$AICRM_BASE_URL/open/v1/customers/123/identities"
```

当前实现的响应形状为：

```json
{
  "customer_id":"123",
  "canonical_customer_id":"123",
  "status":"found",
  "identities":[
    {
      "kind":"phone",
      "scope":"phone:cn11",
      "value":"<authorized-phone>",
      "assurance":"declared",
      "source":"<identity-owner-source>",
      "status":"active"
    }
  ]
}
```

默认只返回 phone facts；UnionID 必须通过显式重复查询参数请求 scope：

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  "$AICRM_BASE_URL/open/v1/customers/123/identities?unionid_scope=wechat-open-platform%3Awx0ca836834b18e989"
```

调用者不得把未请求 UnionID 的空结果理解为“该客户没有 UnionID”，也不得在有
多个 UnionID 时任取一个。当前实现返回全部获授权 fact，并保留明确的
`conflict`/多值状态；不能误标为唯一身份。请求 customer A 但 canonical lineage
落到 B 时，敏感读取前会再次检查 B 的 owner scope，不能只检查输入 A。

身份响应中的 `value` 是敏感字段，只能在专用 Client 的 `identity.read` grant 下
出现。当前 `customer.context.get` 仍是安全摘要，其 `phone_masked` 不等于 raw
phone，也不自动授予 UnionID。V1 没有额外的字段级 profile 引擎：`identity.get` 的
原始 fact 由 `identity.read` 保护，`customer.detail.get` 的备注/Owner/follow 由
`customer.detail.read` 保护；未登记的身份 kind、scope 或详情 Owner 字段必须返回
明确的 missing/unavailable/permission 语义。

时间合同固定为：查询参数使用秒级 Unix 时间戳，响应时间使用 RFC3339/ISO8601 UTC
（通常带 `Z`），保留时点精度供跨系统同步。工作台统一显示 Asia/Shanghai 的
`YYYY-MM-DD HH:mm:ss`（不带时区），只是展示层转换，不能把上海本地时间当作 API
的另一种存储或筛选格式。

### 3.3 业务查询往返

拿到 canonical ID 后，调用者应使用同一个 ID 查询业务 Owner：

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  "$AICRM_BASE_URL/open/v1/orders?customer_id=123&limit=100"
```

订单、问卷、Chat、Radar 点击的每个记录必须保留 `source_system` 和
`source_record_id`，供工作台稳定回查原始记录；这两个字段不能被一个易变的
展示名替代。客户端可以使用身份→customer→业务记录→身份的 round-trip 检查：
响应中的 `customer_id`、手机的真实 scope/assurance 和 UnionID scope 必须与首次
解析一致。历史记录没有可信身份时保持 `unresolved`、`pending` 或 `conflict`，
不能用空数组掩盖缺失，也不能在读请求中补绑定。

Radar 链接只按 link ID/code 和授权范围读取。它是内容实体，不接受 customer
选择器，不返回客户身份，也不能由“有人点击了该链接”推断客户归属。

## 4. 当前已接线 API 的请求和字段

### 4.1 `platform.capabilities.list`

`GET /open/v1/capabilities` 无请求体，返回：

```json
{
  "schema_version":"v1",
  "operations":[
    {
      "operation_id":"order.list",
      "rest_method":"GET",
      "rest_path":"/open/v1/orders",
      "mcp_tool":"list_orders",
      "capability":"order.read",
      "required_scope":"read",
      "schema_version":"v1"
    }
  ]
}
```

`operations` 已按当前 principal 的 capability 和 token scope 过滤，未授权操作
不会出现在这里。

### 4.2 `customer.context.get`

`GET /open/v1/customers/{customer_id}` 的当前安全摘要字段来自 Customer Owner
`SidebarProfile`：

`customer_id`、`display_name`、可选 `avatar_url`、`phone_masked`、
`phone_assurance`、`status`、`activation_status`、`gender`、`contact_type`、
`corp_name`、`source`、`profile_source`、`profile_version`、`industry`、
`industry_description`、`needs_blockers_followup`、`version`、`last_synced_at`、
`updated_at`。

它不包含 raw phone、UnionID、OpenID、WeCom external_userid 或内部身份表字段。
客户详情 raw identity 不是当前已接线的 `customer.read` 摘要能力。

### 4.3 `customer.activities.list`

`GET /open/v1/customers/{customer_id}/activities` 支持 `types`（重复 query 参数
或 MCP 数组，值为 `message`、`survey`、`radar`、`order`）、`cursor` 和 `limit`
（1–100，默认 50；这是既有活动摘要能力的兼容默认值）。响应数据当前为：

```json
{
  "customer_id":123,
  "types":["order","survey"],
  "items":[
    {"activity_id":"order:7","type":"order","occurred_at":"2026-09-13T03:00:00Z","source":"order","payload":{} }
  ],
  "as_of":"2026-09-13T03:05:00Z",
  "next_cursor":"<opaque-cursor>"
}
```

`payload` 是各 Owner 的安全摘要，不能当作订单详情、问卷答案、Chat 原文或
Radar 身份明细。该聚合流的游标绑定 customer、types、grant 和水位，并为每个
Owner 保留位置；某 Owner 失败时不能推进整体游标。

### 4.4 `order.list`

请求为 GET query 或 MCP 同名参数。当前接受：

| 参数 | 类型/约束 |
|---|---|
| `provider` | `wechat_pay`、`wechat_shop`、`alipay` |
| `product_code` | 字符串精确筛选 |
| `merchant_order_no` | 字符串精确筛选 |
| `provider_transaction_no` | 字符串精确筛选 |
| `source_system` + `source_record_id` | 必须成对出现；按原始来源精确筛选 |
| `customer_id` | 正整数；筛选匹配 payer 或 beneficiary 的 canonical ID |
| `created_from/to` | 秒级 Unix 时间戳；范围含边界 |
| `paid_from/to` | 秒级 Unix 时间戳；只看可信 paid event |
| `is_paid` | Boolean |
| `is_refunded` | Boolean；只由已完成退款金额筛选 |
| `limit` | 1–100，默认 100 |
| `cursor` | 不透明字符串，最多 4096 字节 |

REST 会拒绝 GET body、未知参数、重复 scalar 参数、带空格的数值和非法范围。
`customer_id`、订单 ID、时间戳不能带小数；`source_system` 与
`source_record_id` 必须一起提供。当前代码已在 PostgreSQL Unit of Work 中包住
订单、退款和时间线读，Terra 的真实 PG/HTTP 自测已覆盖 101 条 lookahead、来源
筛选和 payer/beneficiary 投影；最终交付仍需按发布门槛重跑总回归。

REST 和 MCP 共用同一输入结构；MCP `list_orders` 的 `is_refunded`、source pair 和
详情嵌套字段必须以当前 `tools/list`/OpenAPI 生成结果为准。HTTP/MCP 都不得把
Owner struct 的默认 Go 字段名当作外部合同。

成功数据为 `items` 数组；有下一页时有 `next_cursor`，没有 `has_more`：

```json
{
  "items":[
    {
      "order_id":"7",
      "payer_customer_id":"123",
      "beneficiary_customer_id":null,
      "customer_id":"123",
      "identity_status":"linked",
      "provider":"wechat_pay",
      "source_system":"wechat_pay",
      "source_record_id":"<source-record-id>",
      "merchant_order_no":"<merchant-order-no>",
      "provider_transaction_no":"<provider-transaction-no>",
      "product_codes":["<product-code>"],
      "created_at":"2026-09-13T03:00:00Z",
      "paid_at":"2026-09-13T03:01:00Z",
      "paid_at_status":"verified",
      "status":"paid",
      "amount_minor":19900,
      "amount_yuan":"199.00",
      "currency":"CNY",
      "is_paid":true,
      "refund_status":"known",
      "is_refunded":false,
      "has_refund_request":false,
      "refunded_minor":0,
      "refunded_yuan":"0.00",
      "refund_requested_minor":0,
      "refund_processing_minor":0,
      "refund_outcome_unknown_minor":0,
      "refund_final_failed_minor":0
    }
  ],
  "next_cursor":"<opaque-cursor>"
}
```

每项字段规则：

- `order_id`、`payer_customer_id`、`beneficiary_customer_id`、`customer_id` 当前
  按字符串输出，`null` 表示该关系没有值；`source_system` 和
  `source_record_id` 必须原样保留来源事实。
- `payer_customer_id` 与 `beneficiary_customer_id` 是不同业务关系。当前冻结规则
  是 payer 优先作为 `customer_id`；只有 beneficiary 时 `customer_id=null`，不能
  把受益人冒充付款人。按 customer owner scope 查询时，响应只能暴露调用方被授权
  的关系 ID；不能因另一侧命中筛选就泄漏未授权的 counterpart ID。两侧为空必须
  保持 `customer_id=null`、`identity_status=unresolved`。
- `paid_at` 只有 immutable、verified paid event 才出现；历史状态时间不能伪装
  成付款时间。无此证据时 `paid_at=null`、`paid_at_status=unavailable`。
- `amount_minor` 是整数分，`amount_yuan` 是两位小数字符串，不能使用 float
  计算金额；`currency` 当前来自订单金额事实。
- Payment 的本地退款摘要存在时，`refund_status=known` 和
  `refund_amount_status=known`。`is_refunded` 只由已完成退款金额决定。`has_refund_request`、
  `refund_requested_minor`、`refund_processing_minor`、
  `refund_outcome_unknown_minor` 和 `refund_final_failed_minor` 分开表达；退款
  申请、处理中或 outcome unknown 不能单独标成已退款。没有退款摘要时返回
  `refund_status=unavailable` 和 `refund_amount_status=unavailable`。

### 4.5 `order.get`

`GET /open/v1/orders/{order_id}` 或 MCP `{ "order_id": 7 }` 只接受正整数 ID。
当前响应使用列表字段，并追加 `items`、`refund_records` 和 `timeline`：

```json
{
  "order_id":"7",
  "items":[
    {"line_no":1,"product_code":"<product-code>","product_name":"<snapshot>","unit_amount_minor":19900,"quantity":1,"line_amount_minor":19900}
  ],
  "refund_records":[
    {"refund_id":"9","status":"completed","amount_minor":19900,"created_at":"2026-09-13T03:02:00Z","updated_at":"2026-09-13T03:02:00Z"}
  ],
  "timeline":[
    {"status":"paid","refunded_minor":0,"occurred_at":"2026-09-13T03:01:00Z"}
  ]
}
```

`items` 的金额仍是整数分；`refund_records.refund_id` 是字符串；
`refund_records` 只返回 Payment 安全投影；`timeline` 只返回状态、已完成退款分
和时间。Host DTO 当前显式映射为上述 snake_case/string 字段，Terra 的真实 HTTP
自测已断言嵌套 `items`、`refund_records`、`timeline`，不会接受 `LineNo` 或数值
`RefundID`。当前没有支付明细、回调摘要、原始回调 body、签名、密钥或 Provider
私密字段；订单不存在返回 `404`，不在 Client customer/owner scope 内也不能靠猜
ID 绕过授权。

### 4.6 `identity.get`

REST 路径为 `GET /open/v1/customers/{customer_id}/identities`，MCP 参数为：

```json
{"customer_id":123,"unionid_scopes":["<wechat-open-platform-scope>"]}
```

当前可见字段是 `customer_id`、`canonical_customer_id`、`status` 和
`identities[]` 中的 `kind`、`scope`、`value`、`assurance`、`source`、`status`。
默认只返回 phone；传入一个或多个 `unionid_scope` 后才返回对应 scope 的 UnionID
facts。机器 Identity Owner 会沿 canonical lineage 读取 active facts，解密
`phone:cn11` 的存储值并保留实际 `scope/assurance/source/status`；生产中的这类行
`normalized_value` 为空、digest 和 ciphertext 存在，所以只用明文 fixture 不能
证明手机号往返。

当前导出状态只有 `found`、`missing`、`conflict`：open identity conflict 返回
`409 conflict`，没有 active fact 返回 `404 not_found`，有 fact 才返回 `found`。
当前事实读取为 active-only；pending/inactive 行不会被伪造为 active，其缺失状态
要在覆盖报告中保留。若客户只有未请求的 UnionID、而请求默认 phone，调用方必须
按请求投影把结果解释为 phone missing，不能把成功空数组当成全量身份。多 UnionID
返回全部获授权 facts，并保留实际 scope/assurance/source/status；不能任取一个。
Terra 的 PG/HTTP 自测已覆盖加密 declared phone、verified e164、多个带 scope
UnionID 和 wrong scope。

### 4.7 `questionnaire.submissions.list`

这是当前已经进入 Catalog、HTTP、MCP 和 `Available/Invoke` 的 Survey Owner 读取。
REST 为 `GET /open/v1/questionnaire-submissions`，MCP tool 为
`list_questionnaire_submissions`，capability 为 `questionnaire.read`，scope 为
`read`。REST 只接受 query，不接受 GET body；MCP 接受同一字段的 JSON object。

| 参数 | 类型/约束 |
|---|---|
| `customer_id` | 必填正整数；必须先由 OneID 得到 canonical customer |
| `questionnaire_id` | 可选非负整数；`0`/省略表示不按问卷筛选 |
| `source_system` + `source_record_id` | 必须成对出现，精确回查原始记录 |
| `submitted_from/to` | 可选、非负 Unix 秒；`to` 在 V1 为排他上界 |
| `limit` | 1–100，默认 100 |
| `cursor` | HMAC opaque 字符串，最多 4096 字节 |

首次请求若未给 `submitted_to`，服务端冻结当前 UTC 时间为排他上界；每页向 Owner
请求 `limit+1`，只返回请求条数。游标绑定 operation、grant、完整筛选、冻结的
`submitted_to` 和 `(submitted_at, submission_id)` 下降位置。

当前 item 字段是：

```json
{
  "submission_id":"31",
  "questionnaire_id":"9",
  "definition_version":4,
  "questionnaire_title":"<snapshot>",
  "submitted_at":"2026-09-13T03:00:00Z",
  "answers":[
    {
      "question_title_snapshot":"<snapshot>",
      "selected_option_texts_snapshot":["<snapshot>"],
      "text_value":"<authorized-snapshot>",
      "score_contribution":1.5
    }
  ],
  "final_tags":["<snapshot>"],
  "assessment_result_snapshot":{},
  "source_system":"<source-system>",
  "source_record_id":"<source-record-id>",
  "customer_id":"123",
  "identity_status":"resolved"
}
```

顶层返回 `customer_id` 和 `items`，有下一页时返回 `next_cursor`。回答、标签和
assessment 都是提交时快照，不在查询时重新评分；`text_value` 属于敏感内容，
只能在已授权的专用 Client 中读取。当前路由只返回能通过 canonical customer 或
已配置 Survey UnionID 历史别名选出的记录，匿名、unresolved 和未映射历史记录
不会被伪装成空回答；它们仍须在覆盖率清单中处理。当前 item 的
`identity_status` 由 V1 组合固定为 `resolved`；pending/conflict/unresolved 历史行
继续保留在覆盖率清单，不能据此宣称 Survey 全量打通。

### 4.8 `customer.detail.get`

REST 为 `GET /open/v1/customers/{customer_id}/detail`，MCP tool 为
`get_customer_detail`，capability 为 `customer.detail.read`，scope 为 `read`。
请求只接受 path 中的正整数 `customer_id`，不接受 query 或 body。当前实际响应
字段为：

`customer_id`、`display_name`、`avatar_url`、`status`、`activation_status`、
`gender`、`contact_type`、`corp_name`、`source`、`industry`、
`industry_description`、`needs_blockers_followup`、`updated_at`、`remark`、
`business_detail_availability`、`owner`、`follow_users`。

其中前一组来自 Sidebar profile；`business_detail_availability` 为 WeCom Owner
投影的 `{status:"available"|"missing", reason?}`。`owner` 是通过
`AudiencePrimaryOwners` 选择出的同一 corp scope 主 Owner（`{user_id}` 或 `null`），
`follow_users` 是已完成同步的 `{user_id}` 数组；`remark` 只有主 Owner 对应的真实
备注存在时返回字符串，否则为 `null`。当前代码已使用组合的
`CustomerBusinessDetailReader` 和 Owner Reader，且有失效/歧义时的 unavailable 或
空值语义；Terra 的真实 PG/HTTP 自测已用非空 fixture 证明 `remark`、`owner` 和
`follow_users` 的组合输出，生产数据回读仍是独立上线门槛。raw phone、
UnionID 等身份值仍走 `identity.get`，不会从 detail 里猜测或补造。

### 4.9 Chat 和 Radar 能力

Chat v1 当前已经进入 Catalog、HTTP、MCP、Access 和 OpenAPI。调用路径为
`GET /open/v1/chat-records`，MCP 工具为 `list_chat_records`，capability 为
`chat.read`，scope 为 `read`。它只读本地 message archive，不调用 Provider，不取
媒体内容或 raw identity。旧 `/api/external/chat-records` adapter 的历史字段包括
`msgid`、`chat_scene`、`chat_type`、`unionid`、`external_userid`、`with_userid`、
`sender`、`receiver`、`chat_id`、`roomid`、`group_name`、`msgtype`、`content`、
`media_id`、`send_time`、`source_id`，并使用旧身份参数和 offset cursor；这些字段
只用于迁移审计，不能按旧 route 接入，也不能把 `external_read` grant 当作 Chat v1
权限。

Chat 请求字段为 `customer_id`（必填）、`chat_type`（`private`/`group`，默认
`private`）、私聊时的 `staff_user_id` 或 `staff_wecom_userid`、
`occurred_from/to`、成对的 `source_system=message_archive` 与 `source_record_id`、
`message_id`、固定 `limit=20` 和 opaque `cursor`。响应 page 字段为
`customer_id`、`items`、可选 `next_cursor`；item 字段为
`message_id`、`customer_id`、`identity_status`、`chat_type`、`message_type`、
`content`、`render_type`、`direction`、`occurred_at`、`conversation_id`、
`group_name`、`media_archive_status`、`media_availability`、`staff[]`、
`source_system`、`source_record_id`。每个 staff 为 `staff_id` 和 `display_name`。
媒体未归档时用 `media_archive_status`/`media_availability` 的 unavailable 语义，
不能转为空消息或实时拉取 Provider。

Chat 代码、archive Store、Host employee directory 和私聊 staff selector 的真实
PG/HTTP 自测已通过；`staff_wecom_userid` 可解析为本地 staff projection，并与
`customer.detail.get` 的 `follow_users` 使用同一已同步员工事实。媒体缺失仍按
unavailable 返回，不能转为空消息或实时拉取 Provider。

可运行的占位调用如下（`staff_user_id` 也可以替换为已同步的
`staff_wecom_userid`）：

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  "$AICRM_BASE_URL/open/v1/chat-records?customer_id=<canonical-customer-id>&chat_type=private&staff_user_id=<staff-id>&limit=20"
```

同一 Token 下的 Radar 调用为：

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  "$AICRM_BASE_URL/open/v1/radar/clicks?customer_id=<canonical-customer-id>&limit=100"

curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $AICRM_ACCESS_TOKEN" \
  "$AICRM_BASE_URL/open/v1/radar/links?radar_code=<radar-code>&limit=100"
```

Radar application layer 和当前 Catalog、HTTP、MCP、Access、OpenAPI 已登记，调用
路径为 `GET /open/v1/radar/clicks`、`GET /open/v1/radar/links`，工具分别为
`list_radar_clicks`、`list_radar_links`，capability 分别为 `radar.click.read`、
`radar.link.read`。Terra 的真实 PG/HTTP 自测已覆盖其响应和 signed cursor；部署前
仍需总回归和工作台回读。以下字段是代码当前形成的合同：

- click：`click_id`、`event_id`、`session_id`、`radar_id`、`radar_code`、
  `clicked_at`、`open_stage`、`source_system`、`source_record_id`、`customer_id`、
  `identity_status`、`attribution_status`。只取一个 Radar session 的首次成功
  opening；`resolved` 有 customer，`pending`/`conflict` 的 customer 为 null；
  anonymous、failed、IP、UA 和 OAuth 中间阶段排除。
- link：`link_id`、`radar_id`、`radar_code`、`title`、`status`、`is_enabled`、
  `source_system`、`source_record_id`。链接查询只接受 link ID/code 和授权范围，
  不接受 `customer_id` 或任何身份选择器，不返回客户字段；draft/disabled 的已
  保留链接仍可按 Owner 规则读取。

Radar click 的目标请求字段是可选 `customer_id`、`radar_id`、`radar_code`、
`session_id`、`clicked_from/to`、`limit`（默认/最大 100）和 opaque `cursor`；
link 的目标请求字段是 `radar_id`、`radar_code`、`limit` 和 opaque `cursor`。
其游标分别绑定 operation、grant、筛选和 opening 位置或 link ID。link 不能接收
`customer_id`，click 才能按 customer scope 筛选。运行时接线和单元测试不能替代
部署和工作台回读。

## 5. 游标、错误和 MCP

### 5.1 游标

订单游标当前是 HMAC 签名的 opaque envelope，绑定 grant digest、筛选 digest、
排序位置（`created_at` + order ID）和版本；客户端只能原样保存和回传，不能解码
或拼接。订单查询默认 100，最大 100，服务端使用 `limit+1` lookahead 决定是否
产生 `next_cursor`，不会返回第 101 条。

游标必须在以下情况失效并返回 `400 validation`：签名被改、筛选/客户改变、
grant 或 owner scope 改变、auth version 变化、版本不兼容或位置非法。撤销后不能
用旧游标继续读取。客户端遇到该错误应从第一页重新开始并记录同步 checkpoint，
不能把失败当作空页。

问卷 v1 route 当前已经接入签名 keyset cursor，绑定 operation、grant、完整筛选、
冻结的 `submitted_to` 和 `(submitted_at, submission_id)` 位置；Owner 内部仍可使用
offset/分页适配，但不得把该内部位置暴露给客户端。Radar click/link v1 route 也已
接入 signed cursor，分别绑定 opening 位置或 link ID；Chat v1 使用签名 cursor，绑定
operation、grant、完整筛选、冻结的 `occurred_to` 和
`(occurred_at, message_id)` 位置。所有最终外部列表都必须绑定 operation、筛选、
effective grant、auth version 和必要水位，不能把内部 offset 当成外部合同。

### 5.2 REST 统一错误

所有已接线 `/open/v1` REST 成功响应使用：

```json
{"data":{},"error":null,"request_id":"<request-id>"}
```

失败响应使用：

```json
{"data":null,"error":{"code":"<category>"},"request_id":"<request-id>"}
```

同时返回 `X-Request-ID`。当前错误类别和 HTTP 状态：

| HTTP | `error.code` | 用途 |
|---:|---|---|
| 400 | `validation` | JSON/query/path/时间/游标不合法 |
| 401 | `authentication` | 没有 Bearer、Token 无效/过期、Client 停用或轮换 |
| 403 | `permission` | scope、capability、CIDR、corp 或 customer owner scope 不足 |
| 404 | `not_found` | 客户或订单等明确资源不存在 |
| 409 | `identity_pending` / `identity_conflict` / `conflict` / `outcome_unknown` | 身份 pending、冲突、多值、业务冲突或结果未知；保留不确定状态，不能转空数组或完成结果 |
| 429 | `rate_limited` | 访问频率受限 |
| 503 | `dependency_unavailable` | Owner Port、Unit of Work 或读模型未就绪 |

### 5.3 MCP

MCP 使用同一 Bearer Token 和同一 Operation Catalog：

```http
POST /mcp
Content-Type: application/json
Authorization: Bearer <short-lived-access-token>
```

支持 JSON-RPC 2.0 的 `initialize`、`tools/list`、`tools/call`。`tools/list` 只列
当前 principal 可用的工具；`tools/call` 的参数必须是严格 JSON object，未知字段
或重复字段会失败。业务错误仍以 HTTP 200 的 JSON-RPC error 返回，错误 code 为
`-32000`，`data.category` 携带上表中的类别。transport/参数错误使用标准
`-32600`/`-32601`/`-32602`。

## 6. 迁移差异

| 主题 | 旧外部接口行为 | v3 v1 合同 |
|---|---|---|
| 路径 | `/api/external/*` | `/open/v1/*`；旧路径不作为新接入路径 |
| 认证 | 旧共享模板/direct key 语义 | 专用 `external_agent` Client + `client_credentials` + 短期 Bearer |
| 权限 | `external_read` 与可能的 write 混在模板 | Operation capability + `read` scope；专用 profile 显式只读 |
| 客户主键 | `person_id`、外部 ID 或隐式 fallback | OneID canonical `customer_id`；不建客、不自动合并 |
| UnionID | 可能按无 scope 值匹配 | 必须携带并校验开放平台 scope；多值保持 conflict/列表语义 |
| 手机 | 旧字段可能没有 assurance/scope | 返回事实的 `kind/scope/value/assurance/source/status`；生产当前是 declared `phone:cn11` |
| 来源 ID | 旧响应可能只给展示字段 | `source_system + source_record_id` 保留且可用于精确回查 |
| 订单金额 | 旧适配可能使用浮点元金额 | `amount_minor` 整数分为事实，`amount_yuan` 仅格式化字符串 |
| 付款时间 | 状态迁移时间易被当 paid time | 只有 verified paid event 才填 `paid_at` |
| 退款 | 申请/处理中/unknown 可能被合并成 refunded | 完成金额与申请、处理中、unknown、final failed 分开；`is_refunded` 只看完成金额 |
| 详情 | 列表和详情可能共享不完整投影 | 当前 v1 已追加商品、退款记录和 timeline，并以 DTO 输出 snake_case/string；完整支付/回调摘要未提供 |
| 分页 | 旧路径/规划使用 offset 或固定 page | 订单、问卷、Radar 和 Chat 使用 signed opaque cursor；旧 offset 不能使用 |
| Provider | 旧接口可能触发同步/fallback | v3 外部读取只读本地 Owner projection，不在查询时调用 Provider |
| Radar | 旧链接/点击可能附带外部身份假设 | Radar link 不属客户；click 必须以可信事件和 OneID 状态表达，不硬绑 |

## 7. 真实数据和上线门槛

生产快照（2026-09-13 11:49 Asia/Shanghai，即 03:49 UTC）显示：927 条订单中，
按 payer 关联 active phone 的为 734、active UnionID 的为 579；这两个数字不是
beneficiary 覆盖，也不能相加。active phone 共 1,557 条，全部为
`phone:cn11` + `declared`，当前没有 verified phone；active verified UnionID
为 1,025，当前快照中的 UnionID scope 为
`wechat-open-platform:wx0ca836834b18e989`。问卷 resolved 1,049、unresolved 513、anonymous 25，legacy projection
1,562；Radar native 技术阶段事件 4 条、legacy 0；archive group 442、private
5,215、legacy projection 0。完整口径和历史 commerce 状态见覆盖报告。

这些数量只能说明生产覆盖，不能说明每条记录都能安全回到同一个 Customer。尤其
不能用“resolved survey 都有 active phone/UnionID”替代 assurance、scope 和
多值审计，也不能因为没有 verified phone 就伪造 verified e164。缺失、匿名、
pending、conflict 和未解析记录必须作为数据事实保留，不通过本只读 API 补数据。

维护方在 PR #264 合入前报告的本地验证覆盖最终 Catalog、HTTP、MCP、source pair、订单
payer/beneficiary 分离、订单嵌套 snake_case/string 映射、101 条 lookahead、
客户详情真实 remark/Owner/follow、Chat staff selector、Radar、身份的
declared/verified/多 UnionID/wrong scope 与非 CN phone 空回退保护，以及专用
10-cap read-only Client 的 PG 200、写入 403 和停用后 401。订单 Host DTO 显式映射
`items`、`refund_records`、`timeline`，因此外部合同继续使用 snake_case 和字符串
`refund_id`；不能根据旧的 CamelCase/数字响应改写本文。OpenAPI 源文件与嵌入副本、
14 项 Catalog、生成客户端和真实 PostgreSQL/HTTP composition 均已通过本地验证。

仍未完成的是发布门槛：代码已合入 `origin/main`，但尚未部署，生产仍运行旧版本；
外部工作台同步和真实业务回读也尚未验收。不能把本地测试、OpenAPI
源文件、生成客户端或有效 Client 自检写成已上线；发布前仍要分别留下迁移/备份、
公网 TLS、凭据注入、工作台同步和真实业务回读证据。

生产覆盖仍有明确边界：没有 verified phone；1,557 条 active phone 是加密存储的
`phone:cn11` + `declared`，1,025 条 active verified UnionID 只属于已知 scope。
本地代码验证不代表 927 条订单、1,587 条问卷或 5,657 条 archive 记录全部有可
回溯身份；pending、conflict、unresolved、匿名和 legacy 缺口必须继续按覆盖报告
处理，不能通过本只读 API 修补数据或补绑客户。生产旧版不应按本文 v1 路径操作，
直到部署和外部工作台回读分别完成。可复现的本地无敏感数据检查见
[external-read-v1-test-guide.md](external-read-v1-test-guide.md)。
