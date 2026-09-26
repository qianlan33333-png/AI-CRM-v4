# 支付宝原付款页设计验收

结果：本次付款蒙版与成功反馈范围 passed。生产验收仍待发布后执行。

## 来源与方法

已确认的父 PRD、现有 CRM 付款页，以及用户“原页灰色蒙版、箭头指向右上角”的明确要求。沿用付款页字体、金额层级与已有完成后动作；仅对蒙版增加局部覆盖样式。使用 Product Design audit / image-to-code / design-qa 流程，不改冻结前端源。

使用实际 Go 模板生成 HTML，通过 127.0.0.1:4186 的合成 API 夹具，在 Codex In-app Browser（Chromium）执行交互与截图。夹具仅提供本地原订单 M-preview、原签名链接格式和可切换的合成付款状态，不访问支付宝或生产客户。

## 页面检查

| 视口 | 检查 | 证据 |
|---|---|---|
| 360×800，dSF 1 | 蒙版覆盖全屏，中文两行可读，按钮完整 | [360px](images/alipay-overlay-360.png) |
| 390×844，dSF 1 | 白色箭头指向右上角、背景灰色半透明、无跳页 | [390px](images/alipay-overlay-390.png) |
| 430×932，dSF 1 | viewport/document/dialog 宽度均为 430，无横向溢出 | [430px](images/alipay-overlay-430.png) |
| 原页成功 | 服务端夹具变为 paid 后自动显示支付完成，无再次点击 | [成功反馈](images/alipay-original-page-paid.png) |

白色箭头采用 Tabler MIT arrow-up-right 原始 SVG，88px，右 16px、顶部 12px 并考虑 safe area。蒙版为 rgba(44,48,54,.86)，引导字 20px / 1.7；触控按钮至少 44px。关闭按钮、复制备用、我已支付保持原订单。键盘 Tab 限制在蒙版内，Escape 可关闭，焦点可见。

## 修正及剩余范围

首次截图中复制备用按钮文字偏长，已缩为“复制链接（备用）”并重新捕获三个尺寸；关闭后恢复原订单，刷新后继续原订单，成功时移除 fragment 和蒙版。单一轮询、前台唤醒、外部 fragment 继续、坏链接拒绝、SDK AES 密文链接原字节继续、redirect / QR / none 由 JSDOM 专项验证；真实 PostgreSQL Composition 旅程验证 return 路由及 WAP/Page 同一订单。

付款回跳页亦在 390×844 实际浏览器验证：服务端已支付显示“已支付 / 支付成功”，无有效订单参数显示返回微信原付款页，不创建订单。[已支付](images/alipay-return-paid.png)、[缺少参数](images/alipay-return-missing.png)。JSDOM 另验证无付款会话、坏参数及后台超过 90 个等待周期后仍可前台继续读取。

没有本次范围内的 P0/P1/P2 页面问题。截图验证为本地合成状态，不代表真实微信菜单行为、Provider 实付或生产已部署。真实原页成功、通知收据、配置跳转仍在生产业务验收清单中。
