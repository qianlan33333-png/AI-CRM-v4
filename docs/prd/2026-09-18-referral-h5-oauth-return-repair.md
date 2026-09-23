# 裂变活动微信授权回跳修复

## 问题与目标

用户从 `/referral?campaign=1` 发起微信授权时，Payment H5 OAuth 的应用层已认可该回跳，但保存一次性 OAuth state 的 PostgreSQL 约束仍只允许商品和分销路径。因此 state 写入失败，授权入口返回 `invalid_request`。

本次让有效裂变入口完成“开始授权 → 一次性 state → Provider 验证 → 可信 Payment session → 原始裂变页面”的既有链路；回跳本身不参加活动、不换绑归属，也不产生积分。

## 闭合契约

仅允许以下同源、canonical 回跳值：

- `/referral`
- `/referral?campaign=<正 int64>`
- `/referral?campaign=<正 int64>&invite=rfi_<43 个 URL-safe 字符>`

拒绝外站、片段、编码分隔符、未知/重复/乱序参数、非正数和 int64 溢出。继续由 H5 OAuth state 消费后决定回跳，不接受浏览器提供的身份、客户主键或任意 next URL。

## 领域判断

- **OneID：涉及。** OAuth 只通过既有 Provider 验证后签发可信 Payment session；Referral 继续从稳定 bridge 取得 canonical Customer，绝不接受浏览器自报身份。
- **持久化：涉及。** 修复 Payment 拥有的 `payment_h5_oauth_states` 约束；state 的创建与消费仍在 PostgreSQL Unit of Work 内。
- **内部持久任务：不涉及。** 没有新增任务或 worker。
- **外部效果：不涉及。** 复用既有微信 OAuth 身份读取；不提交支付、分账、转账、奖励或其他 Provider 写入。

## 实施与验收

1. 新增前向 Payment migration 扩展现有 CHECK，不修改已发布的 0162。
2. readiness 和发布安装契约要求 0191，避免发布时缺失约束修复。
3. 入口区分请求无效（400）与 OAuth state 持久化不可用（503），不返回内部错误、state、code 或身份信息。
4. PostgreSQL 回归先证明 0162 拒绝 Referral state，再证明 0191 后有效路径可保存、非法路径不能保存；真实合成 HTTP 回归覆盖 campaign-only、campaign+invite、callback 精确回跳、state 单次消费、可信 session 签发及零参与/零归属变更。

## 参考

GitHub 参考检索了 [Argo CD 的 OIDC 回跳校验](https://github.com/argoproj/argo-cd/blob/master/util/oidc/oidc.go)：其授权链路把回跳限制在预先验证的来源和路径。本修复沿用本仓既有 exact-return whitelist，并把数据库约束与应用层契约对齐，不引入通配回跳规则。
