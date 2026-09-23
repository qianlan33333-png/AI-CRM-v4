# PR04 商品管理 donor 前端冻结审计

审计对象是 AI-CRM-v2 `main` 的冻结 donor `6bfbe5816bb89913c70adaca87d6a486260e016e`；
`b0bd173` 将 donor 文件放在 `web/donors/products-v2/src`。本审计记录 donor 证据与 v3
适配边界；实现可修改 v3 宿主/adapter，但不得修改任何 donor 前端字节。

## 分类结论

- OneID/外部身份：不涉及。商品定义的身份是商品自身的 `id` 或
  `service_product_id`，本板块不解析客户、external_userid、手机号归属，也不做客户绑定或标签打标。
- 持久化/内部任务：涉及本地商品定义的 PostgreSQL 事务、版本号和幂等收据；不自行引入队列或
  Worker。商品写入、审计和本地事件由 v3 Product Store/UoW 原子提交。
- 外部效果：商品页面只保存外部推送的本地配置，并可记录本地 test acceptance。真实企微/微信/支付
  Provider 写入、回执、`outcome_unknown`、对账和重试策略必须由 `outbound`/External Effects 承担。
- 完整闭环：PR04 必须同时交付商品列表、表单、共享/二维码交互、图片/渠道选择、所有页面请求对应
  的后端路由/DTO、权限、事务、审计、回归和部署适配；不能只挂静态页面或只交 API。

## 冻结结果

| 检查项 | 结果 |
| --- | --- |
| donor commit | `6bfbe5816bb89913c70adaca87d6a486260e016e` |
| staged frontend files | 24 个，全部存在 |
| donor 与 staged SHA-256 | 24/24 一致 |
| donor 与 staged byte compare | 24/24 `cmp` 通过 |
| frozen donor Node/npm `version:check` | 通过（Node `v24.18.0`、npm `11.12.1`） |
| frozen donor TypeScript `typecheck` | 通过 |
| frozen donor `npm run build` | 通过（56 pages + 9 hashed entries） |
| 独立商品 CSS/图片/字体二进制 | 无；页面样式为模板内联，二维码依赖 `qrcode-generator@1.5.2` |
| v2 runtime/database/provider dependency | 禁止；staged 目录只作为只读 donor 证据 |
| 自动校验 | `scripts/check-pr04-donor-manifest.sh` |

校验使用 `docs/migration/product/pr04-donor-sha256.txt` 中的 24 条 `web/src/**` 记录，并同时比较
donor 原路径与 `web/donors/products-v2/src/**`。后端准备文件的 hash 也保留在 manifest 中，但不被
本前端校验冒充为 donor byte match；它们属于 v3 适配实现。

## 页面边界（必须原样挂载）

下列四个页面是 PR04 的完整前端能力边界。`template`、controller 分支、API wrapper、生成客户端、
共享 UI 依赖必须保持 donor 字节；只能由 v3 Web adapter 提供路由、认证、CSRF、DTO 兼容和数据装配。

| 页面 key | donor 模板 | donor registry/nav | PR10 单壳 canonical URL | 页面能力 |
| --- | --- | --- | --- | --- |
| `products` | `web/src/admin/templates/products.html`（47 个非空模板行） | registry `243-249`；nav `69-72` | `/admin/wechat-pay/products` | 普通商品列表、创建、编辑、分享入口、复制、启用/停用、删除 |
| `productForm` | `web/src/admin/templates/productForm.html`（102 个非空模板行） | registry `289-294`，非导航一级页 | `/admin/wechat-pay/products/new` 与 `?id=<id>` 适配路径 | 普通商品创建/编辑、图片、售卖字段、购买后动作、企微标签原值、外部推送本地配置 |
| `spProducts` | `web/src/admin/templates/spProducts.html`（47 个非空模板行） | registry `99-105`；nav `75-78` | `/admin/service-period-products` | 周期商品列表、创建、编辑、数据入口（必须禁用）、分享、复制、启用/停用、归档 |
| `spProductForm` | `web/src/admin/templates/spProductForm.html`（97 个非空模板行） | registry `297-303`，非导航一级页 | `/admin/service-period-products/new` 与 `?id=<id>` 适配路径 | 周期商品创建/编辑、图片、售卖字段、购买后动作、企微标签原值、外部推送本地配置 |

### 单壳约束

v3 当前 PR10 壳已经注册 `/admin/wechat-pay/products` 和
`/admin/service-period-products`，并由 `internal/webshell/templates/admin_base.html` 输出唯一
`<aside class="admin-sidebar">`。donor `nav.json`/`registry.json` 只提供页面元数据和 inline SVG，
不能把 v2 的完整 shell/sidebar、旧 `.side` 布局或旧 HTML 文档再部署一次。集成时应将四个 raw
template 的内容作为 PR10 壳的页面 body；壳的资源、session 权限门禁和静态托管由 v3 负责。

donor `controller.goto()` 会把页面 key 转成 `*.html`（controller 约 494 行），因此保持 raw
frontend 不变时，Web adapter 必须覆盖这些导航结果（或提供同源、受权限保护的 aliases）：

- `products.html` → `/admin/wechat-pay/products`；
- `productForm.html?id=<id>` → `/admin/wechat-pay/products?view=form&id=<id>`（新建同理）；
- `spProducts.html` → `/admin/service-period-products`；
- `spProductForm.html?id=<id>` → `/admin/service-period-products?view=form&id=<id>`（新建同理）。

canonical URL 的具体 query 形状可以由 adapter 决定，但不能改模板、`goto` 或用户交互顺序。
若选择真实 `.html` alias，alias 仍必须经过 v3 Session/CSRF 权限门禁，不能暴露旧壳。

## 模板与交互逐项清单

### 普通商品列表 `products`

模板是一个带内联样式的列表页面：顶部“客户管理后台 / 交易 / 商品管理”，展示商品总数；表格列为商品
编码、商品名称、价格、状态、已售卖数量、更新时间、操作。每行操作必须原样保留：

- `编辑` → `productForm`，带商品 ID；
- `分享` → donor 当前明确阻断 `no_authoritative_public_purchase_route`，不得伪造支付链接；
- `复制` → 新的本地 draft/disabled 商品；
- `启用`/`停用` → 带当前版本的 CAS lifecycle 写；
- `删除` → 原样确认后，仅允许 donor 约束下未引用 draft 删除。

页面还包含分享 modal：只在后端给出权威地址时显示链接，否则维持 donor 的 blocked 语义；二维码容器
`#shareQrBox`、复制链接和“保存二维码”必须使用 donor `qr.ts`/`util.ts` 行为。

### 普通商品表单 `productForm`

保持五段锚点和重复“保存当前维度”按钮：`#product-sale` 售卖信息、`#product-media` 页面素材、
`#product-action` 购买后动作、`#product-wecom` 企微标签、`#product-push` 外部推送。

原样字段和行为：

- 售卖：`pfName`、`pfPrice`、`pfBuyButtonText`、`pfRequireMobile`、`pfCode`、`pfCurrency`、
  `pfStock`、`pfDescription`；价格换算为 `price_minor`，库存必须是非负整数。
- 页面素材：`pfImageUpload` 接受 `image/*`、UI 限制 2MB；“从素材库选择”读取图片素材，支持
  排序/移除；提交最多 20 条 URL/引用。URL 由 Media adapter 提供，不在 Product Store 跨表读取。
- 购买后动作：`pfLeadChannelId`（隐藏值 + 渠道选择器）、`pfLeadProgramId`、
  `pfCompletionRedirectEnabled`、`pfLeadQrTitle`、`pfLeadQrSubtitle`、
  `pfCompletionRedirectUrl`、`pfCompletionTarget`（JSON object）。
- 企微标签：`pfWecomTagging` 是不解释的 JSON object 原值；这是商品 admin projection，不是客户
  标签绑定或打标调用。
- 外部推送：`pfExternalPushEnabled`、`pfExternalPushReference`；保存只更新本地配置，页面不外呼。

新建普通商品的 donor 默认值是编码/名称/描述空、价格 `0.00`、币种 `CNY`、库存 `0`、生命周期
`draft`、未启用、空图片、空购买后配置、空标签对象和关闭外部推送；编辑页则完整回填服务端值。保存
校验和 projection 组装位于 donor `controller.ts:1780-1815`、回填位于 `3135-3165`。

### 周期商品列表 `spProducts`

表格和普通商品列表保持 donor 结构，标题为“客户管理后台 / 交易 / 周期商品管理”，创建按钮为“创建
周期商品”。行操作必须原样保留：编辑、数据、分享、复制、启用/停用、归档。

`数据` 会进入 donor `spProductData`（registry `369-375`），该页是会员数据/member-grid/customer
展示，**本轮必须整体排除并将该操作保持不可用/blocked**；不得只迁移一个数据按钮。周期商品分享只
接受后端返回的本地公共路径 `/p/service_period/<positive-id>`，并保留 `local_only: true`、
`real_external_call_executed: false`，不能声称已完成支付或 Provider 外呼。

### 周期商品表单 `spProductForm`

保持四段锚点和重复保存按钮：`#sp-sale`、`#sp-media`、`#sp-action`、`#sp-push`。

- 售卖：`spfName`、`spfCode`、`spfPrice`、`spfCurrency`、`spfStock`、`spfDescription`；
- 页面素材：`spfImageUpload`、素材库选择、预览和移除，约束与普通商品相同；
- 购买后动作：`spfBuyButtonText`、`spfRequireMobile`、`spfLeadChannelId`、`spfLeadProgramId`、
  `spfCompletionRedirectEnabled`、`spfLeadQrTitle`、`spfLeadQrSubtitle`、
  `spfCompletionRedirectUrl`、`spfCompletionTarget`、`spfWecomTagging`；
- 外部推送：`spfExternalPushEnabled`、`spfExternalPushReference`，仅保存本地配置。

新建周期商品默认同样为价格 `0.00`、币种 `CNY`、库存 `0`、空图片和关闭外推，projection 初始
status 为 `service_period_draft`；编辑页回填服务端值。保存/回填行为位于 donor
`controller.ts:1780-1815`、`3166-3195`。

列表行行为的冻结证据是 donor `controller.ts:2809-2840`；普通商品分享明确走 blocked toast，
周期商品分享读取 local-only path，周期商品 `data` 行为只能被 PR04 adapter 屏蔽而不能被误接入。

## 24 个冻结 donor 文件（逐文件范围）

以下就是可被 PR04/PR10 构建选择的全部前端文件；没有未列出的商品专属 CSS 或静态二进制。所有
文件均须与 donor SHA-256 manifest 一致：

```text
web/src/admin/controller.ts
web/src/admin/main.ts
web/src/admin/nav.json
web/src/admin/registry.json
web/src/admin/sections/qr.ts
web/src/admin/sections/util.ts
web/src/admin/templates/productForm.html
web/src/admin/templates/products.html
web/src/admin/templates/spProductForm.html
web/src/admin/templates/spProducts.html
web/src/api/admin.ts
web/src/api/generated/health.schemas.ts
web/src/api/generated/p4-commerce-external-push/p4-commerce-external-push.ts
web/src/api/generated/p4-product/p4-product.ts
web/src/api/generated/p4-service-period-products/p4-service-period-products.ts
web/src/api/transport.ts
web/src/shared/api/client.ts
web/src/shared/api/mockData.ts
web/src/shared/api/types.ts
web/src/shared/ui/download.ts
web/src/shared/ui/feedback.ts
web/src/shared/ui/picker.ts
web/src/shared/ui/runtime.ts
web/src/shared/ui/tokens.css
```

依赖说明：

- `controller.ts` 和 `api/admin.ts` 是 v2 的混合总控制器/适配器，不能整文件编译成 v3 的第二套
  shell；Web lane 只能选择上表中的 product/service-product 分支。
- `admin/main.ts` 通过页面 dataset 选择 legacy loader，是 donor 行为证据；v3 只能在自己的
  route/adapter 中加载 raw bundle，不能引入 v2 runtime。
- `tokens.css` 是四个模板的共享视觉依赖；v3 可在单壳中加载同一冻结 CSS，但不得将 donor `.side`
  布局或第二 sidebar 带入产物。
- `qr.ts` 依赖 npm `qrcode-generator@1.5.2`，二维码尺寸、画布和下载行为必须保持；依赖锁由中央
  Integration/Terra lane 统一处理，本审计不改 lock。
- 页面中的 `<img>` 是运行时素材 URL，不是 donor 二进制；图片列表/上传必须通过 PR02 Media Port。
- `nav.json` 中商品/周期商品 inline SVG 分别为 `products`（69-72）和 `spProducts`（75-78）；
  PR10 壳应使用其视觉资产或等价已冻结产物，不重绘图标。

## API URL 与 DTO 合同（24 个 operation）

### 普通商品（9）

| 方法 | donor URL | 请求/响应要点 |
| --- | --- | --- |
| GET | `/api/v1/products` | `cursor` 是不透明 1..512 字符，实际 `limit` 为 1..100；响应 `ProductPage{items,next_cursor?}` |
| POST | `/api/v1/products` | `CreateProductRequest`；201 `Product` |
| GET | `/api/v1/products/{productId}` | 详情 `Product`，ID 为正整数 |
| PUT | `/api/v1/products/{productId}` | `UpdateProductRequest` 含 `expected_version`；200 `Product` |
| POST | `/api/admin/wechat-pay/products/{productId}/enable` | `LocalProductLifecycleVersionRequest`；200 lifecycle product |
| POST | `/api/admin/wechat-pay/products/{productId}/disable` | 同上 |
| POST | `/api/admin/wechat-pay/products/{productId}/copy` | 同上；201 新本地商品 |
| DELETE | `/api/admin/wechat-pay/products/{productId}` | 同上；200 `{ok,deleted,product_id}` |
| GET | `/api/admin/wechat-pay/products/{productId}/share` | `LocalProductLifecycleShare`；普通商品无权威购买路由时 reason 为 `no_authoritative_public_purchase_route` |

### 周期商品（9）

| 方法 | donor URL | 请求/响应要点 |
| --- | --- | --- |
| GET | `/api/admin/service-period-products` | `limit` 1..100、`offset` 0..1000000；`ServicePeriodProductPage{ok,items,total,limit,offset}` |
| POST | `/api/admin/service-period-products` | `ServicePeriodProductCreateRequest`；201 `ServicePeriodProductResponse` |
| GET | `/api/admin/service-period-products/{serviceProductId}` | 详情 response |
| PUT | `/api/admin/service-period-products/{serviceProductId}` | update 含 `expected_version`；200 response |
| DELETE | `/api/admin/service-period-products/{serviceProductId}` | archive；expected version；200 response |
| POST | `/api/admin/service-period-products/{serviceProductId}/enable` | expected version；200 response |
| POST | `/api/admin/service-period-products/{serviceProductId}/disable` | expected version；200 response |
| POST | `/api/admin/service-period-products/{serviceProductId}/copy` | expected version；201 response |
| GET | `/api/admin/service-period-products/{serviceProductId}/share` | `public_path` 必须匹配 `/p/service_period/[1-9][0-9]{0,18}`；local-only |

### 外部推送本地配置/测试（6）

| 方法 | donor URL | 请求/响应要点 |
| --- | --- | --- |
| GET/PUT/POST | `/api/admin/wechat-pay/products/{productId}/external-push`、`.../test` | 配置为 `{enabled,configuration_reference?}`；test 仅产生本地 `CommerceExternalPushTest` |
| GET/PUT/POST | `/api/admin/service-period-products/{serviceProductId}/external-push`、`.../test` | 同上，`product_kind=service_period` |

外部测试 response 的固定安全语义是 `state: accepted|queued`、`provider_accepted:false`、
`delivery_proven:false`、`real_external_call_executed:false`、`auto_retry_allowed:false`；不能由
HTTP 202 冒充执行成功。所有写请求必须保留 v2 transport 的 CSRF 和 Idempotency-Key 语义，由 v3
后端 adapter 接收并转译，不能改前端请求契约。

## DTO 重点

- `Product`：`id`、`product_code`、`name`、`description`、`price_minor`、`currency`、
  `stock_quantity`、`images[]`（最多 20）、`admin_projection`、创建/更新时间、`version`。
- `ProductAdminProjection`：schema version 1、`status`、`enabled`、购买按钮/手机号要求、可空
  `lead_program_id`/`lead_channel_id`、二维码标题、副标题、跳转开关/URL/JSON、opaque
  `wecom_tagging`、最多 20 个 `slices`。
- `ServicePeriodProduct`：`service_product_id` 加同一商品字段，以及 `lifecycle=draft|enabled|disabled|archived`、
  `enabled`、`archived`、`version`。
- 普通商品 lifecycle 为 `draft|disabled|enabled`；每次 update/enable/disable/copy/delete 都带
  `expected_version` 并 CAS。周期商品 archive 是保留历史事实，不做物理删除。
- Product form update 不允许客户端改商品编码；create 才提交 `product_code`。description、名称、
  价格、币种、库存、图片、admin projection 均必须按 donor DTO 字段语义校验。

## 排除项与 adapter 缺口

这些不是“暂时没有 UI”，而是 PR04 明确不能接入的能力：

- `spProductData` 页面及其 member grid、会员数据、成员视图、员工范围、分享设置、客户/权益查询；
- `/api/admin/service-period-history*` 历史定义、entitlements、events；
- `listProductLocalEntitlements` 的数据所有权（普通商品表单的真实读取仍由兼容 adapter 返回
  truthful empty projection，不得改前端或伪造 entitlement）；
- 订单、支付、退款、权益发放、会员、客户、OneID、客户标签绑定/打标；
- 真实企微/微信 Provider 写入和任何声称已交付的外部效果。

集成前必须处理以下具体缺口，且只能改 v3 backend/Web adapter 或 PR10 壳，不得改 donor 前端：

1. donor `readAdminRows()`（`web/src/api/admin.ts` 约 2041 行）会为列表读取商品/周期商品，并为表单
   读取 channels、images、企微 tag directory；v3 adapter 应仅通过 Media/Channel/Tag 的稳定 read
   Port 装配这些数据。
2. donor `readAdminPage()`（约 2106 行）在普通商品表单并行调用 `getProduct`、
   `listProductLocalEntitlements` 和 external-push；v3 不改动这些真实请求，而是由 Product
   兼容 adapter 返回商品详情、本地 external-push 和 truthful empty entitlement projection。
3. donor 周期商品 form/data 分支会触碰成员/历史/member-grid API；周期商品 form 的真实成员元数据
   请求必须由兼容 adapter 返回不含成员明细的 truthful empty/capability projection，商品详情和本地
   external-push 仍按 Product owner 提供；`spProductData` 数据页及其 query/write 分支必须 blocked。
4. donor 商品分享行为不应构造 URL；普通商品沿用 blocked reason，周期商品只返回 local-only
   `/p/service_period/<id>`。
5. image picker/upload 依赖 PR02 Media API；channel picker 需要渠道目录的只读 adapter。任何
   `customers.id`/external identity 都不能被当成商品字段。
6. PR10 canonical path 与 donor `.html` `goto` 不同，必须加 adapter alias/route mapping，并确保
   session、CSRF、管理员权限和审计门禁覆盖 alias。
7. `admin/controller.ts`、`api/admin.ts` 同时包含排除域的分支；不得把整份混合文件直接注册为 v3
   route handler，也不得因此复制 OneID/customer store。

## 合入前验证门禁

在 Terra 完成后端适配和 Root 集成前，Luna donor prep 仅需通过本地冻结检查：

```sh
PR04_DONOR_ROOT=/tmp/aicrm-v2-audit.yN3jmr \
  scripts/check-pr04-donor-manifest.sh
```

最终 PR 还必须在全新 PostgreSQL 16 与上一版本升级库执行 migration，验证管理员登录/CSRF/无权限、
CAS 并发、幂等重放、审计、Product/Media/Channel adapter、普通/周期商品完整 Journey（创建→编辑→
状态操作→刷新持久化→审计可查），以及 1440×900、1280×800 的冻结模板视觉对比。外部 Provider 在
`id-dev` 保持 disabled；任何 Provider 真实外呼、身份错误归属、重复效果或 entitlement/member-grid
泄露都应停止 PR。
