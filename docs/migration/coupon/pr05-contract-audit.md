# PR05 优惠券规则 donor 契约审计

本文件冻结 PR05 的优惠券规则管理边界和 v2 donor 的可观察行为。它是
冻结 donor 行为证据；对应 HTTP/OpenAPI、页面壳和数据库实现位于 PR05 集成分支。所有 donor
路径均来自只读树 `/tmp/aicrm-v2-audit.yN3jmr` 的冻结提交
`6bfbe5816bb89913c70adaca87d6a486260e016e`；v3 目标基线为
`68dc62b22d1fd7a609b6d00e5fe683cbb4a152ef`（含 Product canonical port）。

## 领域边界

本切片只冻结优惠券规则定义及其管理生命周期：

- 创建、编辑草稿、复制、发布、停用、归档和无领取草稿删除；
- Product Port 的 `standard_product:<positive integer>` 或
  `service_period:<positive integer>` 适用范围；发布前由 Product 适配器校验商品
  ID、CNY 货币和商品价格大于减免金额；
- 固定日期区间或领取后相对天数的有效期配置；
- 规则行自有的发行总量、已发行数、剩余数、状态、可用状态和更新时间统计。

明确不属于 PR05：领取执行、客户持券、核销/验证、订单、支付、退款、权益、
会员、Customer/OneID、公开领取页、侧边栏持券投影、历史券/领取/核销只读导入。
`issued_count` 是规则行的累计计数，不是本切片引入的 claim 表或客户维度统计。

## 页面入口与 Journey

这些入口是 donor 的原始页面契约；v3 集成只能由 v3-owned adapter 挂入
PR10 `internal/webshell/admin_base`，不得直接部署 donor 的第二套 `.side` 壳。

| 页面键 | donor 入口 | 原始模板 | PR05 处理 | 关键交互 |
| --- | --- | --- | --- | --- |
| `coupons` | `/admin/coupons` | `admin/templates/coupons.html` | 规则管理页面证据，可由 adapter 挂载 | 列表搜索/状态筛选；新建；编辑；复制；发布/停用；归档；删除草稿；分享按钮保留原样但其公开领取路径需单独 gate |
| `couponForm` | `/admin/couponForm.html`、`/admin/couponForm.html?id={positive}` | `admin/templates/couponForm.html` | 规则定义表单证据，可由 adapter 挂载 | 名称、减免金额、发行/单用户上限、领取窗口、固定/相对有效期、说明、target refs；商品选项查询/分页和加入/移除引用；保存草稿或保存并发布 |
| `couponData` | `/admin/coupons/{id}/data` | `admin/templates/couponData.html` | 整页只读 donor 证据；不得在 PR05 暴露 | donor 页面统计卡从领取记录派生，明细包含客户、商品、订单和核销字段；这些全部是排除域，不能通过 adapter 暴露 |

表单的原始 Journey 是：

1. 列表页读取当前优惠券页；点击新建进入空表单，点击编辑先读取指定规则详情。
2. 表单先从服务端商品选项目录搜索，按 `target_ref` 原值加入适用范围；浏览器
   不推断或转换商品编码。有效期在 `fixed_range` 与 `relative_days` 间切换，切换
   或商品翻页前快照 DOM 草稿，避免丢失未提交输入。
3. “保存草稿”执行创建或更新；“保存并发布”先保存，再对返回的规则 ID 执行发布。
   发布、停用、归档、复制和删除草稿均由列表中的真实生命周期操作触发，成功后回
   到列表并重新读取。
4. donor 的 `couponData` 入口会读取 claim 分页并把当前页的 claim 状态做成统计卡，
   因此不能把该页面当作 PR05 规则统计实现。PR05 的 `RuleApplication.Stats` 只
   返回规则行 counters；若需要展示，Terra 必须提供不读取 claim/customer/order
   的 v3-owned 投影或替换 adapter。

## 管理 API/DTO 证据

以下 URL 与方法来自原样生成文件
`web/src/api/generated/p4-coupon-compat/p4-coupon-compat.ts`，并已由 v3 后端 adapter
在不改 donor 前端请求的前提下实现、登记到 `api/openapi.yaml`。

### 规则管理（候选 PR05 adapter 合同）

| 方法与 URL | 请求 DTO | 成功 DTO | 行为 |
| --- | --- | --- | --- |
| `GET /api/admin/coupons?limit&offset&q&status` | `ListLegacyCouponsParams`：`limit 1..200`、`offset >=0`、`q <=80`、`status` | `LegacyCouponListResponse`：`ok`、`coupons/items[]`、`total`、`limit`、`offset` | 列表；donor 页面只对当前已读页做名称/可用状态本地筛选 |
| `POST /api/admin/coupons` | `CouponUpsertRequest` | `LegacyCouponCreateResponse`：`ok`、`coupon`、`coupon_id`、`fallback_used`、`create_replay_safe`、`real_external_call_executed` | 创建草稿；v2 注释称旧路由不 replay-safe，v3 必须以同事务 receipt/idempotency 语义适配 |
| `GET /api/admin/coupons/{coupon_id}` | 无 | `LegacyCouponDetailResponse`：`ok`、`coupon`、`data.coupon` | 读取规则详情；响应 ID 必须与请求 ID 一致 |
| `PUT /api/admin/coupons/{coupon_id}` | `CouponUpsertRequest` | `LegacyCouponUpdateResponse`：`ok`、`coupon`、`fallback_used`、`real_external_call_executed` | 更新规则；PR05 浏览器边界只允许未发行草稿编辑 |
| `DELETE /api/admin/coupons/{coupon_id}` | 无 | `LegacyCouponBoardMutationResponse`：`ok`、`coupon` | 仅删除未领取草稿；不是 claim 删除 |
| `POST /api/admin/coupons/{coupon_id}/publish` | 无 | `LegacyCouponMutationResponse`：`ok`、`coupon`、`fallback_used`、`real_external_call_executed`、可选 replay 字段 | 草稿发布；发布前校验 Product Port 的 CNY 价格 |
| `POST /api/admin/coupons/{coupon_id}/stop` | 无 | `LegacyCouponMutationResponse` | 已发布规则停用 |
| `POST /api/admin/coupons/{coupon_id}/archive` | 无 | `LegacyCouponBoardMutationResponse` | 规则归档；donor 文案提到“保留领取/核销记录”，记录本身不在 PR05 |
| `POST /api/admin/coupons/{coupon_id}/copy` | 无 | `LegacyCouponBoardMutationResponse` | 复制为新的草稿，规则字段复制，计数/生命周期重新开始 |
| `GET /api/admin/coupons/product-options?q&product_type&limit&offset` | `ListLegacyCouponProductOptionsParams`：`product_type=all|standard_product|service_period` | `LegacyCouponProductOptionsResponse`：`ok`、`items[]`、`total`、`limit`、`offset` | 三种筛选均直接委托 Product canonical Port；不构造 Coupon 本地目录。 |

`CouponUpsertRequest` 的字段为：`name`（1..45）、`discount_amount_total`（正整
数，分）、`total_issue_limit`（正整数）、可选
`per_user_issue_limit`（正整数）、`claim_starts_at`、`claim_ends_at`、
`validity_mode=fixed_range|relative_days`、可空固定使用时间、可空
`relative_validity_days`（正整数）、`instructions`（最多 200）和
`target_refs`（1..100）。v3 service 规范化货币为 CNY，并要求 target ref 去重且
严格匹配 `standard_product:<id>` 或 `service_period:<id>`。

`Coupon` 响应在上述规则字段外包含 `id`、`currency`、`status`、
`availability_status`、`issued_count`、`created_by`、`updated_by`、`version`、
`created_at`、`updated_at`。donor schema 的可选 `history_only` 只表示只读历史
记录；历史定义、claim 和 redemption 不在本切片。

### 明确排除的 donor API

这些生成 operation 和任何对应路由不可由 PR05 adapter 暴露：

| 方法与 URL | DTO/原因 |
| --- | --- |
| `GET /api/admin/coupons/{coupon_id}/claims?limit&offset` | `LegacyCouponClaimListResponse`；客户领取记录，含 claim ref/领取时间 |
| `GET /api/h5/coupons/available?target_ref=...` | `H5CouponAvailableResponse`；公开可领取券投影 |
| `GET /api/h5/coupons/{public_slug}` | `H5CouponDetailResponse`；公开领取页 |
| `POST /api/h5/coupons/{public_slug}/claim` | `H5CouponClaimResponse`；领取执行和身份会话 |
| `GET /api/sidebar/v2/coupons` | `SidebarCouponListResponse`；客户持券侧边栏投影 |
| `GET /c/{public_slug}`（生成 operation 名 `getPublicCouponPage`） | 公共兼容页面；不属于规则管理 |
| `GET /api/admin/coupons/{coupon_id}/share` | `LegacyCouponShareResponse`；分享链接指向公开领取路径，需等公开能力和安全边界另行批准，不得因 donor 按钮自动开放 |

生成文件没有独立 `stats` operation。`couponData` 的五张统计卡是 controller 从
claim 行计算的“累计领取/当前可用/支付预占/已使用/已过期”，并非规则-owned
statistics。因此 v3 `RuleStats` 故意只含总量、已发行、剩余量、规则状态、可用状态
和更新时间；任何 customer/order/payment/claim 字段都不能添加到该 port。

## v3 叶子代码与适配边界

本分支只保留以下领域叶子：

- `internal/coupon/port/port.go`：规则值对象、`RuleStats` 和
  `RuleApplication`；没有 claim/holder/redeem/identity API。
- `internal/coupon/port/target.go`：只携带 Product ID、CNY currency 和 price minor
  的临时 ProductReader port；不得 import Product store/app/http。
- `internal/coupon/port/events.go`：规则 mutation event seam；不得在该叶子执行
  provider 或网络副作用。
- `internal/coupon/app/service.go`：创建、更新草稿、发布、停用、归档、删除、复制、
  列表、详情和规则 counters 统计；所有 mutation 通过 UoW receipt 与本地 event seam
  闭合，产品校验只在发布时发生。
- `internal/coupon/app/board_test.go`：生命周期、规则统计、Product Port 边界、发布后
  草稿锁定、receipt replay/冲突测试；没有客户 ID、claim、订单或支付 fixture。

Terra 在集成 lane 负责：Coupon-owned SQL/store/UoW receipt、audit/outbox、独立
migration；把本地 event seam 接到 v3 versioned event/outbox；把 ProductReader 接到
canonical Product port；定义读一致性、CAS/锁和 HTTP 错误映射；在 PR10 shell 下挂
载原样 donor hooks，并确保所有排除 API/页面未注册。任何适配不得修改原样 donor 文件，
不得让 Coupon import Product/Customer/Order/Payment store 或中央 Composition Root。

## 原样文件和验证

原样前端共 19 个文件，完整列表及 donor/source SHA 与目标 SHA 位于
`pr05-donor-sha256.txt`。目标路径统一在
`web/donors/coupons-v2/src/`，与 donor `web/src/` 相对路径一一对应；逐文件
`cmp` 必须通过。前端闭包包含模板、控制器、入口、导航/registry、coupon API
generated schemas、HTTP transport、shared API client/types/mock 和 shared UI
依赖，但这些文件都只作为冻结证据保存于 build tree 之外；不能从中复制第二套壳或
把 mock 当作成功写入。

后端目标文件是 v3 适配后的 SHA，不能与 donor SHA 机械比较；SHA 文件同时保留
donor source SHA、目标 SHA 和 `cmp` 结果。该提交不触碰 `cmd/aicrm`、
`internal/webshell`、`internal/platform`、`internal/access`、OpenAPI、migration、
依赖锁、deploy 或 CI。
