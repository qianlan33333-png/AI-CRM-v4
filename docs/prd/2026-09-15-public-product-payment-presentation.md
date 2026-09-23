# 公开普通／周期商品与支付页统一呈现（第一批）

## 目标与范围

把现有公开入口统一成可辨认的商品详情、支付准备、支付恢复和已购／周期权益呈现，不改变它们的业务命令或身份语义：

| 路由 | Owner 事实 | 第一批呈现目标 |
| --- | --- | --- |
| `/p/{product_code}` | 已启用普通商品、图片和可验证推广上下文 | 商品详情、价格、固定购买栏、图片加载失败不遮挡商品信息 |
| `/pay/{product_code}` | 付款会话、优惠券、手机号、原订单恢复 | 金额层级、支付准备／恢复／未知结果的可理解状态，现有恢复按钮与错误文字仍可用 |
| `/s/{product_code}` | 周期商品与可信会话下的权益状态 | 服务期、可续费／已生效／已到期及二维码引导的事实呈现 |
| `/s/{product_code}/pay` | 周期商品的既有支付契约 | 与普通支付页相同的金额、按钮、恢复和辅助说明层级 |

首个 PR 是一个可观察能力：**四个入口挂载同一 V3-owned public-commerce 视觉／状态层**。它只从当前页面已呈现的受权事实推导视觉状态，不发新请求、不生成订单、不保存表单、不上传素材，也不调用支付或企微 Provider。

本批不包含问卷、分销公开页、企微侧边栏、商品管理后台、真实支付交易、支付回调、退款、推广归因规则或移动后台壳改造。

## 开发前分类

```text
OneID: involved via the existing Payment trusted session and H5 OAuth binding;
       public presentation must not resolve, create, merge or expose an identity.
Persistence: public presentation is stateless. Existing checkout creation,
             coupon reservation, order/payment receipt, and original-order recovery
             remain inside their Owners and existing PostgreSQL transaction boundary.
External Effects: presentation adds none. Existing payment handoff / Provider
                  lifecycle remains untouched; outcome_unknown cannot issue a new order.
```

`promotion_context` 是既有签名／验证后的上下文，V3 不重新解析、拼接或回退到 cookie。订单恢复继续使用同一 sessionStorage checkpoint、付款会话 binding、幂等键和原商户订单号；`session_mismatch`、`response_lost`、`purchase_pending`、`outcome_unknown` 和最终失败均只呈现已有事实，不能通过新 key 或“重新下单”绕开。

## 现有真实链路与复用边界

| 链路 | 现有入口 | 第一批复用／约束 |
| --- | --- | --- |
| 普通公开商品 | `internal/product/http/public.go` 的 `PublicHandler` | 仅已启用商品；代码路由和旧数字别名语义不变；图片仍由 Product／Media 授权读取。 |
| 周期公开商品 | `internal/product/http/service_period_public.go` → `service_period_template.go` | 权益来自现有 trusted session／Order entitlement 读取；冻结 Python renderer 本体和 digest 不改，只在其输出外层装配 V3 展示资源。 |
| 支付与恢复 | `internal/payment/http` 的既有 API、`cmd/aicrm/public_checkout_journey_test.go`、`cmd/aicrm/public_checkout_recovery_postgres_integration_test.go` | 不改 POST、回调、支付 handoff、优惠券选择、手机号规则或 original-order checkpoint。 |
| 构建资源 | `scripts/build-v3-host-adapters.mjs`、运行时 `web/dist` manifest | 新 `publicCommerceStyles`／`publicCommerceHost` 必须由 manifest 指定；Composition 只绑定该明确 release 目录，首次公开 HTML／资源请求验证完整闭包。缺资源时明确失败，绝不静默降级。 |

普通页和周期页不借用 `admin_base`、后台 token 选择器、后台导航、选择器或任一 frozen donor 壳。新样式仅作用于 `data-v3-public-commerce` 根节点和明确的公开页面 data 属性。

## 交互与状态合同

1. **身份门**：未在微信、OAuth 缺失或会话读取失败，保留既有“在微信打开／重新授权”事实和链接；视觉层不得自动重定向、重试 OAuth 或展示客户信息。
2. **商品详情**：名称、描述、保存的图片、价格和服务周期按当前 Owner 投影显示。缺图、媒体读取失败、不可购买和 404 各自保留已有路径；不以演示图片、旧价格或另一商品补全。
3. **支付准备**：优惠券、手机号、付款方式、实付金额和主按钮只改变版式、焦点、禁用和加载呈现。优惠券／手机号校验仍由既有脚本和 Owner 处理。
4. **支付恢复**：有同一付款会话绑定的原订单时，明确“恢复原订单”；会话变化、未知结果、已停止、已支付和最终失败分别呈现既有事实。未知与失败不显示成功，不清除 checkpoint，不创建新订单。
5. **周期权益**：未开通、不可用、有效、到期四种 Owner 状态保留不同的 CTA／说明；二维码缺失显示“未提供”，不伪造可扫码或发送成功。
6. **无障碍与窄屏**：375、390、430px 时固定底栏不遮挡主要内容和安全区，按钮至少 48px、金额等宽数字、图片失败仍有文字替代、可见焦点与 `aria-live` 状态不被 V3 覆盖。1280／1440 仅校验公开页在桌面浏览器中的最大内容宽度与不溢出；不把它称为后台移动壳验收。

## V3 装配设计

- 增加 V3 public-commerce CSS／module entry；CSS 提供白底、浅灰页面、蓝色主操作、金额与语义反馈 token，并保持现有 `#status` 文本和 `aria-live`。
- module 只给公开页面根添加 scoped data 属性，并依据既有 DOM 可观察事实标注中性／准备中／恢复中／完成／需要处理的视觉状态；不调用任何 HTTP API，也不替换 Owner 文本。
- 普通模板使用 manifest 派发的 V3 assets。周期页经输出包装在 `</head>` 前插入同一资源；不修改 `frozenServicePeriodPublicRenderer` 及其 hash。
- 资源 resolver 由 Composition Root 绑定一个明确的 `web/dist` release 目录并显式注入两个 public handler。它与现有 UIBinding 一样惰性验证：worker、无 UI 的组合和非 UI fixture 不因当前 cwd 缺资源而失败；首次公开 HTML 或 public asset 请求会验证 manifest entry／文件／hash，失败返回明确不可用状态，不使用 fallback 文件名或 donor 页面。`stage-new-shell-ui` 必须保留两个入口及其静态 import closure；最终启动只读取 staged release manifest，不能以未 stage 的 `web/dist` 作为通过证据。
- 公共资源仅限 manifest 声明的同源 CSS、Host module 和 Host 静态 chunks，以 `/product-public-assets/` 匿名提供。每个文件在启动时和响应时都核对 release hash；无关 `/assets`、未声明文件、路径穿越和被改写资产一律 404。

## 参考与不采用项

- 采用 [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) 的语义 token、单一主要操作、显式 loading／error／disabled 反馈、紧凑金额信息层级；不引入 Ant Design、React 或运行时依赖。
- 参考 [Shopify Polaris checkout extension guidance](https://github.com/Shopify/Shopify-AI-Toolkit/blob/main/skills/shopify-polaris-checkout-extensions/SKILL.md) 的 checkout error handling 分层。不会照搬其组件、SDK 或 checkout data model。
- 不采用 Stripe／Shopify 的支付调用逻辑：本仓已有 Payment Owner、会话绑定、订单恢复、幂等和 Provider 回调边界。

## 验收与阶段

1. **资源与路由合同**：build → stage → actual Composition 依次验证四个入口都带同一 manifest V3 assets；匿名访问 CSS、Host 和静态 chunk 都为 200，后台 `/assets` 不因本能力开放；冻结服务周期 renderer digest 不变；禁用／草稿／非法路由仍 404。
2. **真实浏览器页面**：本地 PostgreSQL + Chromium 验证普通详情与支付、周期详情与支付和周期不可用的 Owner disabled 状态；375／390／430 组件布局并保存截图。无可信付款会话时截图展示既有身份门，不能把它标作商品已购或支付成功。1280／1440 是后续完整公开页资料状态验收，不冒充后台移动壳。
3. **支付安全回归**：复用既有 public checkout journey 和 PostgreSQL response-loss／renewed-session recovery；验证 UI 只重现原订单，Provider 不启用、无真实交易。
4. **构建与冻结门禁**：Node unit、TypeScript、manifest／runtime-asset contract、受影响 Go tests、build 和 canonical consumer；最终 PR CI 是独立门禁。

## 本 PR 验收记录

- 首轮 PostgreSQL／Chromium 仅证明四个 route mount、asset closure 和身份门存在；它没有取得可信 H5 会话，因此不能作为商品或支付内容验收。
- 后续浏览器 fixture 仅在测试数据库中，通过已组合的 `paymentsession.Service` 为 provider-verified 的合成 OneID 事实签发既有 trusted H5 session，并以 HttpOnly cookie 访问页面。它不新增签发 HTTP endpoint，不点击支付，不发 OAuth 或 Provider 请求。
- 授权后的 Chromium 路径验证普通商品详情（含 Product-owned 合成长图、真实页面可滚动且底端位于固定购买栏上方）、普通支付、可购买周期商品与周期支付的真实名称、价格、优惠券、实付和微信支付 DOM；不可用周期商品保持 Owner disabled 操作与中性状态点。无会话的非微信路径仍断言“请在微信中打开”。
- 周期不可用页曾因 Payment session lookup 所在 UoW 被传给 Order entitlement read 而触发 nested transaction，返回 503。修复为两个顺序的只读 Owner 调用：有效会话无权益、有效会话有权益和过期会话三种 PostgreSQL 覆盖均保持受控不可用页；过期会话不会复用 actor 或暴露权益。
- 最新本地证据：`/tmp/aicrm-public-commerce-authorized-final-wip4-20260915.log`（真实 PostgreSQL + Chromium）与同目录七张 375／390／430 截图（包括长图底端 `public-standard-detail-375-bottom.png`）；`/tmp/aicrm-public-presentation-runtime-release-wip-20260915.log`（Runtime Release Chromium）。这些是本地候选证据，最终 PR CI 仍是独立门禁。

后续 PR 才接入问卷／分销公开页、企微侧边栏及后台公共壳；不能把本批四条公开商品路由的验证泛化为它们已完成。
