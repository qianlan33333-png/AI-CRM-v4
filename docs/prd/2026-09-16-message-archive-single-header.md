# 会话存档单页头 PRD

日期：2026-09-16
状态：已授权实施；仅修复已观察到的管理端重复标题。

`/admin/message-archive` 与 `/admin/message-archive/customers/{id}` 同时渲染 V3 `admin-topbar` 的“会话存档”和内容卡片的同名 `h2`。这违反管理端一个页面一个页级标题的既有约束。入口仍然是选择客户，不增加全局会话列表。

## 分类、复用与范围

OneID：不涉及；入口与详情仅使用既有受权客户读取，不解析、建客、绑定或合并身份。

Persistence / External Effects：不涉及；本修复不改归档读取、审计、数据库、任务、企微 Provider 或外部效果。

复用 V3 `admin_base` 已有 `PageActions` 顶栏动作槽。[GitHub PR #201](https://github.com/qianlan33333-png/AI-CRM-v3/pull/201) 的产品、频道页已使用同一 `ShowPageHeader` / `PageActions` 模式维持顶栏唯一标题；不创建新标题壳或平行按钮样式。

只改 Webshell 的会话存档模板与路由数据：保留唯一 `admin-topbar` 标题和说明；把“选择客户”移入其既有 primary action 槽；移除 entry/detail 卡片中的重复 `h2`；详情搜索表单仍留在内容区。

## 验收

1. 两条路由各只有一个 `.admin-page-title`，且不再有会话存档卡片 `h2`。
2. 顶栏含 `/admin/customers` 的“选择客户” primary action；entry 不再另有平行按钮。
3. customer detail 仍含 `#archive-search-form` 和受保护 archive root；不改变其 API 或认证边界。
4. Webshell HTTP/template 回归及真实 PG + Chromium 验收覆盖 1280、1440，无横向溢出。
