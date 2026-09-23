# PR05 优惠券规则管理 donor 完整闭包复核

本复核只读检查 donor `AI-CRM-v2` 固定提交
`6bfbe5816bb89913c70adaca87d6a486260e016e`，以及 v3 已部署基线
`015587164bd52ee5d24978aa07970b7c782fbb1e`。复核分支为
`codex/pr05-closure-review`；本提交不修改主工作树，不 push、merge 或 deploy。

## 结论先行

- **前端硬门禁通过（以已批准的准备/审计提交为证）：19/19 文件逐字节相等。**
  `e8263f949866087cfd16f01c74433cf5b1514a2f` 的 `cmp` + SHA-256 审计在固定 donor
  SHA 的完整临时 clone 上通过：19 个文件、2 个活动规则模板、1 个明确排除的
  `couponData.html` 证据文件，全部 PASS。哈希账本是
  `docs/migration/coupon/pr05-donor-sha256.txt`；门禁脚本是
  `scripts/check-pr05-donor-frontend.sh`（本分支另提供 closure gate，见文末）。
  前端业务 HTML/TS/CSS、图标、文案、默认值、binding、交互顺序、URL 和 DTO 均不作
  发挥；本复核提交没有修改 donor 前端。
- **实际构建入口已确定，但 donor 有一条不能混用的第二实现。** Node 构建的业务
  来源是 `web/scripts/build.mjs:209-222` 的 `admin -> web/src/admin/main.ts`，
  由 registry 生成 `dist/admin/coupons.html` 与 `dist/admin/couponForm.html`。
  固定 donor 同时注册了 Go compatibility carrier/minimal HTML（见“第二实现”），
  它们不是这套构建的业务页面，不能和静态 donor fragment 混接。生产接入必须继续使用
  donor 的冻结 browser path；v3 只在挂载和后端路由边界做 allowlist，不另写前端 runtime。
- **PR10 单侧栏门禁通过。** v3 `internal/webshell/templates/admin_base.html`
  是唯一 admin shell，只有一个 `class="admin-sidebar"`。donor `build.mjs` 生成的
  `.shell/.side/side-nav` 不能被挂载；只允许 v3-owned mount/route adapter 把原样业务
  fragment 放进 stage/template 槽位。当前 v3 没有第二 admin 壳，但如果直接部署 donor
  full `dist` 或使用 Go minimal page，就会重新引入第二实现/第二壳风险。
- **donor 规则服务闭包作为行为参考；v3 PR05 已在本分支实现本地规则管理闭环，待集成验收。** donor 的状态机、
  row lock/version、同 UoW receipt/event、Product Port 发布校验和安全草稿删除已复核；但
  v3 基线 `0155871` 尚无 `internal/coupon` 或 `internal/product` 领域实现，也没有
  coupon API adapter、规则 migration、audit/outbox 接线或 real browser Journey。
  因此本提交是 closure review，不把静态证据或 donor 通过的 Go 测试冒充 v3 完成。

## 先行边界分类

- **OneID/外部身份：不涉及。** PR05 只管理规则和 Product Port 引用；不读取 Customer、
  `external_userid`、手机号、领取人或客户归属，商品 target ref 不是身份键。
  donor 的 claim、payment identity session、sidebar grant 和 Customer 代码均在排除域。
- **持久化：涉及。** 规则、状态、版本、发行计数、receipt、事件/审计需要由 v3
  Coupon Store 和 PostgreSQL Unit of Work 原子提交。
- **内部持久任务：不涉及。** 创建/编辑/复制/发布/停用/归档/删除是同步管理命令，
  不新增 jobqueue、worker、lease 或重试状态机。
- **External Effects：不涉及。** 不开放领取、核销、公开链接、H5、sidebar 或客户
  持券读取；页面中的分享 binding 只保留 donor 视觉，adapter 必须 fail closed。

## donor 实际构建、页面与入口

### 构建路径

`web/scripts/build.mjs` 在 `:11-15` 固定 `src/dist/assets`，在 `:44-49` 读取
`admin/registry.json` 与 `admin/nav.json`，在 `:102-125` 为非 rich 页面读取对应
`admin/templates/<screen>.html` 并放入 `<template id="tpl">`。实际 esbuild entry
在 `:209-222` 明确为：

```text
admin -> web/src/admin/main.ts
h5 -> web/src/h5/main.ts
sidebar -> web/src/sidebar/main.ts
...
```

随后 `:255-263` 为每个 registry screen 写出 `dist/admin/<screen>.html`、H5、
sidebar 与其它索引页。coupon 在 registry 中的 screen key 是 `coupons`、
`couponForm`、`couponData`；前两者是规则管理页面，后者仅保留证据。

`web/src/admin/main.ts:4-10` 根据 `data-page` 选择模块；coupon 等非 customers 页面
会进入 `import("./legacy")`。`legacy.ts:1-11,23-124,413-421` 是一个宽域兼容加载器：
它注册约 28 个其他 section 的 deferred import，最终才以 `AdminController` mount
模板。由此得到两个必须同时遵守的事实：

1. 构建入口是 donor `main.ts`，不能另写一套 coupon HTML/TS/CSS；
2. `main.ts -> legacy.ts -> AdminController` 是实际且必须保留的 donor browser runtime，
   即使它是宽域 graph（约 131 个 source inputs，并包含
   `qrcode-generator`、`fflate`、`read-excel-file`、`@xmldom/xmldom` 等依赖）。这些
   宽依赖不能通过删文件、改 import 或另写 coupon runtime 来“优化”。v3 的 allowlist
   只决定挂载 `coupons`/`couponForm` 模板和允许的规则 HTTP URL；不开放其它页面/API，
   不改变 donor bundle 的执行链。

v3 正确边界是 v3-owned mount/route adapter：按 donor 原样 release bundle 装载
`main.ts -> legacy.ts -> AdminController`，只给 `/admin/coupons`、new/edit 对应
模板和规则 API 放行；data/claims/share/H5/sidebar/public 等路径由后端明确 fail closed。
不得另写 coupon frontend runtime，不得为了编译修改 donor 文件。

### 页面和 Go compatibility path

| 页面 | donor browser artifact | PR05 判定 |
| --- | --- | --- |
| `/admin/coupons` | `dist/admin/coupons.html`，模板 `coupons.html` | 活动规则列表 fragment |
| `/admin/couponForm.html` | `dist/admin/couponForm.html`，模板 `couponForm.html` | 新建规则活动 fragment |
| `/admin/couponForm.html?id={positive}` | 同 `couponForm.html`，由 query/DTO 填充 | 编辑规则活动 fragment |
| `/admin/coupons/{id}/data` | `couponData.html` | 排除；只留 byte-exact evidence |

固定 donor 的 `cmd/aicrm/legacy_coupon_page.go:11-23` 把 `/admin/coupons` 定义为
carrier：鉴权后跳转 `/?legacy_admin_path=%2Fadmin%2Fcoupons`，不是业务模板服务。
`cmd/aicrm/legacy_coupon_board_api.go:23-72` 另有 `couponPageTemplate` 这一套极简
Go HTML，并由 `CouponNewPage`/`CouponEditPage`/`CouponDataPage` 使用；它只有一个
`<main>` 和文本列表，缺少 donor SPA 的完整 DOM、binding、样式和交互。

这两条路径都是真实代码，故不能把极简 Go 页面误报为 donor 活动构建，也不能把两者
字段拼在一起。发布前必须在实际 ingress/静态服务上观察 `/admin/coupons*` 的最终
HTML 和 browser network：确认只提供原样 donor fragment 的 v3 adapter；如果仍命中
Go minimal page，则是未闭合的路由选择缺口，必须先修正路由 ownership，不能静默混用。

## 前端 100% 原样门禁

### 文件计数和哈希

冻结 inventory 共 **19** 个文件：

- 规则模板/入口证据：`admin/templates/coupons.html`、`admin/templates/couponForm.html`、
  `admin/templates/couponData.html`、`admin/controller.ts`、`admin/main.ts`、
  `admin/nav.json`、`admin/registry.json`；
- API/DTO/transport 证据：`api/admin.ts`、`api/transport.ts`、
  `api/generated/p4-coupon-compat/p4-coupon-compat.ts`、
  `api/generated/health.schemas.ts`；
- shared 运行时/UI 证据：`shared/api/client.ts`、`shared/api/types.ts`、
  `shared/api/mockData.ts`、`shared/ui/download.ts`、`shared/ui/feedback.ts`、
  `shared/ui/picker.ts`、`shared/ui/runtime.ts`、`shared/ui/tokens.css`。

每个 donor `web/src/<relative>` 必须与 v3 evidence
`web/donors/coupons-v2/src/<relative>` 同时满足 ledger SHA-256 和 `cmp`。现有审计实测
结果是：

```text
donor = 6bfbe5816bb89913c70adaca87d6a486260e016e
files = 19/19
active_rule_templates = 2
excluded_evidence = 1 (couponData.html)
cmp + sha256 = PASS
```

本闭包 review 不会为了 v3 兼容而改任何 donor business byte。特别是：

- `coupons.html` 的 `data/share` 操作、分享弹窗和所有 donor 文案必须保留；排除动作
  由 adapter 路由 gate，不得删按钮或改文案；
- `couponForm.html` 的字段 ID、默认单用户限领 `1`、相对有效期默认 `7`、固定/相对
  单选、target ref 原值和预览文案必须保留；
- donor controller 的校验顺序、金额转 minor、时间 ISO 映射、保存后跳转、toggle 文案
  和错误反馈不能被“优化”；
- `tokens.css` 即使包含 `.shell/.side` 选择器也必须 byte-exact，v3 只禁止 donor
  shell DOM 和 donor shell asset 作为壳，不借此改 CSS。

`api/admin.ts`、generated schemas 和 shared types 是宽域 donor glue，包含 claim、
H5、sidebar、Customer 等结构。它们的存在是证据，不是开放这些 API 的许可；v3
adapter 必须只取规则 DTO，生产不得注入 `MockApi`/sessionStorage 伪成功。

## 规则管理交互闭包

| 交互 | donor 可观察行为 | v3 适配硬约束/缺口 |
| --- | --- | --- |
| 列表、搜索、筛选 | `GET /api/admin/coupons?limit&offset&q&status`；controller 对当前已加载集合按名称和 `availability_status/status` 本地筛选。UI 有全部、草稿、未开始、进行中、已领完、已结束、已停止、已归档。 | 保留 donor 本地筛选语义，不发明第二套筛选接口。generated query 类型只有 `draft|published|stopped`，donor service 另验证 `archived`；adapter 要明确这个边界并返回规则-owned availability。 |
| 新建/编辑 | `/admin/couponForm.html` 或 `?id={positive}`；详情 ID 必须和 URL 一致。表单字段和默认值保持原样。 | 仅管理员 capability + CSRF；无效 ID 404；禁止隐式建客。 |
| 商品适用范围 | `GET /api/admin/coupons/product-options`，查询、分页、重试、加入/移除 target ref；视觉上有 `all|standard_product|service_period`。 | 三种查询都只委托 Product 的 canonical Port；规则保存 `standard_product:<id>` 或 `service_period:<id>`，发布时确认当前 Product 为 CNY 且价格高于优惠。 |
| 保存草稿 | 创建 `POST /api/admin/coupons`，编辑 `PUT /api/admin/coupons/{id}`；donor 在 controller 中先 snapshot DOM，再做名称/金额/上限/时间/target 校验，成功 toast 后回列表。 | 同一 UoW 写规则、receipt、audit/event；保留 donor DTO/错误顺序。donor create response 明确 `create_replay_safe:false`，不得照搬。 |
| 保存并发布 | donor 先保存，再对返回 ID 调 publish；这是两个命令和两个潜在 receipt。 | 必须定义保存+发布的重试边界：每一步 actor-scoped idempotency、payload conflict、同 UoW receipt，不能把第二次 publish 当成 create 的隐式副作用。 |
| 复制 | `POST /api/admin/coupons/{id}/copy`，复制字段、生成新 ID/草稿、发行计数重置，随后跳编辑页。 | 新规则保留同一 Product refs；操作 receipt/audit 同事务；不能复制 claim/customer/order。 |
| 发布 | `POST /api/admin/coupons/{id}/publish`；donor service 只允许 draft -> published，并在发布时验证 Product。 | Product ID 精确匹配、CNY、`PriceMinor > discount`；重放不得重复状态/事件。 |
| 停用 | `POST /api/admin/coupons/{id}/stop`；published -> stopped，相同状态返回幂等事实。 | 只允许已发布规则；状态和 receipt/audit 原子提交。donor handler 使用按 ID 生成的 deterministic key，不能当作完整 caller idempotency。 |
| 归档 | `POST /api/admin/coupons/{id}/archive`；draft/published/stopped -> archived，保留历史语义。 | 只写规则生命周期；不得触碰 claim/redemption；header key 需做 actor/payload 冲突检查。 |
| 删除草稿 | `DELETE /api/admin/coupons/{id}`；仅 `status=draft && issued_count=0`，否则 conflict。 | donor 规则与 UI 文案原样保留；不能实现 claim 删除或物理清理客户记录。 |
| 状态/规则统计 | `issued_count`、总量、剩余量和 derived availability 属于规则行；scheduled/active/sold_out/ended 是可用性推导。 | 只读规则-owned counters。`couponData.html` 五张卡是 claim 行派生的领取/使用统计，不能作为 PR05 stats；当前 v3 没有 RuleStats 投影。 |
| 数据/分享按钮 | 列表行保留 `r.data`、`r.shareIt`，`couponData` 还含 claim 明细和分享弹窗。 | 前端按钮和 binding 不得隐藏/改写；后端对 `/data`、`/claims`、`/share`、H5/public/sidebar 明确 fail closed，且 list/form 允许 Journey 不得依赖这些排除响应。 |

已知 donor UI 边缘行为也必须记录而非改前端：toggle 用原始 `status` 判断 publish/stop，
`scheduled`、`sold_out`、`ended` 行可能显示 publish 并由服务返回 conflict；这是 donor
行为，v3 用后端状态机/错误 toast 收敛，不改按钮文案或 controller。

## API/DTO 和排除域

### 规则 adapter 允许的 URL

```text
GET    /api/admin/coupons?limit&offset&q&status
POST   /api/admin/coupons
GET    /api/admin/coupons/{coupon_id}
PUT    /api/admin/coupons/{coupon_id}
POST   /api/admin/coupons/{coupon_id}/publish
POST   /api/admin/coupons/{coupon_id}/stop
POST   /api/admin/coupons/{coupon_id}/archive
POST   /api/admin/coupons/{coupon_id}/copy
DELETE /api/admin/coupons/{coupon_id}
GET    /api/admin/coupons/product-options?q&product_type&limit&offset
```

请求字段保持 donor `CouponUpsertRequest`：`name`、`discount_amount_total`（minor）、
`total_issue_limit`、`per_user_issue_limit`、claim start/end、
`validity_mode=fixed_range|relative_days`、fixed use start/end 或 relative days、
`instructions`、`target_refs`。v3 规范化为 CNY，target refs 只允许去重的
`standard_product:<positive integer>` 或 `service_period:<positive integer>`。响应必须有规则 ID、状态、availability、计数、
version、actors 和时间；不得加 customer/claim/order/payment 字段。

以下路径、DTO 和任何数据库读取全部排除：

- `GET /api/admin/coupons/{id}/claims`、`couponData.html` 的 claim/customer/order/use
  明细和五张 claim-derived cards；
- `GET /api/h5/coupons/available`、`GET /api/h5/coupons/{public_slug}`、
  `POST /api/h5/coupons/{public_slug}/claim`、`GET /c/{public_slug}`；
- `GET /api/sidebar/v2/coupons` 和 payment identity/session、sidebar grant；
- `GET /api/admin/coupons/{id}/share`；它会产生公开领取 URL，不属于规则管理；
- `GET /api/admin/coupon-history/{id}/claims`、`redemptions` 及 V1 historical tables。

这些 operation 可能仍出现在 byte-exact generated file 或 donor router 中，只能作为
排除证据；不能进入 v3 API adapter、OpenAPI 发布清单或 PR10 路由。

## donor 后端闭包复核

### 已确认可复用的规则行为

- `internal/coupon/app/service.go:92-140` 在读操作使用 UoW，并验证 stored rule 和
  derived availability；`142-227` 覆盖 create/update/publish/stop/archive/delete/copy。
- `:509-632` 的 mutation 流程在同一 UoW 内 reserve receipt、锁规则、做状态转移、
  append event、写 result snapshot、complete receipt；`:634-681` 做字段、时间、target
  ref 规范化；`:688-705` 在发布前读取 Product 并验证 CNY/价格。
- `internal/coupon/store/repository.go:26-31,75-160` 只从 transaction context 取
  sqlc queries；Create/Update/target refs/counter/status/receipt 在同一交易上下文中；
  `Lock` 使用行锁，Update 将 version 增加。
- 状态机为 draft -> published -> stopped/archived；archive 允许 draft/published/stopped；
  copy 产生新 draft；delete 只允许未发行 draft；claimed rule 除允许的 total limit
  增长外被冻结。availability 从 status、claim window 和 issued/total 推导。

### 必须在 v3 修正/补齐的点

1. **幂等不完整。** `cmd/aicrm/legacy_coupon_api.go:148-171` 对 create/update 每次
   随机生成 `legacy-coupon:<random>`，忽略 caller 的 `Idempotency-Key`；create response
   在 `:85` 明确 `create_replay_safe:false`。publish/stop 在 `:130-145` 使用
   `coupon:<op>:<id>`，可以表达同状态，但不能替代 actor/payload scoped key。archive/
   delete/copy 由 board API 读取 header 并在 `:148-177` 进入 receipt。v3 必须为所有
   rule writes（含 save+publish 两步）定义固定的 actor scope、key digest、payload digest、
   replay snapshot 和 conflict，且和状态、audit 一起 UoW 原子提交。
2. **审计接口未闭合。** donor 的 event appender (`service.go:495-506,604-615`) 是
   技术事件，不自动等价于 v3 批准计划的 audit/outbox contract。v3 要明确规则 audit
   owner、actor、before/after/digest、拒绝/重放记录，并验证和 receipt/状态在同一
   PostgreSQL UoW；不允许跨两个独立事务假设原子性。
3. **Product 端口不应直连 store。** donor Composition (`cmd/aicrm/api.go:1233`)
   直接把 `productstore.NewCatalogRepository()` 传给 Coupon service；donor coupon
   `ProductReader` 还期望 `Get`，而稳定 `internal/product/port.Reader` 的窄接口是
   `ReadProduct` (`internal/product/port/port.go:92-94`)。v3 只能通过稳定 Product
   port/adapter 读取 ID、currency、price；Coupon 不得 import Product store/app/http。
4. **当前 v3 尚无实现。** 在基线 `0155871`，`internal/coupon`、`internal/product`
   目录、Coupon Store、独立 migration、HTTP DTO adapter、Composition mount、规则
   stats/audit 和 browser Journey 均不存在。准备提交 `4727238`/前端审计 `e8263f` 的
   contract 是“准备/证据”，不能被解释成这些代码已落地。
5. **migration 必须独立。** donor `00033_coupon_rules.sql` 是规则表和 receipt，
   `00036_coupon_claims_and_public_access.sql` 扩展 archived/claim/payment/sidebar，
   `00112_coupon_v1_history.sql` 是历史券/claim/redemption。v3 不复制 donor migration
   历史；只在批准的独立 migration 中建 Coupon-owned rule/receipt/audit 结构，不建客户
   持券、身份 session 或公开领取表。
6. **权限和失败语义要由 v3 adapter 明确。** donor route 具备 Coupons read/write
   capability、CSRF 和 400/401/403/404/409/503 映射；v3 需要窄路由复刻这些可观察错误，
   不以 mock 空列表、HTTP 200 或排队成功代替规则读写成功。

### OneID/客户域越界检查

规则写入仅以 admin principal、规则 ID、商品 target ref 和本地 Product 读模型为输入。
donor 的 `BoardStore` 虽然在同一接口里包含 `ListClaims`、`CountCustomerClaims`、
`ResolvePaymentIdentitySession`、`ResolveSidebarGrant` 等方法，但这些方法属于排除
域；v3 rule port 必须删除它们，不能因为 donor service 接口宽而导入 Customer/Identity
store。PR05 不创建客户、不解析 external identity、不做隐式合并，也不向 OneID 发请求。

## unused / 第二套实现清单

| 代码/资产 | 发现 | 处理 |
| --- | --- | --- |
| `web/scripts/build.mjs` + `web/src/admin/main.ts` | donor 实际静态 browser build graph；非 customers 页面进入宽域 `legacy.ts`，最终由 `AdminController` mount | 必须保留冻结 `main.ts -> legacy.ts -> AdminController` 链；v3 只 allowlist coupons/couponForm 挂载和规则 URL，不另写 runtime、不开放其它页面/API |
| `cmd/aicrm/legacy_coupon_page.go` | `/admin/coupons` Go carrier，鉴权后 redirect | 只作为旧路由行为证据；v3 由唯一 admin_base + adapter 明确 ownership |
| `cmd/aicrm/legacy_coupon_board_api.go:23-72` | 第二套极简 `couponPageTemplate`，new/edit/data 输出与 SPA 不同 | **unused for PR05；禁止与 donor SPA 混用** |
| donor `admin/legacy.ts` | 28 个 section deferred imports，含 survey、history、marketing 等无关域 | 这是必须保留的 donor runtime 链；不修改/不裁剪，v3 仅在页面和后端 URL allowlist 层限制可观察范围 |
| `couponData.html`、claims/history/H5/sidebar/share | claim/customer/public/核销域 | byte-exact evidence 保留，adapter/route 全部排除 |
| `shared/api/MockApi`、`mockData.ts` | sessionStorage 和伪成功 mutation | 仅 donor evidence；冻结 browser bundle 的实际运行仍必须走真实 transport，生产不得切换 MockApi |
| donor `.shell/.side/side-nav`、`nav.json`/`registry.json` shell metadata | donor 第二壳/导航数据 | 文件按硬门禁保留，但 PR10 nav/shell 归 v3；不生成 donor shell DOM |
| donor `coupon_v1_history` 及 history API | 历史 definitions/claims/redemptions | 不迁入、不提供 PR05 读取 |

因此“第二壳”结论是：**当前 v3 admin_base 中不存在第二壳，donor 源码中存在可生成
第二壳的模板和另一个 minimal page 实现，第二壳风险未被前端字节审计自动消除。** 只有
fragment-only release adapter、单一 v3 route ownership、shell DOM 断言和 browser
network trace 全部通过，才能关闭该风险。

## PR10 单侧栏复核

- `internal/webshell/templates/admin_base.html:40-73` 只渲染一个
  `<aside class="admin-sidebar">`；v3 `contract.go:91,133-139` 已把 Coupons 放在
  “交易”组，endpoint 是 `api.admin_coupons_page`，不能再从 donor `nav.json` 追加菜单。
- `internal/webshell/handler.go:240-244` 当前 `/admin/coupons` 只是 v3 placeholder；
  这是“未实现”事实，不是已挂载 donor 页面。
- `internal/webshell/renderer.go:166-180` 的 `RenderMedia` 展示了批准的模式：只接受
  已验证 release template 和 manifest assets，渲染 `stage + template` 到 `admin_base`，
  不嵌入 donor full HTML/nav。Coupon adapter 应复用该边界思想，但本复核不改
  Composition Root 或 webshell。
- PR10 的 shell/HTML/CSS/asset ownership 只能是 v3；原样 donor tokens/controller/
  templates 不能自行带第二 `<html>`、`<body>`、`<aside class="side">` 或 side nav。
  门禁应断言 active coupon fragments 没有 donor shell markup，并断言最终响应中
  `class="admin-sidebar"` 恰好一次。

## 验证和剩余缺口

### 本次窄测

以下命令均通过：

```text
PR05_DONOR_ROOT=/private/tmp/pr05-donor-audit.<tmp>/donor \
  bash /private/tmp/aicrm-v3-coupon-audit/scripts/check-pr05-donor-frontend.sh
  # 19/19 cmp + sha256 PASS; active templates=2; excluded evidence=1

(donor 6bfbe581) go test ./internal/coupon/...
(donor 6bfbe581) go test ./cmd/aicrm -run 'Coupon' -count=1
(v3 0155871)    go test ./internal/webshell ./internal/media
```

没有运行 donor `npm build`：固定 donor worktree 没有可复用的 `node_modules`，构建会
写入 donor `web/dist`；本次审计所需的 source hash/cmp、入口静态检查和 Go 规则窄测已
完成，且不应以构建产物替代逐字节门禁。正式集成仍需在受控 release workspace 运行
固定 Node/npm/esbuild 版本并检查 manifest 输入闭包。

### 本分支交付事实与剩余验收

1. `0011_coupon_rules.sql` 只建 Coupon-owned rule、target、receipt、append-only audit 和
   outbox 表；无 claim/public/customer/order/payment/identity 表。
2. `internal/coupon` 的 Store、receipt、audit 和 outbox 写入由同一 PostgreSQL UoW 提交；
   draft/create/update、publish/stop/archive/copy/delete 均有严格状态和重放/payload-drift
   边界。
3. 商品下拉读取和发布校验均通过 Product-owned port：`ListProductOptions` 覆盖 standard/
   service_period/all，`ReadProductTarget` 再核对类型、CNY 与 `price > discount`。Coupon
   不读 Product 表，也不建立本地商品目录。
4. v3 HTTP adapter 完成鉴权、CSRF、显式幂等键、DTO 和失败映射；`couponData`、claims、
   redemption、holder、public-link 等排除路径均不注册。
5. `coupons` 与 `couponForm` 作为 release-private donor templates，经 `donortemplate.Extract`
   挂入 PR10 的单一 `admin_base`；19 个冻结 donor 文件不修改，未新增 donor runtime。
6. 尚待主分支环境验收：固定 donor Node/npm 版本的实际 build、浏览器 DOM/Journey 截图，
   以及有 PostgreSQL 16 URL 时的 fresh/upgrade 迁移运行。它们不是以 mock 或 HTTP 200
   代替的完成声明。

## 复核提交

本分支实现了上述闭环，仍未 push、未 merge、未 deploy。
