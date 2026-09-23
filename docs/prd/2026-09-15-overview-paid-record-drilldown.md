# 首页已确认支付同口径明细抽屉

## 目标与范围

经营首页的“已确认支付”金额、订单数和趋势已经以 Payment 的
`paid_confirmed_at` 为准；运营人员还不能在不改变统计口径的前提下查看组成记录。本 PR 增加一个只读明细页和首页抽屉：点击“已确认支付”的“查看明细”后，查看该首页响应所代表的北京统计区间内、能进入付款金额分母的支付记录。

本抽屉只解释两个指标：**已确认支付金额**和**支付订单数**。`payments.order_id` 的唯一约束使每条明细恰好对应这两个分母的一笔业务订单。它不解释“支付客户”数：该人数是当前 canonical Customer root 的去重计数，需 Identity Port 的独立名单/归并语义，订单行里的历史付款时客户不能冒充这一口径。客户、退款、净收款、分销和趋势下钻保留为后续独立能力。

本 PR 不把 `/admin/orders` 现有 `created_from/created_to` 筛选当作支付时间下钻，也不把 `ExternalReadQuery.PaidAt` 当作替代：后者仅接受 native immutable verified Order paid event，不能覆盖可信历史付款确认事实。抽屉只让订单沿既有 provider-scoped 详情协议跳转；历史付款客户不生成客户链接，更不会把订单列表的创建时间筛选伪装为当前统计区间。

这是一项只读可观察能力：

- **OneID/客户身份：只读涉及。** 明细复用 Payment 已保存的历史 canonical `payer_customer_id` 事实，并明确标作付款时客户；不调用 Identity `Resolve`、Provision 或 merge，不改写历史事实，也不把它冒充为当前 canonical root。
- **持久化、内部任务：不涉及。** 只执行 Payment Owner 的 PostgreSQL `SELECT`，没有写入、审计、Outbox 或 job。
- **Provider/外部效果：不涉及。** 端点不发起 Provider 读取或写入。

前端沿用 `web/v3/overviewAdmin.ts`、`web/v3/overview.css` 和共享
`openDetailDrawer`。不新建页面壳、弹窗或另一套 token。现有 Product Design 路由中首页管理端为本次唯一 surface；桌面和后台窄屏只验证该抽屉本身，不把后台窄屏当作企微侧栏验收。

## 已核实的事实与参考

1. `payments.order_id` 有数据库 `UNIQUE` 约束（`migrations/0021_payment.sql`）。因此一条 Payment 记录恰好对应一个业务订单：明细每行都对应 `ReadPaidOverview` 的一个已确认订单和其金额分母，不需要页面层去重。
2. `internal/payment/store/overview.go` 当前事实谓词是
   `status='paid' AND paid_confirmed_at IS NOT NULL AND paid_confirmed_at >= start AND paid_confirmed_at < end`。它包含带可信原始确认时间的历史 Payment，半开区间和现有趋势/总额使用同一个谓词。
3. `paid_confirmed_at` 是独立保存的付款确认事实；`updated_at` 会受后续退款/对账影响，不能用于本次筛选或展示的确认时间。
4. 现有 `openDetailDrawer` 已提供可访问的关闭、Escape 和 opener 焦点回归；明细加载只更新 drawer body，不能重建首页根节点而破坏焦点回归。
5. GitHub code search 对本仓 `ReadPaidOverview` 和 `paid-records` 未发现可复用的已发布明细 Port 或 API。复用路径是既有 Payment overview Read Port、overview Handler 授权与 shared drawer，而不是复制 Order list。

## 读取、筛选与分页合同

新增 Payment 的稳定只读 contract，供 Overview app 依赖：

```go
type PaidOverviewRecord struct {
    Provider        string
    OrderReference  string
    PayerCustomerID *int64
    AmountMinor     int64
    Currency        string
    PaidConfirmedAt time.Time
}

type PaidOverviewRecordPage struct {
    Items      []PaidOverviewRecord
    NextCursor string
}

type PaidOverviewRecordQuery struct {
    Window OverviewWindow
    Cursor string
}
```

Payment Store 只读取 `payments`，返回固定 25 条的 keyset page，按
`paid_confirmed_at DESC, id DESC` 排序。查询和首页汇总使用完全相同的付款谓词；`id` 只用作同一确认时间的稳定次序，绝不作为付款时间。Payment 不 join Order、Customer 或 Identity 表，也不返回 Provider transaction reference、identity 或电话等敏感值。

第一页通过：

```
GET /api/admin/overview/paid-records?period=today|7d|30d
GET /api/admin/overview/paid-records?period=custom&from=YYYY-MM-DD&to=YYYY-MM-DD
```

后续页只带 `cursor`。cursor 为不透明编码，包含 version、首次解析出的 UTC
`[start,end)`、最后一行的 `paid_confirmed_at` 和 Payment `id`。服务端校验其完整形状和范围；畸形 cursor、同时带 period 和 cursor、重复 query key 或超出限制的输入一律 `400 invalid_request`。后续页直接使用 cursor 冻结的窗口，故北京午夜后“近 7 天”不会静默变成另一个区间。响应包含：

```json
{
  "range": {"period":"7d","timezone":"Asia/Shanghai","start":"...","end":"..."},
  "items": [{"provider":"wechat_pay","order_reference":"...","payer_customer_id":123,"amount_minor":100,"currency":"CNY","paid_confirmed_at":"..."}],
  "next_cursor":"..."
}
```

跨 HTTP 页不虚构长期事务快照：每个请求读取当前已提交的 Payment 事实，keyset 只保证静态结果中的无重复、稳定翻页顺序。cursor 锁定的是统计窗口而不是数据库快照；已经在首屏之后发生的支付状态变化可能在下一次重新打开抽屉时体现。端点不会把这种并发读写误称为一个跨请求 RR snapshot。

端点复用 overview Handler 的现有 Access authenticate 和 `canReadAdmin` 全局 admin-read scope。未认证 `401`、无权限 `403`、Payment read 不可用/超时 `503`；任何 Owner 失败都不返回部分记录并宣称成功。

## 管理端交互

1. 当当前首页 Payment 区段为 `ready`、`zero` 或 `data_missing`，且首页响应与当前选择的 period 一致时，指标提供明确的“查看支付记录”按钮。区段 `failed`、过期或读取中的旧首页不能打开可能错口径的抽屉。
2. Drawer 标题为“支付记录”，显示首页响应中的实际北京日期范围，并说明“以系统确认的支付时间为准”。`data_missing` 时同时保留原首页的缺确认时间提示；没有确认时间的 Payment 不会伪造进入明细。
3. 每行显示支付来源、订单参考、金额和币种、北京时间确认时间。订单链接复用已验收的 provider-scoped 详情协议：Payment `wechat_pay` 映射为 URL provider `wechat`，`wechat_shop` 保持 `wechat_shop`，并生成 `/admin/orderDetail.html?id=...&provider=...`。目标 Host 已通过其 Order Owner Port 将 provider 带入 `/api/admin/orders/{reference}?provider=...`，因此 merchant reference 跨 Provider 时不会误读另一笔订单；本 PR 的 Chromium 验收必须真实点击该链接并验证目标 API query 与页面事实。`payer_customer_id` 有值时仅显示“付款时客户 #ID”，无值显示“付款时客户待核实”。它是 Payment 历史事实，页面不链接成或声称为当前 canonical Customer，也不暴露外部身份或敏感字段。
4. 首次加载、空结果、HTTP/契约失败和“加载更多”失败均在 drawer 内显示。加载更多失败保留已加载行和原 cursor，提供同一 cursor 的重试；不以空数组或 `¥0` 冒充未知。请求 generation 只让当前 drawer 和当前页 cursor 的结果落入 DOM；关闭 drawer 或切换首页 period 后的迟到响应不得污染正在看的范围。
5. 新按钮在 drawer 打开期间保持在首页 DOM 中；关闭后由 shared drawer 将焦点返回该按钮。响应字段经过严格类型校验并 HTML 转义；订单参考不会成为未转义 HTML。

## 实现边界

- `internal/payment/port` 定义 records page；`internal/payment/store` 实现同口径、固定长度的 keyset select；现有 `paymentapp.OverviewReader` 在现有只读 RR UoW 中调用它。Overview app 只经该 Port 协调；HTTP 不直接访问 Payment store。
- `internal/overview/http` 加受现有授权保护的 records 路由，复用既有北京 period 解析，新增严格的 cursor-only 分支。
- `api/openapi.yaml` 描述 endpoint、范围、page、受限 provider enum 和 nullable `payer_customer_id`，明确只读、无 Provider 调用和 `paid_confirmed_at` 口径。再生成受管理的 API source metadata 并做 P5/source authority 验证。
- `web/v3/overviewAdmin.ts` 和已有 CSS 只为既有指标增加按钮与抽屉记录布局；不修改冻结 donor、Order 查询契约、Identity 合并、Payment 业务状态或 Provider 规则。

## 验收

1. Payment Store / PostgreSQL：native 与历史的可信 `paid_confirmed_at`、区间开始包含/结束排除、`paid_confirmed_at=NULL` 排除、确认时间相同的 `id` keyset、25 条边界、空页、无重复/遗漏、nullable payer 和多币种金额均覆盖；同一 merchant reference 跨 provider 的行都保留其可信 provider；验证唯一 `order_id` 明细行与 overview gross/order 分母一致。
2. Payment app / Overview app：records 读取经过稳定 Port 与只读 UoW；Store 返回异常、取消和 deadline 不产生部分成功 page。
3. HTTP：管理员成功响应、401/403、period 形状、cursor 形状/窗口固定、cursor 和 period 混用、无效/重复参数、503 均覆盖；OpenAPI 与实际 JSON 的 nullable payer 合同一致。
4. 前端单元与 Chromium：点击按钮打开正确北京时间范围；确认时间、CNY 金额、付款时客户和 payer-null 文案准确；点击每个 Payment provider 映射出的既有 detail URL 后，目标 Order Host 实际请求同一 provider/reference 并显示该行事实；“支付客户”指标没有借订单行开启错误下钻；加载更多失败后重试保留首屏；快速切换统计区间或关闭 drawer 后迟到响应不覆盖当前内容；Escape/关闭后焦点回到仍在的“查看明细”按钮。
5. 以独立 PostgreSQL DB 做完整 composition 和 Chrome 验收，分别捕获 1280/1440 drawer、失败恢复以及后台 360/420 抽屉截图。截图只证明首页管理端和该组件；发布前再跑完整 stage、manifest/asset closure 和 ASCII consumer。

## 不在本 PR

- 首页的支付客户、退款、客户、分销或趋势表下钻。支付客户在后续独立 PR 中应通过 Identity Port 的 canonical root 名单解释，不能复用本 records page。
- 将 Order 创建时间、外部订单 native paid event 或 `updated_at` 作为支付确认时间。
- 新的跨领域汇总表、Identity 解析/写入、Order/Customer 表 join、Provider 读取/写入、任务或审计写入。
- 跨 HTTP 页宣称 Repeatable Read 长期快照，或把公共/后台窄屏验收称为企微侧栏验收。
