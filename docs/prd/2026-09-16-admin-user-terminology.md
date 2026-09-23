# 管理后台用户称谓统一

日期：2026-09-16

## 业务判断与边界

```text
OneID: 只读沿用既有 canonical customer 事实；本项不解析、Provision、绑定或合并身份。
Persistence: stateless；只调整管理端静态展示文案，不改变业务写入、字段、数据库、任务或回读。
External Effects: not involved；不调用 Provider，不改变支付、标签、同步、发送或外部效果。
```

本项把管理端面向运营人员的中文称谓从“客户”统一为“用户”。API、数据库、Customer/OneID 领域模型、JSON 字段、DOM data 属性、路由、模板变量和技术协议保持原样，避免改变调用合同。用户数据中的姓名、备注、历史导入文本和 Provider 返回值不做全局替换。

管理端 v3-owned shell、页面模板和 Host adapter 是本次可修改范围。`admin_base` 的品牌、顶栏和导航外壳由经营总览 UI 任务统一调整；本项处理导航 JSON、页面规格和页面级 fallback 文案，避免路由标题仍显示旧称谓。overview Host/CSS 由该任务负责。

`web/dist/admin/*.html` 是 `DistAdminPageFile` 实际提供的管理端构建产物，不能因它来自冻结 donor 而遗留可见旧称谓。为保持 donor 字节冻结，本项在既有 `scripts/build-v3-host-adapters.mjs` 最终装配阶段，对每份已生成 HTML 的确定静态文本及 `aria-label`、`placeholder`、`title` 做完整短语白名单转换，并同步重新计算 manifest release metadata。转换不遍历或修改 HTML 的 `script`、`style`、URL、属性名、JSON/API token、模板语法节点或动态数据；不手改 `web/dist`。

三个既有 V3 Host 会加载冻结的同源标准组件脚本，且这些脚本有确定的运营端动态 UI 文案。构建仍先校验 donor SHA-256；仅对 release copy 的六个精确字面量做白名单替换（用户群、插入用户姓名、每个用户、渠道表头），不改变 `{{客户名}}`、`data-*`、API 字段或任何运行时数据。所有其他脚本保持字节原样。

冻结 donor 中仍可能出现的“客户”只能通过已经存在的 v3-owned adapter 做定点呈现适配；若某页没有稳定的现有挂载点，则登记为未覆盖项，不复制 donor 或新增平行文案框架。

## 参考与复用

- `docs/prd/2026-09-15-admin-overview-home.md`：管理端统一壳、Owner 只读事实和 OneID 边界。
- `docs/prd/2026-09-15-admin-list-visual-consistency.md`：管理端 v3 adapter 复用、冻结 donor 不改和真实页面验证原则。
- `skills/aicrm-v3-development/SKILL.md`：OneID 与持久化/外部效果的双轴分类。
- `skills/aicrm-v3-frontend-consistency/SKILL.md`：canonical route → handler/adapter → mount → manifest → page caller 链路，以及 v3-owned adapter 复用边界。
- GitHub 参考沿用仓库已核实的 [Ant Design 组件设计](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) 与 [Playwright 截图指南](https://github.com/microsoft/playwright/blob/main/docs/src/screenshots.md)：文案变化仍需配合语义、路由和真实挂载验证，不以静态替换证明业务完成。

## 入口与文件范围

| 页面/能力 | 当前入口 | 本次处理 |
| --- | --- | --- |
| 用户目录与档案 | `internal/webshell/templates/admin_customers.html` → `internal/webshell/static/admin_console/admin_customers.js` | 修改页面静态和 Host 生成的用户可见文案；保留 `customer_*` API/DOM 标识。 |
| OneID 查询 | `internal/webshell/templates/admin_oneid.html` → `internal/webshell/static/admin_console/admin_oneid.js` | 修改中文展示称谓；保留 `Customer ID`、`customer_id` 和 OneID 协议。 |
| 会话存档 | `internal/webshell/templates/admin_message_archive.html` → `internal/webshell/static/admin_console/message_archive.js` | 修改目录说明和消息方向文案；保留 `customer_to_staff`/`staff_to_customer` 枚举。 |
| 人群包详情 | `internal/webshell/templates/admin_audience_detail.html` → `internal/webshell/static/admin_console/admin_audience_detail.js` | 修改可见列名、状态和说明；保留事件 key 与 API 字段。 |
| 已有 v3 管理 Host | `web/v3/customerAdapter.ts`、`aiAssistantAdapter.ts`、`channelAdmissionHost.ts`、`channelAdmissionStandard.js`、`componentStatesHost.ts`、`openPlatformAdapter.ts`、`orderAdapter.ts`、`radarAdapter.ts` | 仅修改 adapter 自己生成的静态 UI 文案；不改冻结 donor 或技术 token。 |
| 登录品牌 | `internal/webshell/templates/login.html` | 改品牌可见称谓；`admin_base` 由经营总览任务处理。 |
| 负责人迁移 Host | `internal/webshell/static_src/admin_console/owner_handoff_host.ts` | 通过已有 Host 对冻结 donor 的确定静态标签做定点适配；保留旧 Excel 输入表头和技术字段，生成的构建资产交由统一构建流程更新。 |
| 构建管理页 | `web/dist/admin/*.html` → `scripts/build-v3-host-adapters.mjs` | 对实际由 `DistAdminPageFile` 暴露的静态标题、导航、说明、空态和安全属性做构建期白名单适配；不改 donor、运行时脚本或动态文本。 |

后端页面规格与菜单同步涉及 `internal/webshell/handler.go`、`internal/webshell/renderer.go`、`internal/webshell/static/admin_console/admin-navigation.v3.json`；对应断言在 `internal/webshell/handler_test.go`、`internal/webshell/dist_test.go` 和 `web/v3/navigationHost.test.mjs`。

## 验收与未覆盖

同步更新直接断言上述静态文案的 webshell/Host 测试，并运行构建期转换的 Node 合同：静态用户文案正确、`script`/nonce、模板变量、`customer_id`、`Customer ID`、旧 Excel 列名和退款原因 value 不变。构建后检查每一份实际 `web/dist/admin/*.html` 的静态 HTML、manifest release metadata 及既有 shell 合同。真实 PG/Chromium、生产发布和未挂载 donor 页面不因本项静态替换而自动通过。

以下内容由同一经营总览 UI 任务中的 terra 负责，不属于本子任务改动：`internal/webshell/templates/admin_base.html`、`internal/webshell/contract.go` 及其顶栏/导航合同实现；`web/v3/overviewAdmin.ts`、`web/v3/overview.css` 与总览旅程。另有 `web/v3/sidebar/**`、`web/src/admin/**`、`web/donor-sources/**` 及用户数据/接口返回文案不在本次文案改动范围。`web/dist` 只可由既有 build adapter 重建，不能手改。具体残留如下：

- `internal/webshell/templates/sidebar.html`、`web/v3/sidebar/main.ts` 及 `internal/webshell/static/sidebar_workbench/**` 属于并行 Sidebar/V2 donor 及其身份上下文，未改动。
- `internal/webshell/static/admin_console/owner_migration_dd8d60d.html` 保持冻结；其中 `客户备注名`、`external_userid` 等旧 Excel 输入合同仍保留，Host 只做精确静态标签适配。
- `web/v3/channelAdmissionHost.ts` 的 `{{客户名}}` 是服务端接受的模板变量，必须保留；旁边的说明已改为“用户姓名”。
- 测试 fixture、Provider 返回值和历史导入/用户姓名等示例数据中的“客户”未替换，避免把数据内容误当产品文案。
