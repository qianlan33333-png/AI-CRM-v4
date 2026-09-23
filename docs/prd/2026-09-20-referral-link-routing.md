# 裂变活动链接路由调整 PRD

状态：实现前冻结（2026-09-20）

## 业务判断

1. 付费分销活动的“邀请好友”必须复制活动绑定商品的公开购买链接，不能复制普通裂变活动分享链接；免费报名活动继续复制现有活动邀请链接。
2. 一级活动列表中的每张活动卡都提供活动专属链接复制入口，链接固定为 `/referral?campaign={id}`，不再要求用户进入通用活动列表后再定位活动。
3. 绑定商品链接来自 Product Owner 的 `ProductTargetReader`，Referral 不读 Product 表、不拼接商品编码，也不让浏览器提交商品标识。
4. 本次只调整可复制入口，不新增身份、分销佣金或支付状态机；现有 `product-context` 与支付 Cookie 机制保持不变，直接复制商品 URL 不额外携带普通邀请码。

## 参考与复用

- GitHub deep-link 参考：AppsFlyer OneLink 的 campaign/deep-link 参数约定（https://github.com/tokenize-x/appsflyer-docs/blob/main/appsflyer-onelink-dashboard-setup.md）。
- GitHub referral 参考：Usher Referrals 的 campaign 对应可分享邀请入口（https://github.com/usherlabs/usher-referrals）。
- 复用当前 `referralCenter.ts` 的活动卡、邀请对话框、`copy()` 与 `referralAdmin` 的活动 API；不新增页面壳或选择器。

## 接口与数据边界

- Public Campaign DTO 增加 `activity_url`；付费活动增加 Product-owned `product_url`。
- Product URL 仅允许站内 `/p/{code}` 或 `/s/{code}`，由后端 Product Port 投影提供。
- 前端根据 `qualification_mode` 选择邀请复制目标：`product_purchase` 使用 `product_url`，其它使用现有 `/referral/invite/{token}`。
- 活动列表卡增加“复制活动链接”按钮；复制失败时保留当前通用浏览器降级提示。

## 分类

- OneID：不涉及；本次只读取公开活动与商品投影，不解析或建立客户身份。
- Persistence：读取现有 Campaign/Product 投影；复制动作 stateless，不新增业务写入。
- External Effects：不涉及；剪贴板写入是浏览器本地 UI 效果，不调用 Provider。

## 成功标准与回滚

- 免费活动邀请仍请求并复制 opaque invitation URL。
- 付费活动邀请复制绑定商品公开 URL，且不出现普通邀请 URL。
- 一级列表每张卡可复制其唯一活动 URL；活动详情与列表 URL 一致。
- Product 读取失败时不生成伪造链接，展示明确失败提示。
- 回滚只需恢复前端与 Referral DTO 变更；无迁移、无配置、无数据回滚。

## 验证

- `referralCenter.test.mjs` 覆盖免费/付费邀请目标、列表活动链接复制和异常无伪造链接。
- Referral HTTP 单测覆盖 `activity_url` 与 Product Port 投影 URL。
- 运行受影响前端测试、Go Referral HTTP 测试、typecheck、build。
