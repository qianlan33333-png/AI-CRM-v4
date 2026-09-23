# Audience 管理端确认与原因对话框

## 目标与范围

交付一个 V3-owned 的可复用确认／原因对话框，并首先替换 AI 自动化人群包页面的四类破坏性确认：人群包归档、空分组删除、触发策略归档与话术智能体解绑。它只负责呈现临时文本、必填原因校验、键盘焦点、Escape／取消和单次确认结果；业务页面仍自行决定何时调用 HTTP、如何显示回读和失败结果。

实际入口是 `internal/webshell/templates/admin_base.html` 在 `/admin/automation-conversion` 与 `/admin/automation-conversion/packages/{id}` 加载的 `internal/webshell/static/admin_console/admin_audience_detail.js`。仓库不存在题述的 `admin_audience.js` 运行时文件；不修改冻结 donor，也不触及 `admin_access.js`。

不包含全局 `window.confirm` 替换、其他页面迁移、任何 API/业务规则/权限模型改造或生产操作。

本次会话的 Skills catalog 没有可用的 Product Design 路由，故该步骤未完成；本次沿用已审核的管理端视觉基线与 shared visual token，不安装、替代或伪称已使用该插件。

## 分类

- **OneID：不涉及。** 调用点不读取、解析、创建或合并客户身份。
- **持久化：组件不涉及。** 它仅返回内存中的确认结果和可选理由；现有 Audience／Automation Owner 命令继续拥有写入、版本号和持久化收据。
- **外部效果：组件不涉及。** 不新增 Provider 调用、队列、重试或发送。现有请求的认证、CSRF、幂等键、未知结果语义保持原样。

## 组件与装配

| 位置 | 作用 |
| --- | --- |
| `web/v3/shared/ui/confirmationDialog.ts` | V3 公共 API；基于现有 `selectionDialog` 的焦点枚举、focus trap、Escape 与焦点归还合同，创建可访问的 modal。 |
| `web/v3/shared/ui/confirmationDialog.css` | 复用已有管理端 / shared visual token 的语义变量，不新增第二套色彩或壳。 |
| `web/v3/confirmationDialogHost.ts` | 将公共 API 暴露给现有静态 Audience adapter；不拦截 transport。 |
| manifest、`presentation.go`、`admin_base.html` | 仅 Audience 两条 SSR 路由加载受 manifest 校验的 CSS / Host 闭包。 |
| `admin_audience_detail.js` | 调用确认 API 后才保留原 HTTP 请求；取消零请求，确认一次原请求，失败继续使用原错误文案和刷新／重试契约。 |

调用参数包含标题、精确动作与后果、确认按钮文案、danger tone、可选的必填理由以及 readonly / busy 状态。确认控件在本次结果落定前锁定；取消、Escape 和原生 cancel 均不写入，并把焦点归还原触发控件。组件不接受 URL、HTTP body、幂等键、身份或 Provider 数据。

## 参考

- 当前仓库已合入 [PR #283](https://github.com/qianlan33333-png/AI-CRM-v3/pull/283)：Audience 现有 HTTP 合同、认证与无额外外部效果边界。
- [W3C ARIA Practices modal dialog pattern](https://github.com/w3c/aria-practices/blob/main/content/patterns/dialog-modal/dialog-modal-pattern.html)：modal 焦点进入、Trap、Escape 与触发控件焦点归还的权威交互参考。只采纳行为准则，不引入外部依赖。

## 验收

- 单元／DOM：必填理由未填不确认；确认至多 resolve 一次；取消、Escape、readonly / busy 不确认；焦点在关闭后回到触发按钮。
- Audience DOM：四个原生确认点不再调用 `window.confirm`；取消不发请求；确认分别使用未改动的 endpoint、method、body、`expected_version`、CSRF 与 Idempotency-Key；失败显示原有错误并允许依据既有业务合同重试。
- 装配：manifest、SSR Audience 页面和 release stage 都包含 Host/CSS 闭包。
- 本地受授权 PostgreSQL + Chromium：以登录的本地 Fixture 验证归档／解绑的对话框、取消、确认、失败反馈与 1440 / 1280 / 窄视口布局；不访问生产。
