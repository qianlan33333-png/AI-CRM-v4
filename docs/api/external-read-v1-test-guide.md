# 外部只读 API v1 本地测试说明

适用基线：`origin/main` 的 PR #264 提交
`7f448ccaa0c0a073b3702ab3af07cc99a5c145a9`。本文只说明本地合同校验，不能作为
部署、真实授权或生产数据读取完成的证据。

## 安全边界

- 不把 client secret、Bearer Token、Cookie、手机号、OpenID、UnionID、客户 ID、订单号
  或原始响应写入 shell 历史、测试夹具、提交、截图或报告。
- 所有 curl 仅使用 `<placeholder>`；不在本地文档提供在线试调或真实客户样例。
- `customer.resolve` 只解析 OneID；测试不能借此建客、绑定、合并或修补身份。
- 订单、问卷、Chat 与 Radar 是本地 Owner projection 的只读查询，不在读取时调用 Provider。

## 快速本地检查

在仓库根目录执行：

```bash
TZ=Asia/Shanghai node scripts/open-platform-host-e2e.mjs
go test -count=1 -v ./internal/openplatform/http ./internal/openplatform/port
go test -count=1 -v ./cmd/aicrm -run 'TestV1|TestOpenPlatformV1Routing'
```

这些检查分别覆盖浏览器 Host 的静态文档/管理视图、REST 与 MCP 入参合同、以及
组合层的只读 operation。它们通过不等于生产部署、有效 Client、Token 签发或外部工作台
验收已经完成。

## 文档页检查

`/admin/api-docs` 默认是纯说明页：不应请求
`/api/admin/open-platform/clients` 或 `/api/admin/open-platform/routes`。检查：

1. 页面显示 11 个专用只读 operation、OAuth 占位示例、OneID、cursor、退款语义、错误码。
2. “下载已认证 OpenAPI YAML”链接到同源的
   `/api/admin/config/openapi.yaml`；它仍需要现有管理员登录，不经过旧 bridge。
3. `?tab=clients` 才懒加载调用方管理；旧 `?client=<id>` 深链仍经既有外层认证路由进入管理视图。
4. `?tab=docs` 优先显示文档，即使 URL 同时带有 `client`，也不能触发管理请求。
5. 管理页对 401 显示登录入口、对 403 显示超级管理员说明、对 5xx 显示可重试提示；这些
   状态不能覆盖文档页。

## Token 与测试数据先决条件

在隔离环境中创建一个停用的 `external_agent` 调用方，授予下表所列 capability 和
`read` scope，配置非空 owner scope 与测试环境 CIDR。通过一次性密钥确认激活，再以
`client_credentials`、`audience=external_integration`、`scope=read` 换取短期 Token。
Token、secret、Cookie 只从安全注入读取，运行后清除环境变量。

准备最小合成 fixture：一个 canonical customer、其 scoped identity、一个可读订单、
一条已解析问卷提交、一个 customer detail 投影、一条本地 archive Chat、一条 Radar
click 和一个 Radar link。另准备不存在、owner-scope 外、identity conflict/pending 与
依赖不可用的 fixture。测试报告仅记录 fixture 标签和 request_id，不记录真实标识或
响应内容。

## T01–T11 HTTP 合同检查

以下请求均加 `Authorization: Bearer <short-lived-test-token>`，使用
`https://<test-host>` 和占位值。每项成功时都断言 envelope 的 `error=null`、非空
`request_id`，以及表中列出的安全字段；不输出响应 body。

| 用例 | capability | method/path | 必填参数 | 正向断言 |
|---|---|---|---|---|
| T01 capabilities | `platform.capabilities.read` | `GET /open/v1/capabilities` | 无 | `operations` 只包含当前 Token 的已授予项。 |
| T02 resolve | `customer.resolve` | `POST /open/v1/customers:resolve` | JSON `references` 数组 | 返回 canonical `customer_id` 或明确 pending/conflict；不建客。 |
| T03 context | `customer.read` | `GET /open/v1/customers/<customer-id>` | 正整数 path ID | 返回安全摘要，不含 raw phone/外部身份值。 |
| T04 orders | `order.read` | `GET /open/v1/orders` | 无；所有筛选可选 | `items` 中金额使用 `amount_minor`，续页才有 `next_cursor`。 |
| T05 order detail | `order.read` | `GET /open/v1/orders/<order-id>` | 正整数 path ID | 详情保留 `items`、`refund_records`、`timeline`。 |
| T06 identities | `identity.read` | `GET /open/v1/customers/<customer-id>/identities` | 正整数 path ID；UnionID 需 `unionid_scope` | 每个 identity 保留实际 `kind/scope/assurance/source/status`。 |
| T07 submissions | `questionnaire.read` | `GET /open/v1/questionnaire-submissions` | `customer_id` | 返回 `customer_id`、提交 `items` 与可选 `next_cursor`。 |
| T08 detail | `customer.detail.read` | `GET /open/v1/customers/<customer-id>/detail` | 正整数 path ID | 返回安全业务投影及 `owner`、`follow_users`。 |
| T09 chat | `chat.read` | `GET /open/v1/chat-records` | `customer_id`；private 时 staff selector | 只读本地 archive，媒体缺失保留 unavailable 语义。 |
| T10 radar clicks | `radar.click.read` | `GET /open/v1/radar/clicks` | 无；所有筛选可选 | resolved/pending/conflict 保留显式 identity 状态。 |
| T11 radar links | `radar.link.read` | `GET /open/v1/radar/links` | 无；`radar_id`/`radar_code` 可选 | 返回内容链接；传 `customer_id` 必须被拒绝。 |

每个列表用同一筛选复测一次 `next_cursor`；响应没有 `has_more` 时不得自行推断分页。
详情和列表的 ID 使用测试 fixture 占位符，不能复制生产标识。

### 请求参数明细

除 T02 的 JSON body 外，以下字段都放在 query；所有 GET 都不带 body。标为“可选”的
字段省略后使用服务端默认值，不把可选筛选写成前置条件。

| 用例 | 参数口径 |
|---|---|
| T01 | 无 path、query 或 body。 |
| T02 | body 为 `{"references":[{"kind":"<kind>","scope":"<scope>","value":"<value>"}]}`；`references` 必填，数组长度 1–8，每个对象的 `kind`、`scope`、`value` 均为非空字符串。 |
| T03 | path `customer_id`：必填正整数；无 query/body。 |
| T04 | 均可选：`provider=wechat_pay\|wechat_shop\|alipay`，`product_code`、`merchant_order_no`、`provider_transaction_no` 精确字符串，`source_system` 与 `source_record_id` 成对，`customer_id` 正整数，`created_from/to`、`paid_from/to` 秒级 Unix 时间且范围含边界，`is_paid`/`is_refunded` Boolean，`limit` 1–100（默认 100），`cursor` opaque 且最多 4096 字节。 |
| T05 | path `order_id`：必填正整数；无 query/body。 |
| T06 | path `customer_id`：必填正整数；`unionid_scope` 可重复，用于限制可读取的 UnionID 开放平台 scope。 |
| T07 | `customer_id` 必填正整数；`questionnaire_id` 可选正整数（省略不筛选）；`source_system` 与 `source_record_id` 成对；`submitted_from/to` 可选非负 Unix 秒，`to` 为排他上界；`limit` 1–100（默认 100）；`cursor` opaque 且最多 4096 字节。 |
| T08 | path `customer_id`：必填正整数；无 query/body。 |
| T09 | `customer_id` 必填正整数；`chat_type=private\|group`（默认 `private`）；private 时传 `staff_user_id` 或 `staff_wecom_userid`；`occurred_from/to` 为 Unix 秒；`source_system=message_archive` 与 `source_record_id` 成对；`message_id` 精确字符串；`limit` 固定 20；`cursor` opaque 且最多 4096 字节。 |
| T10 | 均可选：`customer_id`、`radar_id`、`radar_code`、`session_id` 精确筛选，`clicked_from/to` Unix 秒，`limit` 1–100（默认/最大 100），`cursor` opaque。 |
| T11 | 均可选：`radar_id`、`radar_code` 精确筛选，`limit` 1–100（默认/最大 100），`cursor` opaque；禁止 `customer_id` 与身份选择器。 |

已知合同差异：当前运行时把 T07 的 `questionnaire_id=0` 视为省略，但 OpenAPI 的
minimum 是 1。严格合同测试应使用省略或正整数；不要把 `0` 写入接入示例或作为
schema 通过依据。本次不修改后台合同。

## 错误矩阵

| HTTP | 触发方法 | 期望 |
|---:|---|---|
| 400 | 非法正整数、未知 query、错误 cursor，或 T11 传 `customer_id` | `error.code=validation`，客户端从第一页重试或修正输入。 |
| 401 | 无 Bearer、过期 Token、停用或轮换后的 Client | `error.code=authentication`，重新换取 Token。 |
| 403 | 去掉一个 capability、read scope、CIDR 或 owner scope 不匹配 | `error.code=permission`，不能改用管理员目录绕过。 |
| 404 | 不存在且在测试允许范围内的 customer/order | `error.code=not_found`，不能据此枚举资源。 |
| 409 | conflict/pending OneID fixture | `identity_pending`、`identity_conflict`、`conflict` 或 `outcome_unknown`，保留不确定状态，不当作已完成结果。 |
| 503 | 注入 Owner Port、Unit of Work 或读模型不可用 | `dependency_unavailable`，保留 request_id 后重试。 |

## 无敏感数据的 HTTP 形状检查

仅在隔离测试环境、已通过安全通道注入测试凭据时，使用以下形状；命令与日志中只能保留
占位符。运行结束后清除环境变量。

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer <short-lived-test-token>" \
  "https://<test-host>/open/v1/capabilities"

curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer <short-lived-test-token>" \
  "https://<test-host>/open/v1/orders?limit=1"
```

验证响应 envelope 使用 `data`、`error`、`request_id`，列表仅在后续页存在时携带
`next_cursor`，没有 `has_more`。把 cursor 原样回传；筛选或授权改变、cursor 失效时从
第一页重新开始。

订单金额检查 `amount_minor` 整数分与 `amount_yuan` 字符串；`is_refunded` 只代表已完成
退款金额。Payment 的本地退款摘要存在时，`refund_status` 和
`refund_amount_status` 均为 `known`；没有摘要时两者均为 `unavailable`。申请、处理中或
outcome unknown 的金额仍分别在对应分项字段中表达，不能仅据此把订单标成已退款。

## PostgreSQL 集成测试

以下测试需要显式配置隔离的 PostgreSQL：

```bash
AICRM_DATABASE_URL='<isolated-test-postgres-url>' \
  go test -count=1 -v ./cmd/aicrm -run 'TestOpenPlatform.*PostgreSQL|TestV1.*PostgreSQL'
```

不设置 `AICRM_DATABASE_URL` 时，
`TestOpenPlatformMachineManagementPostgreSQLJourney` 会明确报告
`AICRM_DATABASE_URL is not configured; skipping Open Platform PostgreSQL journey`。这是
**跳过**，不是集成测试通过。macOS 上 Chromium journey 也会明确跳过；Linux CI 且设置
`AICRM_REQUIRE_CHROMIUM_JOURNEY=1` 才运行该浏览器门槛。

## 结果记录

记录提交 SHA、命令、通过/失败/跳过、运行时间和非敏感 request_id。不要记录响应载荷、
用户标识、凭据或令牌。将本地单测、PostgreSQL、部署、有效 Client/Token、真实 HTTP
读取和外部工作台业务回读分别报告。
