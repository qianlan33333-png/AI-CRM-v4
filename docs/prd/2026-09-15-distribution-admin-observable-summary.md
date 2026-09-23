# 分销管理后台的可观察汇总与状态边界

## 业务判断

- **OneID：不涉及。** 本项只显示既有管理员授权范围内的分销、订单和佣金读取模型，不解析、创建、关联或合并客户身份。
- **持久化：新增汇总与筛选不涉及。** 新增内容只读取既有 `GET /api/admin/overview` 与分销管理读接口；筛选和抽屉状态只在当前浏览器会话内保留。页面原有的停用、异常核验、追回和商户承担仍是既有持久化命令，本项不改变其版本、幂等键、确认或失败保留合同。
- **Provider／外部效果：新增汇总与筛选不涉及。** 新增内容不调用支付、退款、分账或任何 Provider。保留的异常动作继续经过既有受控命令；未知结果仍必须读回服务端，不能以新键盲目重发。

## 参考与复用

- 复用 [PR #293](https://github.com/qianlan33333-png/AI-CRM-v3/pull/293) 的 `distribution.OverviewReader` 和 `/api/admin/overview`。该 API 已分别标注每段的状态、观察时间、范围和原因码。
- 金额、时间和结算措辞遵循 [PR #301](https://github.com/qianlan33333-png/AI-CRM-v3/pull/301)：`paid` 与 `receiver_succeeded` 仅表示**系统分账成功确认**，不能代称银行到账。结算详情只按同一 `settlement_reference` 的 `distribution.settlement_paid.v1` 审计读取 `settlement_confirmed_at`；`updated_at` 仅为记录更新时间，不能替代确认时间。
- 扩展已有 `web/v3/distributionAdmin.ts`、`shared/ui/detailDrawer` 与 admin shell 的 `--brand`、`--text`、`--panel`、`--line`、`--radius-*` 和 `presentation.css` 的 `--ui-*`。不另建色彩或布局体系，也不修改冻结 donor。
- 当前页筛选消费已审核的 `web/v3/shared/ui/committedTextSearch.ts`（来源 `9c05b9d2d21d9d637b1a0ce54978fbc61897db69`）的提交式输入边界：IME 和普通输入保留草稿，只有 Enter 或“筛选”才重绘当前页。
- 本次会话的 Product Design 路由在 Skills catalog 中不可用，未伪造其调用；实现继续受本仓的组件索引、已选后台视觉 token 和实际挂载验证约束。

## 用户可见行为

`/admin/distribution` 在既有后台鉴权内显示：

1. 可切换 `今日`、`近 7 天`、`近 30 天` 的分销汇总。期内成交、期内初始佣金、期内佣金笔数只使用 `paid_confirmed_at`；当前未结算、系统分账成功确认、待处理异常明确标注为当前状态，不随所选期间变化。
2. 每个汇总区段显示 API 的 `ready`、`zero`、`data_missing` 或 `failed` 状态和观察时间。`ready` 或 `zero` 中服务端给出的实际整数 `0` 都显示真实 `0`；`zero` 另显示“当前口径内未形成”。`data_missing` 显示“待确认”；`failed` 显示“读取失败”。缺少两个区段任一有效状态的 HTTP 200 响应同样显示可重试的读取失败，并给出“重新读取”操作，不会伪装成持续加载。
3. 分销员、订单和异常表支持**当前已加载页**的文字筛选。IME 与普通输入保留稳定草稿，只有 Enter 或“筛选”提交；它不请求所有分页、不伪造总数，也不把当前页行数称为全部记录数。
4. 摘要、表格和详情对 `null`、`undefined`、空字符串或非整数金额均显示 `—` 或“待确认”；`ready` 或 `zero` 区段中服务端给出的实际整数 `0` 显示 `0`／`¥0.00`，而不是把未知压成零。
5. 页面只使用 admin shell 中的“分销管理”标题；正文不再重复页面标题或说明。打开申请页、复制申请链接和申请二维码保留原有权限及行为，并通过共享 `pageHeaderActions` 挂在该唯一标题的右侧，不建立第二套页面头。商品售卖信息页继续不提供分销员申请入口。
6. 既有的停用、核验、追回和商户承担操作在当前请求完成前禁用触发按钮，并以会话内 in-flight 锁防止重绘后的重复提交。失败仍读回服务端且保留原幂等键。现有 `window.prompt` 参数收集未迁入统一确认组件；本 PR 不把异常处理界面宣称为已统一，后续统一组件改造需独立验收其确认和取消语义。

## 验收

- Host DOM 测试覆盖已付确认、零值未形成、`data_missing`、读取失败、IME／Enter／按钮提交的当前页筛选、概览与标签请求竞态、空值金额／时间／策略、结算审计确认时间、异常反馈，以及唯一 shell 标题、顶栏申请动作和筛选重绘后焦点保持。
- 认证 PostgreSQL Chromium 读取真实 `/api/admin/overview` 和分销表格，在后台 1280、1440 宽度检查布局；窄屏和公开分销页由各自终端的验收覆盖。本页不因列表筛选产生跨页请求，也不发起 Provider 或新的业务写入。
- manifest、stage 与资源闭包继续由既有 distribution admin entry 管理。
