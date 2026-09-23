# 企微客户侧边栏：统一呈现与窄屏适配

## 目标与范围

在企业微信内嵌的客户侧边栏中，让 360px 与 420px 宽度下的客户摘要、六个现有分区、素材卡片和反馈状态保持可读、可操作且不横向溢出。目标是统一现有业务事实的呈现，不增加侧边栏功能。

本 PR 覆盖 `/sidebar/bind-mobile` 已存在的客户概览、问卷、商品、订单、优惠券和素材分区，以及其加载、空态、授权失效、403、读取失败与重试反馈。素材分区保留现有图片素材的标签、缩略图、搜索、分页和发送入口。

本 PR 不覆盖员工目录、客户标签选择、群聊目录、话术编辑器或自动化内容：当前正式侧边栏没有这些调用入口，且 `SidebarBridge` 明确拒绝聊天与其他员工路径。不会把后台选择器、群运营组件或自动化话术壳装入企微侧边栏。

## 业务与安全分类

| 判断 | 结论 |
| --- | --- |
| OneID／外部身份 | **读取既有可信上下文**。`SidebarBridge` 经企微 `getCurExternalContact`、既有 OAuth 与签名的 `X-Sidebar-Context-Token` 获取已绑定客户；呈现层不解析、匹配、绑定、建客或读取身份表。上下文失效、未授权与 403 继续由现有 Owner 拒绝。 |
| 持久化 | **无新增持久化**。本 PR 不写数据库；既有换绑手机号、客户资料和周期订单备注仍由原有 Owner 命令处理，视觉层不介入。 |
| 外部效果 | **无新增 Provider 读取或写入**。既有商品、优惠券和素材发送按钮保留原有 `SidebarBridge` 的意图、幂等、JSSDK 回执与 `outcome_unknown` 限制；本 PR 的浏览器验收不触发发送。 |

## 真实装配与复用边界

| 项目 | 实际链路／处理 |
| --- | --- |
| Canonical 路由 | `/sidebar/bind-mobile` → `internal/webshell.RenderSidebar` → 生成的 `web/dist/sidebar/index.html` → `web/v3/sidebar/main.ts` → V3 受限 bridge → 生成的标准 overlay。 |
| 冻结 donor | `internal/webshell/static/sidebar_workbench/sidebar_customer_workbench_dd8d60d.html`、`sidebar_workbench.css`、`sidebar_workbench_dd8d60d.js` 只作行为供体，字节不修改。 |
| V3 扩展 | 新增 V3 scoped presentation stylesheet，并在 `scripts/build-v3-host-adapters.mjs` 以 manifest 哈希资源注入。构建与 `scripts/stage-new-shell-ui.mjs` 必须包含完整闭包；`/sidebar-assets` 仍只服务该闭包。 |
| 现有共享资产 | 复用 `web/v3/shared/ui/visualTokens.css` 的轻量 token 语义；不加载后台壳、员工／群／标签 picker 或管理端选择器。保留 `web/v3/sidebar/main.ts` 的可信上下文、缩略图和外部效果边界。 |
| 现有业务调用 | 客户摘要、问卷、商品、订单、优惠券、图片素材均通过现有 `/api/sidebar/v2/*` scoped endpoint；素材标签是 Owner 返回的 `item.tags`，不新增标签查询。 |

## 体验合同

1. **客户摘要与导航**：摘要信息能换行而不暴露 `external_userid`；换手机号按钮、六个导航项和当前分区在 360／420px 内可达。导航保持原有六分区与数据权限，不重排成后台菜单。
2. **信息分区**：白色内容面、浅灰边界、蓝色唯一主操作／当前导航、14px 正文和紧凑 12px 辅助信息。素材卡须显示实际标签、受控缩略图及占位／重试状态；长名称和标签不得撑开宽度。
3. **稳定状态**：加载、空态、无权／失效上下文、可恢复读取失败与不可用分别呈现。401／无效上下文说明需要重新打开或重新验证；403 说明当前无权访问；暂时不可用保留重试。不得把原始错误码、Provider 术语或“后端能力未就绪”作为主文案。
4. **失败保留**：仅对仍处于同一受权客户上下文的暂时性素材读取失败，保留上一次结果和用户已输入的查询，显示非破坏性错误及原有重试动作；401、403 与上下文失效立即清除敏感内容并阻止发送。旧响应不能覆盖当前查询或上下文。
5. **边界不变**：素材搜索继续由现有 form 的明确提交触发；不改发送、资料保存、订单备注、OAuth 或签名上下文的时序、幂等键、权限和业务 DTO。

## 设计依据

| 参考 | 采用 | 不采用 |
| --- | --- | --- |
| [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) | 语义 token、白色 surface、单一主操作、状态可辨识与受控组件级样式。 | 不引入 Ant Design、React、运行时主题或其组件。 |
| 已装配的官方企微 JS-SDK `jweixin-1.0.0.js` | 仅保留当前 `getCurExternalContact` 的可信上下文读取方式。 | 不引入第三方 JSSDK 封装，不绕过现有官方脚本、签名 context 或 `SidebarBridge`。 |

## 验收

1. V3 build manifest、release stage 与 `/sidebar-assets` 闭包同时含新的样式资源；缺资源／篡改资源保持失败关闭。
2. 真实 PostgreSQL + Chromium 授权 fixture 打开 `/sidebar/bind-mobile`：客户摘要、六个分区、图片素材和标签可读；360／420px 无横向溢出，主操作在视口内。
3. 在不点发送按钮的前提下，验证素材缩略图加载、标签换行、空态、403、失效 context、读取错误／重试；同查询刷新失败仍显示原已加载素材并保留查询。
4. 验证正式页面和所有 sidebar assets 在无后台 session 的 WeCom webview 路径可读；不出现后台员工／群／标签目录请求或直接 Provider 调用。
5. 运行受影响 TypeScript、V3 build、sidebar host adapter、stage closure、实际 PostgreSQL Chromium Journey；PR 记录准确 SHA、截图和未覆盖的业务能力。

## 未覆盖与后续

客服或群成员选择、标签编辑、自动化话术编辑和任何新的外部发送能力须在各自 Owner 的受权页面和独立 PR 中实现；它们不能借侧边栏视觉任务扩大读取权限或发送范围。
