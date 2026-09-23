# PRD — 分销管理概览收敛与申请二维码

## 业务判断与范围

用户希望后台分销管理减少重复入口与冗长口径说明，并把概览收敛为一行四项可直接判断经营状态的指标：

1. 成交额
2. 待结算佣金
3. 已结算佣金
4. 待处理异常订单

顶栏只保留“申请二维码”。“打开申请页”和“复制申请链接”不再作为并列顶栏动作；二维码对话框内保留已有复制链接能力。分销员、订单、异常三个 tab 与当前页筛选应位于同一控制行，并在窄宽度自然换行。正文保留“分销概览”区块标题，删除页面重复标题、观察时间、口径注脚、“当前口径内未形成”和“仅筛选当前页”等长说明；这不改变数值、状态、权限或既有操作。

### 口径结论

当前 `distribution_exceptions` 的 `COUNT(*)` 是异常**记录**数；同一订单可能有多笔佣金或多条异常，不能把它直接标为“异常订单”。

本 PR 新增 Distribution Owner 的只读事实 `CurrentExceptionOrderCount`，在 `distribution_exceptions e JOIN distribution_commissions c ON c.id=e.commission_id` 上，按 `e.status IN ('open', 'querying')` 计算 `COUNT(DISTINCT c.order_id)`。该字段经既有 `distributionport.OverviewReader` 和 `GET /api/admin/overview` 的 `distribution.current_exception_order_count` 输出，仅供分销管理的第四项指标使用。

既有 `OpenExceptionCount`、`todos.items[code=distribution_exceptions]`、异常列表和经营首页维持异常记录数的原语义，不重命名、不复用、不回退为新字段。新字段未出现或概览区段不可信时，页面显示“—”或“待确认”；可信 `ready/zero` 的 `0` 才显示 `0`。

### 架构分类

```text
OneID: not involved. 本项仅读取既有管理员授权范围内的 Distribution 聚合和公开页面 URL，不解析、创建、关联或合并客户身份。
Persistence: stateless read-only. 新字段由 Distribution 已拥有的表实时汇总；本项不新增表、迁移、持久任务或业务写命令。既有启停、异常核验、追回和商户承担的版本、CAS、幂等与审计合同不变。
External Effects: not involved. 二维码只展示同源公开申请 URL，不调用 Provider；不新增支付、退款、分账、发送或调度。
```

## 现有链路与复用

| 目标 | 现有入口 | 本次处理 |
| --- | --- | --- |
| 管理页 | `/admin/distribution` → `RenderDistribution` → `#distribution-admin-root` → `distributionAdmin` / `distributionStyles` | 继续使用唯一 `admin_base` 顶栏、侧栏和 Access 会话；不新增页面壳。 |
| 顶栏动作 | `web/v3/shared/ui/pageHeaderActions.ts` | 保留同一 owner，动作缩为一个“申请二维码”。 |
| 分销详情 | `web/v3/shared/ui/detailDrawer.ts` | 不改现有详情抽屉、异常命令或确认流程。 |
| 当前页筛选 | `web/v3/shared/ui/committedTextSearch.ts` | 保留已接入的 Enter / “筛选”提交、IME 草稿、当前已加载页范围和焦点保持；只将容器与 tabs 编排为同一控制行。 |
| 商品分享二维码 | `web/v3/productAdapter.ts#showShare` + 冻结 donor 的 `qr.ts` | 将**纯展示**的对话框提取到 V3 shared UI；二维码 SVG 继续使用冻结 donor 的 `renderQr` / `downloadQr`，不修改 donor。Product 保留复制、预览、保存二维码和关闭；Distribution 保留复制申请链接和关闭。 |
| 概览读取 | `distributionport.OverviewReader` → Distribution PostgreSQL store → Overview Service → `/api/admin/overview` | 扩展同一只读模型与 OpenAPI；不新建端点、汇总表或跨领域读取。 |

Product Design 路由在本会话的 Skills catalog 中不可用，未伪造其调用。视觉继续复用已批准的后台灰白蓝 token、`pageHeaderActions`、`admin-table`、`detailDrawer` 和 `presentation.css`。

GitHub / 已合主线参考：

- [PR #309](https://github.com/qianlan33333-png/AI-CRM-v3/pull/309)：`distributionAdmin`、唯一顶栏动作、已提交的当前页筛选、概览状态与实际 1280/1440 验证。
- [PR #291](https://github.com/qianlan33333-png/AI-CRM-v3/pull/291)：商品分销只在“售卖信息”维度配置；商品编辑页不提供申请链接、复制或二维码。

## 接口与状态

### Distribution read model

`internal/distribution/port.Overview` 增加：

```go
CurrentExceptionOrderCount int64
```

`internal/distribution/store.ReadOverview` 的单次只读 SQL 保留原有五项金额/计数与 `OpenExceptionCount` 子查询，并增加独立子查询：

```sql
SELECT COALESCE(COUNT(DISTINCT c.order_id), 0)
FROM distribution_exceptions e
JOIN distribution_commissions c ON c.id = e.commission_id
WHERE e.status IN ('open', 'querying')
```

查询只触及 `distribution_exceptions` 与 `distribution_commissions`，两者均由 Distribution Owner 持有。

### Overview response

`internal/overview/app.Distribution` 与 `AdminOverviewDistribution` schema 增加必需整数 `current_exception_order_count`。分销 section 与 todo section 都沿用自身 `status/as_of/reason_code`：

- `distribution.status=ready|zero`：第四卡显示新字段的真实数值，包括 `0`。
- `distribution.status=data_missing|failed` 或字段无效/缺失：第四卡显示“待确认”或“读取失败”，不读取 `todos` 的记录数替代。
- `todos` 继续使用 `OpenExceptionCount` 和 `distribution_exceptions`，供现有经营待办和异常记录语义使用。

这是既有公开 API 的精确契约扩展。实现后先向根审提交 OpenAPI、port、overview app/store 的 authority diff；在批准前不更新 authority/source-index/lock/P5 绑定。

## 前端行为

1. `/admin/distribution` 以唯一 shell 标题“分销管理”呈现。顶栏只出现一个“申请二维码”按钮；保留“分销概览”区块标题，不另建页面标题。
2. 点击“申请二维码”打开 shared V3 对话框，展示同源 `/distribution` URL 与二维码。对话框中可复制申请链接；复制失败给出既有页面反馈。它不打开页面、不请求 Provider、不推断 URL，也不改变分销注册、商品资格、收款准备或资金状态。
3. 商品“分享”也改用同一 shared 对话框，继续在其原回调里提供商品 URL、复制、预览、保存二维码、关闭。Product 的 URL 校验、授权读取和现有错误边界不变。
4. 概览保留期间切换按钮，但正文只显示 4 张卡，桌面 1280/1440 为一行。卡标题为“成交额”“待结算佣金”“已结算佣金”“待处理异常订单”；数字较现状更突出。删除期内初始佣金与佣金笔数，以及正文的观察/口径长说明。
5. 真实零值、未知和失败仍通过数值或短状态文本区分，不把未知/失败压成零。失败时保留“重新读取”这一可恢复入口。
6. tabs 和当前页筛选置于一个 `.distribution-admin-controls` 容器。1280/1440 同行；窄宽度允许按原控件顺序换行。现有“筛选”按钮、草稿、Enter/IME 与“只筛选已加载页”的真实范围不变，删除冗长解释文案但不扩大筛选范围。
7. 分销员、订单、异常表格、分页、详情、启停、异常核验/追回/商户承担不重设计、不改写入语义。

## 代码边界

预计修改：

- `internal/distribution/port/overview.go`
- `internal/distribution/store/overview.go` 及 PostgreSQL integration test
- `internal/overview/app/service.go`、对应单元/HTTP composition tests
- `api/openapi.yaml` 与其受控生成/authority 元数据（待精确 diff 审核）
- `web/v3/shared/ui/shareQrDialog.ts`、样式和组件索引：只承载展示、焦点、关闭和 caller-provided actions
- `web/v3/productAdapter.ts`、`web/v3/distributionAdmin.ts`、`web/v3/distribution.css` 及已有 Host tests/journey assertions
- `cmd/aicrm/distribution_chromium_journey.mjs` 与实际 PostgreSQL composition/journey fixture

不修改冻结 donor、商品分销策略、公开分销中心领域流程、订单归因、支付/退款/分账 Provider、身份、权限模型、数据库 schema 或任何生产数据。

## 验收

1. Distribution store：同一 `order_id` 的多条佣金、多条 `open/querying` 异常只计 1；两个订单计 2；`resolved` 不计；读取失败不伪造零。
2. Overview app/HTTP/OpenAPI：新字段随 Distribution section 的状态输出；旧 `todos.distribution_exceptions` 仍为异常记录数；缺新字段时前端不回退；Access 401/403 与既有聚合授权一致。
3. shared QR dialog：两个真实 caller 使用同一 V3 component；Product 保留复制/预览/保存和原 URL 校验；Distribution 顶栏只有申请二维码，dialog 内复制仍可用；焦点关闭后回收；无 Provider 请求。
4. Distribution Host：四指标、无已删除指标/正文口径段、唯一顶栏动作、tabs+筛选同一控制行，IME 草稿不触发筛选，Enter/按钮仍只过滤当前加载页。
5. 实际隔离 PostgreSQL + Chromium：管理员已认证路由在 1280/1440 截图验证四卡一行、顶栏 containment、tab/filter layout、二维码 dialog 与异常订单的新数值；Provider disabled，且不触发真实写入。
6. 运行受影响 Go、Node/typecheck、build/stage、shell contracts 和 Journey。最终提交后以 `git rev-parse HEAD` 自动绑定 source/stage manifest、完整 release_files 哈希和 P5；PR CI、部署和生产受权回读另行记录。
