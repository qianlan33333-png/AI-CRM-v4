# 分销推广页支付恢复检查点修复

## 目标与判断

通过有效的分销链接进入普通商品或周期商品支付页时，浏览器必须先持久化同一笔支付的恢复检查点，再接受任何新的支付创建。当前支付页在校验检查点时要求 `promotion_context` 与页面中的受控推广凭证完全一致，但首次点击创建的 payload 遗漏该字段。`checkoutKey` 因此返回空值，页面错误地把校验失败显示为“无法保存本次订单恢复信息”。问题发生在网络请求之前：不是浏览器存储容量、微信身份、令牌长度或支付 Provider 的失败。

修复只让首次检查点携带页面已验证的 opaque `promotion_context`。恢复、刷新和重复点击继续复用相同的 idempotency key、付款会话绑定与推广凭证；同一商品在同一 tab 先打开 A 分享链接、再打开 B 分享链接或普通链接时，必须恢复 A 的冻结检查点，不能以当前 URL 覆盖它。存储确实不可用时仍在创建订单前停止。不得补造、替换或清除已有检查点，也不得修改佣金、归因、支付订单或任何生产数据。

## 架构分类

- **OneID：读取既有可信付款会话。** 本修复不解析、建档、合并或修改身份；H5 OAuth 和 Payment 仍是身份事实 Owner。
- **持久化：浏览器恢复检查点与既有 Payment/Order PostgreSQL 事务。** 前端只保存当前订单的本地恢复载荷；Payment 的订单、幂等收据、归因与审计边界不变。
- **外部效果：既有 Payment 外部写。** 新支付仍只能在恢复检查点已成功写入后由 Payment 接受；本 PR 不增加 Provider 调用、队列、Worker、重试或对账逻辑。

## GitHub 参考与采用结论

- [WHATWG Storage Standard](https://github.com/whatwg/storage)：浏览器 Storage 操作可能抛出异常；沿用当前“写入失败即在支付创建前停止”的策略，不吸收外部代码或依赖。
- [Stripe accept-a-payment samples](https://github.com/stripe-samples/accept-a-payment)：借鉴支付 UI 只能提交已冻结的付款意图这一边界；本仓继续使用自己的 Payment、会话绑定和 idempotency 合同，不引入 Stripe SDK 或改变支付 Provider。

## 前端装配与验收

| 终端／入口 | 现有组件或入口 | 本次变更 | 验收 |
| --- | --- | --- | --- |
| 微信 H5 公开支付页 | `internal/product/http/public.go` 的 canonical `/pay/:code` 与 `/s/:code/pay` inline checkout runtime | 仅完善由公开路由受控传递、并由 Payment/Order 最终核验的推广凭证进入初始 checkpoint payload | 普通商品采用真实 H5/WebView 风格浏览器回归；周期商品复用同一受控 checkout runtime，并由 service-period 路由合同验证；Payment HTTP 回读确认归因字段不变 |

Product Design 路由：已读取 `product-design:index` 与 audit 指引；这是已有支付页的错误修复，不进行视觉探索或重设计。前端基线和复用路径来自 `aicrm-v3-frontend-consistency` 的 H5／公共页入口约束。

## 验收标准

1. 有效推广凭证下，首次点击能写入含同一 `promotion_context` 的检查点并只发起一次带该值的 checkout 创建。
2. 普通商品的真实 H5/WebView 风格浏览器覆盖首次点击、reload、响应丢失、重复点击和 A→B／A→普通；JSDOM 与实际 Payment HTTP 回归覆盖普通→A。周期商品复用同一 checkout runtime，并由 service-period 路由合同验证。所有恢复场景复用原 key、绑定和归因，不能新建第二笔订单。
3. 非推广购买保持空的 `promotion_context`；伪造、跨商品或结构错误值仍由现有校验拒绝。
4. 实际 browser Storage 抛错时，不发送 checkout 创建；已有恢复记录、会话变更和终态读取保持既有 fail-closed 行为。
5. 真实 H5/WebView 风格浏览器 Journey 验证，不记录或复制手机号、openid、cookie、推广令牌或生产订单资料。
