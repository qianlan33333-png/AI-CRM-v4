# 公共问卷 OAuth 与失效链接的失败呈现

## 决策与边界

`/q/{slug}` 的公开问卷由 Survey Owner 保留既有 OAuth/session 入口。真实移动 Chromium 取证（`/Users/qianlan/aicrm-artifacts/remaining-pages-chromium-20260916/public-survey-mobile-07a41fd4cf5a`）发现两类失败页仍暴露冻结演示壳：OAuth 回调失败继续称“正在验证微信身份”，没有安全回跳上下文的 `/h5/error.html` 显示“增长诊断测评”“后端能力未就绪”“演示题”和“全部屏幕”。这些文案错误地暗示仍在进行或可以继续。

本 PR 只扩展已有的 `web/v3/surveyPublicHost.ts` 和 `surveyPublic.css`。它在冻结 H5 runtime 挂载前，把以下既有 Owner 错误事实显示为统一的白卡、浅灰背景和蓝色已知入口：

| Owner 已给出的事实 | 呈现 | 操作 |
| --- | --- | --- |
| `/h5/auth.html` 有 `oauth_error` | 授权失败，无法继续当前授权 | 移除“重试微信授权”；仅当 `slug` 通过既有公开 slug 规则时，提供 `/q/{slug}` 入口。 |
| `survey_oauth_unavailable` | 微信授权暂不可用，无法继续 | 不提供重试或跳转。 |
| `survey_oauth_failed` | 授权失败，当前链接无法继续 | 不提供重试或跳转。 |
| `survey_identity_conflict` | 当前身份无法继续该问卷 | 不提供重试或跳转。 |
| 其他或缺失 `code` | 链接无效，无法继续 | 不提供重试或跳转。 |

查询参数不是可信显示或跳转来源：Host 只用精确白名单代码决定文案，不回显 `code` 或 `oauth_error`；回跳只在独立 slug 校验通过后，由 `encodeURIComponent` 组成同源 `/q/{slug}`。不新增、复制或执行 OAuth 请求。

OneID：不新增也不改变。本次只呈现 Survey Owner 已经给出的 OAuth/身份失败事实，不解析、匹配、创建或合并身份。

持久化、内部任务与外部效果：不涉及。没有写入、队列、Provider 调用、支付或回调；冻结 Controller、OAuth Handler、权限和 API 合同保持不变。

## 复用与装配

- 复用 #346 的 [public Survey H5 Host 装配](https://github.com/qianlan33333-png/AI-CRM-v3/pull/346) 及其 `h5AuthAdapter` → `surveyPublicHost` → 冻结 H5 runtime 顺序；不创建第二个 H5 壳，也不修改 donor。
- 组件索引指定公共问卷使用 `surveyPublicHost.ts`、`surveyPublic.css` 和 Survey Owner 的 `/q/{slug}` OAuth/session 入口；将 `error.html` 加入该已有 Host 的明确装配清单和 release-stage 合同。
- 本会话 Product Design catalog 没有可用路由，已如实记录；沿用 #346 已验证的浅灰画布、白卡和蓝色主入口 token，不引入新 UI 依赖。

## 验收

1. JSDOM 验证 OAuth 失败和四种 error code 均没有冻结 demo 文案、假进度或“重试微信授权”；已知有效 slug 仅生成同源问卷入口。
2. 恶意 `code`、`oauth_error` 或无效 slug 不进入 DOM、属性或 URL，不生成跳转。
3. 正常 `auth/all/one/result` 的 #346 公共问卷 Host 合同保持；构建与 Survey stage 明确要求 `error.html` 引入相同 Host、样式和 manifest 闭包。
4. 不调用真实 OAuth Provider；HTTP Handler 定向合同证明其现有安全回跳与固定错误代码未被修改。

## 不覆盖

不改变 OAuth 回调、Cookie、身份冲突决策、问卷定义、答卷提交、结果 token、401/403/503 的答题页恢复策略，或其他 H5 页面。
