# 订单中心：微信卡包查看与原订单继续支付

本次复用用户订单中心与电子发票父 PRD。用户授权新增待付款继续支付，并要求已出票的查看按钮直接打开微信卡包票据；单位抬头、一单一票、交易单号申请编号保持现有合同。

```mermaid
flowchart TD
 A[我的订单] --> B[已出票：查看发票]
 B --> C[会话与 OneID 归属核验]
 C --> D[官方查询核验发票、接收人、AppID、插卡状态]
 D --> E[微信 JS-SDK openCard 打开对应发票]
 A --> F[待付款：继续支付]
 F --> G[本人付款身份、原订单与 Payment 事实核验]
 G --> H[原商品支付页恢复原支付编号和冻结金额]
 H --> I{原支付结果}
 I -->|可支付| J[用户确认唤起原微信或支付宝收银台]
 I -->|已支付| K[现有支付完成页]
 I -->|过期或未知| L[明确提示，保留原订单，不创建新订单]
```

## 复用与边界

- [微信官方发票查询](https://pay.wechatpay.cn/doc/v3/partner/4015792567) 已提供 card_appid/card_openid/card_id/card_code；沿用官方 Go SDK 的签名验签与现有公众号 JS-SDK 签名接口。
- [微信官方 JS-SDK](https://developers.weixin.qq.com/doc/offiaccount/OA_Web_Apps/JS-SDK.html) 的 openCard 负责原生卡券展示；[wechat-jssdk](https://github.com/JasonBoy/wechat-jssdk) 的卡券接口仅作参考，不增加第二套客户端。
- 继续支付复用 V4 商品/服务周期支付页、Payment.GetCheckout 的可信会话和支付通道校验、原 handoff 与现有结果查询。不会以当前商品价格重新下单。
- 涉及 OneID/外部身份读取；Payment/Order/Invoice 均通过稳定 Port 提供本领域事实。新增行为是 Provider 查询和浏览器收银台唤起，沿用现有支付外部效果，不新增队列、状态机或持久表。前向迁移仅扩展既有 OAuth return_path 白名单，支持严格的原订单支付页参数；保留全部既有行与约束。

## 合同与页面

`GET /api/h5/me/invoices/{id}/card` 先核验本人申请，再通过认证官方查询匹配发票 ID、蓝票号码、金额、销售方、公众号及当前微信接收人。仅已出票且已插卡可返回 openCard 所需的 cardId/code；响应不缓存，不返回 OpenID。查询结束再次检查申请未冲红。错误不跳外部 PDF 接口。

订单查询增加本人待付款原支付入口。`GET /api/h5/me/orders/{id}/payment` 返回已授权原 Payment 编号、通道、冻结金额及原商品标识；不返回 Provider 写权限。原支付页使用 `resume_order` 恢复，读取或点击重试均不会落入新订单创建分支。受益人、导入记录、订单与支付金额/编号不一致、非原付款身份或已关闭记录拒绝恢复；过期或结果未知显示原状态，不盲目重开。

Product Design 以用户本轮图二的蓝色卡片和现有订单页为选定目标。已出票按钮保留“查看发票”，卡包未送达时显示交付状态；待付款卡片增加“继续支付”。不增加个人抬头、出行信息、邮箱或新的文件格式。

## 验证与交付

验证本人/他人/合并归属、卡包接收人/AppID 不匹配、未插卡/冲红、JS-SDK 失败；原支付恢复的过期/未知/已支付、防重复下单、原金额、普通及服务周期、微信及支付宝；在实际挂载页面检查 320/390 移动端布局和按钮交互。真实手机卡包跳转和真实付款另记结果。

生产只读证据：2026-10-09 当前 ¥99 官方查询已出票、INSERTED，card_id/card_code 存在且 AppID 匹配；外部浏览器 session_required 符合 Cookie 隔离。候选推送国内准确 ref 后交唯一发布工作台，安装与真实 Provider 结果分别记录。回退本次应用构件；新增 OAuth 白名单保留，旧构件不使用新入口，不删除已有授权记录。生产迁移前由工作台按既有流程备份。
