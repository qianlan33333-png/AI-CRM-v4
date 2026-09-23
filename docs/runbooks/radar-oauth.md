# 内容雷达 OAuth 上线与回滚

## 上线前提

- Radar 复用 Survey 的微信 OAuth Provider 配置；`AICRM_SURVEY_OAUTH_ENABLED=true`。
- `AICRM_SURVEY_OAUTH_APP_ID`、`AICRM_SURVEY_OAUTH_SECRET`、`AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID` 已由发布环境注入，禁止写入日志或文档。
- 微信开放平台回调白名单包含 `${AICRM_PUBLIC_ORIGIN}/api/public/radar/oauth/callback`。
- Provider 返回必须包含 UnionID；Radar 将它规范化为 `kind=unionid`、`scope=wechat-open-platform:<open-platform-id>` 的 verified fact，再交给 OneID Resolve/显式 Provision。OpenID 不作为降级路径。

## 验收

1. 创建 link/image/PDF 三种雷达，并显式启用。
2. 非微信或 Provider 未启用时，`unionid_required` 雷达必须失败关闭，不匿名放行。
3. 微信授权后只返回 HttpOnly Radar session；浏览器响应、管理端事件和 CSV 均不得出现 UnionID、OpenID、OAuth code、手机号或 external_userid。
4. 复核 landing、oauth_verified、identity_resolved 与三种内容终态；重复终态请求返回同一 receipt。
5. 禁用雷达后公开入口返回 410；历史事件仍可在管理端读取。

## 回滚

- 紧急关闭授权入口：将 `AICRM_SURVEY_OAUTH_ENABLED=false` 并重新发布。需要 UnionID 的雷达失败关闭；不会降级匿名。
- 单条回滚：管理端禁用目标雷达，公开入口立即返回 410。
- 版本回滚不会删除 Radar 表、OneID 事实或审计记录；禁止手工删除身份关联。
- Provider 结果不确定时不重复换 code 重试，先查本地状态消费记录和 Provider 日志（日志不得包含 code/UnionID）。
