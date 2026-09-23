# 支付宝商品结算入口（v4）

## 业务判断与确认范围

用户已确认：配置中心提供支付宝入口；商品页按实际启用的渠道展示微信支付和支付宝；微信内选择支付宝后保留原订单，复制该订单的签名付款链接到系统浏览器；“我已支付”只读取服务端确认的支付结果。付款链接、刷新或确认按钮均不得创建第二张订单或直接发货。

## 公开方案与复用

- 支付宝 [Easy SDK](https://github.com/alipay/alipay-easysdk) 区分手机网站支付 `alipay.trade.wap.pay` 与电脑网站支付 `alipay.trade.page.pay`，由商户服务端生成交易表单。
- [Ping++ HTML5 SDK](https://github.com/PingPlusPlus/pingpp-js) 单独处理微信内的支付宝手机网页支付引导；其跨渠道交互作为参考，不引入该 SDK 或新的支付主系统。
- [smartwalle/alipay](https://github.com/smartwalle/alipay) 展示 WAP 付款 URL 可由用户复制到浏览器打开；仅参考接口语义，继续复用仓内 Payment Adapter。

v4 已有 Config runtime catalog、受保护密钥引用、Payment 的支付宝 WAP/Page Adapter、Order/Payment 幂等与验签回调、公开商品模板和付款会话。扩展这些入口；不新增表、身份匹配、Worker、Provider 调用器或退款路径。

## 规则与边界

- OneID：只读取现有微信 OAuth 付款会话关联的 canonical customer；不解析新身份、不建客、不自动合并。
- Persistence：订单、幂等收据及支付意图继续由现有 Payment/Order PostgreSQL Unit of Work 持有。支付宝发起属于已有 Provider 写入，状态查询属于已有 Provider/Payment 读取；页面不推断支付成功。
- 首次下单前用户可选择启用的渠道。生成付款 checkpoint 后渠道冻结；支付状态未知、响应丢失、链接过期、刷新和返回微信时只能恢复原单。仅服务端明确允许重启的终态才解除 checkpoint。
- 微信内选择支付宝使用 `alipay_wap`；外部浏览器打开签名链接，不重新访问商品页下单。非微信浏览器在已有可信付款会话下使用 `alipay_page`。
- 配置中心显示启用状态和受保护配置是否存在，不能显示或写入明文私钥。发布开关需经过现有 runtime release、运行角色读取与部署配置校验。

## 验收与发布

1. 配置中心可进入支付宝详情并操作原有运行态草稿；密钥只显示安全引用。
2. 渠道关闭时商品页不提供该选项；启用时使用所选 `provider/channel` 创建唯一订单，旧微信支付旅程保持可用。
3. 微信内支付宝使用原单签名 URL 并展示复制/系统浏览器指引；“我已支付”只查询原单的服务端状态，只有 `paid` 才显示完成动作。
4. 覆盖响应丢失、刷新、状态未知、已支付、已存在订单和回调重放；不得跨渠道静默切换。
5. 本地 fast、compile、Payment/Product/Config 专项与真实浏览器旅程；预发布使用虚拟支付验证同一包、认证读回与状态转换。真实商户配置、实付/退款和回调读回为独立生产业务验收，未完成时保持 observing。

回滚使用指挥台上一份已验收包；保持订单及 Provider 收据，不重放未知支付效果。与当前 v4 `main`、共享 Composition 和仍在 observing 的上一批生产候选核对后才交接。指挥台负责串行晋级。
