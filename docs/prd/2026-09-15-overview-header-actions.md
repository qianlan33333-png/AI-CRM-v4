# 经营总览页头统计区间收敛

## 目标

将 `/admin` 的四个统计区间操作“今日、近 7 天、近 30 天、自定义”收敛到唯一 `admin-topbar` 的右侧，并删除正文重复的“经营概览”及“统计口径以各项数据的确认时间为准”。正文继续显示实际统计区间、各分区读取时间、加载与失败反馈；自定义日期只在用户选择后在数据区展开。

## 开发前分类

```text
OneID: not involved — 本项只调整已授权经营总览的客户端操作入口，不读取、解析、Provision 或关联客户身份。
Persistence: stateless / read-only local projections — 页面继续调用既有、授权的 GET /api/admin/overview；本项不新增写入、任务或汇总。
External Effects: not involved — 不调用 Provider，也不新增队列、发送或重试。
```

既有 `/api/admin/overview` 的 Payment／Identity canonical payer 只读协调、统计口径和请求代际由原 Owner 保持。本项不改变自定义草稿、预设请求、错误回退或权限失效后的数据清理。

## 实际入口与复用

- 路由 `/admin` → `internal/webshell/handler.go` → `Renderer.RenderOverview` → `admin_base` 中唯一 `.admin-topbar` → `#overview-admin-root` → `web/v3/overviewAdmin.ts`。
- 复用 V3 `web/v3/shared/ui/pageHeaderActions.ts` 的 `mountPageHeaderActions`。它只在既有 `.admin-topbar > .admin-topbar-meta` 中装配，不创建第二个页面头。
- 顶栏按钮直接调用 overview Host 已有的 `load` / `openCustomDraft` 路径；不再依赖正文 root 的冒泡委托。只在 Host 初始装配时挂载，后续渲染只同步既有按钮的 active 状态和 ARIA，不重建按钮或丢失键盘焦点。
- 自定义日期表单仍在 `#overview-admin-root` 内，保留既有 submit 处理和未应用草稿。切换回预设仅发起该预设原有 GET，不会提交未应用日期。

## 参考与设计依据

- [PR #335](https://github.com/qianlan33333-png/AI-CRM-v3/pull/335) 已合并并提供真实 `admin-topbar`、共享页头 action 装配与单标题契约。
- 复用当前组件索引中的 `admin_base` 单壳和“顶栏动态操作”入口；不新建平行页头或自定义按钮组件。
- Product Design：当前会话 Skills catalog 未提供可调用的 `product-design:*` 路由。本项按已批准全局 UI 规范、真实管理端壳和共享组件索引实施；该插件环节未完成，未假称已使用。

## 验收

1. `/admin` 只保留一个壳标题和一个 `.admin-topbar`；四个统计区间操作各出现一次，并位于标题右侧。
2. 正文不再出现重复“经营概览”或“统计口径以各项数据的确认时间为准”；实际统计区间与各项数据读取时间仍在数据区。
3. 今日／7 天／30 天仍通过原 GET 更新；自定义打开日期草稿，切换预设不会提交未应用草稿；提交自定义仍沿原请求、错误和乱序拒绝合同运行。
4. 1280、1440 认证 PostgreSQL Chromium 旅程验证顶栏布局、标题唯一、四个入口、自定义展开和既有读取事实；受影响 Node、类型检查、构建、正式 stage 与 P5 均绑定最终 SHA。
