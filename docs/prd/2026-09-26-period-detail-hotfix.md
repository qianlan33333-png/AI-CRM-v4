# 周期商品详情页热修缺陷合同

用户已明确授权线上排查、热修及恢复既有设计（2026-09-26）。

```mermaid
flowchart TD
  A[访问 /s/商品代码] --> B{商品可展示}
  B -->|否| C[既有未开放状态页]
  B -->|是| D[周期详情页：素材、价格、有效期]
  D --> E{有效 Payment 会话}
  E -->|有| F[Order Port 读取当前客户报名与剩余天数]
  E -->|无或失效| G[公共商品信息，不显示他人权益]
  F --> H[立即报名 / 续费 / 重新开通]
  G --> H
  H -->|用户点击| I[/s/商品代码/pay 既有授权付款流程]
```

## 生产失败证据与根因

生产 active SHA `960b30e9406fae2045aeb7ef5dce863976407727`，readyz=ready。
`ces` 产品 ID 12/version 14：images 绑定 1076、1077、1078，slices=[]；三个 JPEG 启用且 blob 完整。
公开 /s/ces 返回付款模板且无 img。应用只将 slices 投影为 DetailMedia；已上架详情入口复用通用付款/普通详情模板，丢失周期状态页。

## 修复与复用

复用当前仓库 service_period_template.go 的既有周期状态页、Payment SessionReader、Order EntitlementService、Media ImageVariantReader。
在 Product 应用层补充严格解析的 images 素材绑定，保留既有 slices 顺序并去重。HTTP 媒体接口继续核验该产品的绑定与可售状态，禁止公开任意素材。
详情入口固定渲染周期状态页，图片为空也不变成付款页；仅 /pay 进入付款模板。分销 promotion_context 必须保留到付款链接。
Product Design audit：当前生产截图确认只显示付款身份门禁；复用已存在周期页面为视觉目标。artifact-template-crm 已读取，H5 使用现有独立壳。

## 分类

OneID：只通过 Payment 不透明会话读取 canonical customer；不解析、不建客、不合并。
Persistence：Product/Media/Order 只读；无新持久状态、迁移、任务或 Provider 写入。
External Effects：不涉及新增外部效果；浏览详情不创建订单。付款路径复用原合同。

## 参考

Stripe Payment Links 的明确付款入口：https://docs.stripe.com/api/payment-link
Medusa storefront 明确分开 Product Detail 与 Checkout：https://github.com/medusajs/nextjs-starter-medusa
以仓内既有周期状态页为行为与视觉依据；不引入第三方框架。

## 验收与发布

复现 images-only/slices-empty 失败；验证三图顺序、无绑定素材 404、详情与付款分离、匿名/失效会话不泄漏权益、active 剩余天数及上海到期日期、expired 重新开通、分销上下文。
执行 Product 应用/HTTP 专项测试、真实 Host Chromium 旅程、fast/compile。
使用当前 V4 准确 main 新 PR；候选必须按切换后的国内 main 更新，预发安装和验收同包，再由发布指挥台串行晋级。
生产读回 /s/ces 三图内容与状态页面、/pay 授权门禁、active SHA/readyz；认证状态测试使用既有授权会话或合成预发夹具。
回滚到前一已验收包；不修改生产业务记录。健康与匿名读回不能冒称真实微信认证旅程完成。
