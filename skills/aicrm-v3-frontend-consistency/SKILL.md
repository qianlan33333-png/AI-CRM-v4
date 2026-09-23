---
name: aicrm-v3-frontend-consistency
description: Keep AI-CRM-v3 admin, WeCom sidebar, H5, and public frontend work consistent by reusing verified project components and extending or adding shared components when needed. Use for any page, interaction, selector, media, style, or frontend asset change.
---

# AI-CRM-v3 Frontend Consistency

先阅读仓库 `AGENTS.md`、`skills/aicrm-v3-development/SKILL.md`，再按需读取 [组件索引](references/component-map.md)。本 Skill 适用于所有前端设计、开发、修改和审查；纯后端且无前端可观察影响的工作除外。它管理前端复用和边界，不替代业务、OneID、持久化或外部效果设计。

## 先作前端判断

在 PRD 或 PR 记录：用户可观察目标、终端基线（管理端／企微侧边栏／H5／公共页）、命中的组件入口，以及 OneID、持久化、外部效果分类。不同终端不能因视觉相似而互相搬用壳或数据契约。

先确认当前实际链路：canonical 路由 → handler／领域 UI adapter → `Render*` 或对应挂载入口 → manifest assets → 页面调用。历史快照、未挂载页面、残留实现和构建产物均不能单独作为标准；`web/dist` 是构建产物，是否经过验证须查当次证据，且不能代替生产入口。

## Product Design 必用路由

每项本 Skill 范围内的工作都必须从会话 Skills catalog 发现 `product-design:index` 或用户点名的 focused skill，并读取实际资源；不得硬编码插件 cache 路径或沿用过时工具 API。遵循该次可用流程和必要 preflight；插件不可用时明确记录该环节未完成，按实际能力继续独立已授权检查，不假装用过，也不自动安装、发布或保存插件记忆。

路由选择：体验审查用 `product-design:audit`；已有选定视觉目标的匹配实现用 `product-design:image-to-code`；确需视觉探索才用 `product-design:ideate`；`product-design:url-to-code` 只授权原型克隆，不能替换生产项目。普通开发仍在本仓架构和组件约束中执行。已有代码和页面是视觉目标，不要求每次生成三个新方案或重设计。

## 复用与装配

1. 从组件索引选择已存在的页面、公共组件或领域组件，复用其交互、状态、无权限和错误语义。
2. 管理端只通过 v3 `admin_base` 单壳和各领域 adapter 装配；不得引入第二侧边栏、donor 外层 HTML 或另一套页面框架。
3. 企微侧边栏复用其 sidebar 壳与受限 bootstrap 契约；H5 与公共页各自从已有入口挂载，不能借用管理端壳。
4. 冻结 donor 仅是行为和视觉证据。adapter、兼容和新增公共组件写在 v3-owned 路径，不能修改 donor 快照。

保持已选终端的字体、色彩、间距、布局，以及按钮、表单、表格、分页和弹窗的一致性；搜索、预选、取消、数量、权限校验和错误状态也属于组件合同。

## 组件缺口

按以下顺序处理：配置已有组件、组合已有组件、扩展公共组件、新增公共组件。在当前任务授权范围内，常规公共组件扩展或新增无需额外批准，但必须服务至少一个可复用的前端能力，放在公共位置并在组件索引中登记；禁止在单一页面私造平行组件或复制现有组件。

新增或扩展前，确认它不承担领域写入、身份解析、Provider 调用或效果状态机。页面只调用其领域 HTTP/Port adapter；选择器按页面授权范围读取，不以组件为由扩大数据读取范围。修改共享组件时保留默认行为，列出受影响调用点并回归它们。

## 验证

验证复用或公共组件在实际挂载终端的关键交互、加载、空态、失败、无权限和真实 HTTP 回读，并在同一视口与参考页面对照。按影响运行相关 `typecheck`、`build`、`ui:shell:contract`、Journey 或冻结检查；仅文档改动不要求业务测试。不要用 Mock、静态成功提示或截图替代业务结果。

## 交付记录

在 PRD 或 PR 使用以下简表：参考页面｜复用组件｜公共扩展／新增｜受影响调用｜Product Design skill／作用／结果｜验收证据。仅在需要定位入口或组件缺口时读取 [组件索引](references/component-map.md)。

## 治理参考

仅借鉴 [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) 的 token、语义状态和组件目录治理，以及其 [权威组件清单](https://github.com/ant-design/ant-design/blob/master/.github/copilot-instructions.md) 的“禁止虚构组件”原则；不引入 Ant Design、React 或任何 UI 依赖。
