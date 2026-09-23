# 管理端页头主操作收敛

## 目标

将群运营计划、渠道码中心和内容雷达列表页的页面级主操作放入唯一的 V3 `admin-topbar` 右侧。列表中的筛选、刷新、分页和行级操作维持原位置与行为；不保留重复标题、说明文字或空操作行。

| 页面 | 壳标题 | 页头操作 | 保留在内容区 |
| --- | --- | --- | --- |
| 群运营计划 | 群运营计划 | 查看所有群、创建计划 | 指标、提示、创建草稿、列表、分页与行级操作 |
| 渠道码中心 | 渠道码中心 | 新建渠道 | 搜索、统计、列表、抽屉与行级操作 |
| 内容雷达 | 内容雷达 | 新建雷达链接 | 查询筛选、刷新、分享、列表、分页与行级操作 |

## 开发前分类

```text
OneID: not involved — 仅调整已经获授权管理页的导航和按钮挂载，不读取、解析或关联客户身份。
Persistence: stateless — 页头组件只触发既有页面路由或既有创建草稿动作；不保存任何数据。
External Effects: not involved — 不新增 Provider 调用、队列、发送或重试；原有领域命令继续由原调用方拥有。
```

因此不会改变 GroupOps 计划 CAS、渠道配置命令、雷达链接命令或各页现有权限校验。群运营“创建计划”仍通过其原 `show-create-plan` 状态机进入草稿，锁定期间不会绕过禁用条件。

## 实际入口与复用

- 统一复用 V3 共享 `mountPageHeaderActions(owner, actions)`，它只装载到既有 `.admin-topbar > .admin-topbar-meta`，不新增页面头或业务按钮实现。
- 群运营列表由 `web/v3/groupOpsStandard.js` 的 `renderList` 维护；顶部按钮改为共享页头调用原有 `show-create-plan` 或原群列表路由。
- 渠道列表由 `web/v3/channelCenterAdapter.ts` 装配冻结模板；页面使用 `admin_base` 的既有壳标题，适配层移除模板中的重复标题、说明和创建按钮，同时保留搜索输入及其已提交搜索合同。
- 内容雷达由 `web/v3/radarAdapter.ts` 管理冻结列表；共享页头调用既有“新建雷达链接”路由，内容区不再有局部空页头。
- 冻结 donor 文件不修改，所有调整位于 V3 adapter、共享组件装配和 shell renderer。

## 参考与设计依据

- 用户提供的三个实际页面截图：页面级创建按钮应与壳标题同列，筛选属于内容区。
- [PR #317](https://github.com/qianlan33333-png/AI-CRM-v3/pull/317) 的共享组件装配、单一 `admin_base` 壳和冻结 donor 边界。
- Product Design：当前会话 Skills catalog 未提供可调用的 `product-design:*` 路由。本项按现有已批准全局 UI 视觉规范、实际截图和共享组件索引实施；该插件环节未完成，未假称已使用。

## 验收

1. 每页只存在一个 `h1.admin-page-title` 和一个 `.admin-topbar`，对应操作只出现一次且位于其右侧。
2. 群运营“查看所有群”使用原路由；“创建计划”仍尊重原列表锁定状态，点击只打开本地草稿，不发请求。
3. 渠道“新建渠道”和雷达“新建雷达链接”保持原创建路由；内容区搜索、刷新、分享、分页和列表不丢失。
4. 1280、1440 管理端视口中页头操作不换壳；窄屏沿用 `admin-topbar-meta` 的现有换行规则。
5. 运行共享 helper 单测、三页受影响 adapter/Host 测试、TypeScript、shell contract 和至少相应的真实 Host Chromium Journey；构建、staging manifest 与检查均绑定最终提交 SHA。
