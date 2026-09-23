# PRD：内容雷达“谁打开”访客明细

## 业务判断

内容雷达详情当前把管理者可见的记录降为 `receipt_id`、事件阶段和时间。它不能回答“是谁打开”，而且当前公开事件投影的注释明确禁止携带原始外部联系人标识。管理员需要在雷达自身的真实打开会话内查看：客户昵称、同企业唯一且已验证的外部联系人 ID、标准 OneID 和打开时间；筛选和 CSV 必须使用同一服务器端结果集。

本 PR 只增加受限管理员的**只读**访客明细。它不修改公开访问、OAuth、OneID 解析、session token、事件幂等或既有统计口径；不创建客户、不重放事件、不调用 Provider，也不写入 Radar、Identity、Customer 或 WeCom 的数据。外部联系人 ID 属敏感身份展示，不能仅因 URL 位于 `/api/admin` 就向既有 Radar viewer 角色泄露。

### 两轴结论

```text
OneID: reads canonical customer and a narrow admin identity presentation Port; no resolve, provision, link or merge
Persistence: read-only PostgreSQL queries in existing Owners; no durable task, Provider read/write or External Effect
```

Radar 只读取它拥有的 `radar_view_sessions` / `radar_events`。昵称来自 Customer 的既有目录投影；标准 OneID 始终由 Identity 返回的 canonical CustomerID 按 `CID-<id>` 规范派生，绝不信任可漂移的目录 `oneid_label`；外部联系人 ID 由 Identity 的新增、窄化管理员投影在配置企业 scope 内返回。Radar 不访问 `customers`、`customer_identities`、Customer 或 WeCom 表，也不保留、记录日志或复用外部联系人 ID。

## 已核实根因与参考

- `radar_events` 和 `radar_view_sessions` 已在同一 Radar Owner 下保留 `session_id`、归因状态、当时的 `customer_id` 和发生时间；当前 `GET /api/admin/radar-links/{id}/events` 却只返回回执和阶段。
- `EventProjection` 同时服务公开事件追加，其契约禁止 `external_userid`、OpenID、UnionID、手机号和令牌。不得在它上面加管理端身份字段。
- `PublicAccessService` 对相同有效 session 重复访问会复用 session；`(session_id, radar_version, stage)` 使同一阶段幂等。因此一条访客明细表示一次访问会话的首次成功打开，不是每次浏览器点击。
- 目录 `oneid_label` 是可变展示缓存，未找到其全链路写入约束；本能力不能把它当 canonical OneID 事实。已有 CustomerID 是稳定根键，输出统一使用 `CID-<canonical id>`。
- GitHub #80 定义 Radar 只接受 scoped verified UnionID、Radar 不保存原始外部身份的边界；#244 规定 Radar 读失败须显式 unavailable，不能填充伪造数据；#3 是 OneID 的 canonical Customer 约束。它们均支持本方案，未复制旧实现。

## 访问会话语义

“打开”沿用当前 Radar 统计中的 view-stage 集合：

`content_opened`、`redirected`、`image_loaded`、`pdf_opened`。

Radar 在同一 `(session_id, radar_version)` 内将这些阶段归为一条访客记录，`opened_at` 取该集合中最早的 `occurred_at`：

- 链接在服务器实际进入重定向路径时产生 `redirected`；这证明 V3 已发起受控跳转，不声称第三方网站完成加载。
- 图片/PDF 先产生 `content_opened`，之后可有 `image_loaded` / `pdf_opened` 回执；后者补足既有统计，但不制造第二个“谁打开”记录。
- 同一客户以不同 session 再次打开会显示多条；同一 session 的重复请求仍为一条，时间为首次成功打开。
- `start_at` / `end_at` 过滤的是上述会话的 `opened_at`，而不是某个较晚加载回执的时间。范围使用 `[start_at,end_at)`，要求 `start_at < end_at`。

这样访客行不改变 PV、已解析 UV、查看次数和最近查看的既有统计定义；它是按会话的可解释明细，不能直接与事件计数相等比较。

## 身份和缺失语义

| Radar session 归因 | 昵称 | 外部联系人 ID | OneID | 行为 |
|---|---|---|---|---|
| `resolved` | Customer 目录的真实昵称；缺值显示“姓名暂缺” | 仅同企业 scope 内恰有一个 active + verified `wecom_external_userid` 时显示；同时返回受控状态 | 始终使用 canonical CustomerID 派生的标准 `CID-<id>`，不读取目录缓存标签 | 显示已识别客户 |
| `anonymous` | 空 | 空，状态 `missing` | 空 | 显示“未识别访客”，不猜测客户 |
| `pending` / `conflict` / `failed` | 空 | 空，状态 `missing` | 空 | 显示对应未确认状态；不建客、不合并、不选任意身份 |
| `resolved` 但无唯一可展示外部联系人 ID | 正常显示 | 空，明确为 `missing`、`ambiguous`，或 Identity Port 明确给出的 `unavailable` | 正常显示 | 前端不能把 `ambiguous` / `unavailable` 说成“未关联” |
| 读取依赖故障 | 不返回部分成功页面或 CSV | 不返回部分成功页面或 CSV | 不返回部分成功页面或 CSV | 503，保留筛选条件供重试；不能伪装成未识别访客 |

外部联系人 ID 的唯一性只在当前配置的 `wecom-corp:<corp>` scope 内判断。缺少、多个有效值或 Identity Port 明确的安全不可用状态分别作为 `missing` / `ambiguous` / `unavailable` 处理；任一 Owner 查询或关联读取本身失败仍为整个请求 `503`，不是 `unavailable` 行。canonical lineage 的缺失根、断链、环或达到深度边界同样是 `503`，不能静默成空匹配、`missing` 或 `unavailable`。不得从 UnionID、OpenID、昵称或任意第一条历史身份推断。

## 读模型与稳定 Port

新增只面向管理端的 Radar `VisitorSession` 读模型，独立于通用 `EventProjection`：

1. `RadarVisitorStore` 在 Radar UoW 内按 `radar_id`、打开时间和可选的规范客户候选集合查询自身表；按 `opened_at DESC, session_id DESC` 分页，返回 session、归因状态、捕获时 customer ID 和首次打开时间。
2. Radar app 通过新的 `AdminVisitorPresentationReader` 获得两种**纯读取**能力：
   - 对搜索词形成完整、有限的 canonical Customer 候选及其历史 lineage；
   - 对当前页的已解析 Customer IDs 批量补 canonical Customer、昵称、由 canonical CustomerID 派生的标准 OneID 和受控外部联系人 ID 状态。
3. 该 Radar-facing Port 只在 Composition Root 实现，并只依赖下列 Owner Port：
   - Customer 新增的窄批量目录展示/昵称搜索 Port，只读取 `display_name`，并以标准 `CID-<id>` 解析 OneID 搜索候选；
   - Identity 新增 `AdminRadarVisitorIdentityReader`，对指定企业 scope 的 canonical Customer 批量返回唯一 verified external contact ID 状态、外部联系人 ID 的精确查找和 canonical lineage。
4. `AdminRadarVisitorIdentityReader` 不能替代或扩张现有 `OutboundIdentityReader` / `ExternalIdentityValueReader`：后二者只允许 Provider 调用的临时读取，绝不用于 HTTP 输出。新 Port 明确是管理员展示用途，不返回 UnionID、OpenID、手机号、identity ID、scope 或 Provider payload。

Composition 读取链可使用独立只读 UoW；它不是一项原子写命令。任何 Owner 读取错误终止整个管理响应，防止把故障误写成身份缺失。

## 搜索、分页和 CSV

新增 `search` 参数，服务器端形成候选并传给 Radar 自有 SQL；不会先取当前页再在浏览器过滤。

- 昵称：由 Customer 目录在完整候选集内搜索；匹配的 canonical roots 扩展为其当前 lineage，再交给 Radar 过滤。
- `CID-<id>`：由 Customer Owner Port 按稳定 CustomerID 解析，再由 Identity Owner 验证存在和 canonical lineage；不会依赖、修复或暴露目录 `oneid_label`。
- 外部联系人 ID：只做当前企业 scope 中 active + verified 身份的精确匹配；结果再扩展 lineage。不会对任意 Provider 标识做模糊枚举。
- 多种命中取并集、去重；只要提供非空 `search`，没有匹配就返回真实空页和空 CSV，绝不退化为未筛选的全量访问。候选数超过明确的 `100000` 上限时返回可重试的范围过大错误，绝不静默截断而漏掉访问。
- 页面沿用当前管理端 `limit` 1–500 与 `offset` 0–1,000,000；每页当前真实行再批量补齐展示，避免按行 N+1。
- CSV 与页面使用完全相同的 radar、`search`、`start_at` 和 `end_at` 语义；一次最多 500 条匹配会话。超过上限返回既有 `409 export_range_too_large`，不导出部分结果或全局客户目录。
- CSV 使用业务列“昵称、外部联系人ID、外部联系人ID状态、OneID、打开时间、身份状态”，与页面采用同一 `external_contact_status` 语义，时间通过现有上海时区 formatter 输出。每个字符串单元格沿用订单 CSV 的公式注入防护：直接以 `=`, `+`, `-`, `@` 开头，或在前导空格、制表符、回车、换行后成为公式时，均以前置单引号保护；响应 `Cache-Control: no-store`。

## HTTP / OpenAPI 合同

保留既有低层 `events` 路由和公开 EventProjection，不变更其脱敏边界。新增：

- `GET /api/admin/radar-links/{radar_id}/visitors`
  - query：`search`（最多 200 字符）、`start_at`、`end_at`、`limit`、`offset`
  - 200：`RadarVisitorPage`，含 `items,total,limit,offset,has_more`
  - 400：非法时间、范围、分页或搜索值；401：无会话；403：缺失 CSRF 或非管理员；404：雷达不存在；409：候选范围过大；503：Customer / Identity 展示读取不可用。
- `GET /api/admin/radar-links/{radar_id}/visitors/export`
  - query：同页面，省略分页；200 CSV；409：超过 500；其余错误与页面保持语义一致。
- `RadarVisitor`：`nickname`（nullable）、`external_contact_id`（nullable）、`external_contact_status`（`available` / `missing` / `ambiguous` / `unavailable`；Owner 读取故障不产生 DTO）、`oneid`（nullable）、`opened_at`、`attribution_status`。`external_contact_id` 只在状态 `available` 时出现；不含 receipt、session ID、event ID、identity ID、UnionID、OpenID、手机号、scope、token 或 Provider payload。

既有 Radar `read` 允许 viewer 读取脱敏事件，不能复用。两条访客路由使用当前 Customer 敏感读取的 CSRF-backed session 授权路径：`AuthorizeCSRF` 成功后仅 `admin` / `super_admin` 可读；匿名 401、缺 CSRF 或 viewer/staff 403。它复用已存在的角色与 CSRF 能力，不把本次功能开发授权视为任何后台角色的一般性身份读取授权。页面、CSV 和错误响应均 `Cache-Control: no-store`，实现不记录 query 或 raw external contact ID 到应用日志。

## 验收与测试

1. PostgreSQL 集成测试：同一 session 的四个 view-stage 只出一行，取最早打开时间；两个 session 的同一客户保留两行；不同客户不合并；边界时间过滤、排序、分页和 500 上限正确。
2. Identity / Customer Port 测试：canonical merge 后显示 root 的 `CID-*`/昵称；脏或缺失目录 `oneid_label` 不能影响显示或 `CID-*` 搜索；同企业唯一 verified external ID 才返回；缺失、多个、错误 scope、OpenID/UnionID 都不会冒充 external ID；读取错误与缺资料可区分。
3. 搜索测试：昵称、CID、精确 external ID 都在完整雷达时间范围内命中，不依赖当前页；超上限失败不截断；匿名/冲突不会被匹配成客户。
4. HTTP/CSV 测试：管理员并具有效 CSRF 200；匿名 401、无 CSRF / viewer / staff 403；列表与 CSV 字段、筛选和上海时间一致；`external_contact_status` 的 missing / ambiguous 不混淆；超过 500 拒绝完整导出；503 不返回伪“未识别”；响应 `no-store`、CSV 公式保护，应用日志和测试输出不含 query 或禁止身份字段以外的任何 raw identifier。
5. OpenAPI 及生成客户端更新；前端使用 `/visitors`，不再以回执/阶段冒充“谁打开”。
6. 回归：公开 OAuth、session/event 幂等、统计 SQL 和旧 `/events` 脱敏契约不变；无 Provider 调用、无 External Effect。
