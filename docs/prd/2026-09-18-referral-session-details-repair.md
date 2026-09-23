# Referral 可信会话、参加引导与邀请明细修复

## 问题与判断

真实微信 H5 OAuth 已建立可信 Payment 会话并成功获得 Referral 的共享浏览器会话；活动 `/me` 已能返回管理员指定的队长和战队。未参加活动的队长点击“查看明细”时，Referral 将“没有本活动 participation”的存储 NotFound 误映射为 Unauthorized，用户看到 HTTP 401。底部“邀请好友”因尚未参加而禁用，符合活动规则，却没有提供直接可执行的显式参加入口。

参考：仓库当前 GitHub main 的 Payment H5 OAuth → Referral bridge 及显式 `JoinCampaign` 旅程；继续复用其服务端可信会话和同源 CSRF，不引入新的身份或认证库。

## 用户可观察行为

1. 已登录但尚未参加活动的用户查看自己的邀请明细，得到空态和“确认参加后可查看”说明，不会显示登录失效或 HTTP 401。
2. 已被指定的队长看到“加入战队并邀请”入口；普通未参加者看到“参加活动并邀请”入口。入口仍打开既有活动规则确认对话框。
3. 确认参加成功后才启用邀请好友，并立即打开既有邀请链接面板。
4. 活动没有可选战队、未开始、已结束或停用时，界面展示原因，不提供误导性的参加按钮。
5. 未认证、失效会话、跨站写入、CSRF 缺失与伪造客户身份继续被拒绝。
6. 已登录但未参加时，发邀请返回 `403 referral_participation_required`；缺失或失效浏览器会话仍返回 `401 referral_session_required`。

## 领域与安全边界

- OneID：涉及读取。复用 Payment Provider 验证后建立的共享 browser session；HTTP 不接收客户身份。
- Persistence：已有 Referral JoinCampaign 单一 PostgreSQL UoW；本修复不新增表、任务或迁移。
- External effects：不涉及；不发送消息、不发钱、不发券。
- 参加仍由显式 POST、同源和 CSRF、幂等键控制；本修复不自动参加、不变更邀请归属。
- “无 participation”仅代表该可信 actor 没有自己的邀请明细，不能与身份认证失败混淆；底层数据库异常继续上浮为 unavailable。

## 验收

真实 PostgreSQL 组成式旅程必须覆盖 Payment H5 OAuth callback → Payment cookie → Referral bridge → `/me` 指定队长 → 未参加明细空结果 → 显式参加 → 已参加明细与发邀请；同时断言 OAuth 与读取阶段没有提前写 participation、归属或分数事实。
