# 运营闭环 Tab：真实 Handler 的反馈层所有权声明

**状态：** 已批准实施（独立窄修复）
**基线：** `e416d0c3e11703b887c55b7caf96f89e8ca55595`
**用户入口：** `/admin/operation-cycles#strategy=excel.deployment.acceptance.20260910`
**实现边界：** `web/v3/excelBatches.ts` 的 V3 action factory，以及现有 `scripts/excel-batches-dom-test.mjs` 的真实共享反馈层回归

## 用户问题与证据边界

独立 UI 审计在只读切换“内容准备与发送”和“发送效果与复盘”时，先看见完整提示“后端能力未就绪：该操作不可执行”，随后 Tab 内容正常加载。没有点击审核、发送、上传、替换或任何写入动作。

源码可解释该现象：`web/v3/excelBatches.ts` 的 `action()` 为所有本页按钮设置真实 `onclick`，其中两个 Tab 的文字都含“发送”；但没有在节点加入 DOM 前设置反馈层的 `__dcBound` 所有权标记。不可变共享反馈层的 capture 阶段 delegate 会先把含“发送”的未声明按钮分类为 `backend_blocked` 并 toast；事件继续传播，节点自己的 `onclick` 随后调用真实 `loadSelected()`，因此页面恢复。

这不是后端能力、OpenAPI 或 Provider 故障，也不是应当隐藏的真实能力缺失。根因是已接入的 V3 本地交互被共享未接管动作 guard 错误分类。线上 UI 观察与上述源码链相互印证；本 PR 不把它泛化为其他页面的后端可用性结论。

## 架构分类

| 检查项 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不涉及。 |
| 持久化 / 内部持久任务 | 不新增或修改。仅标识既有浏览器 click handler 的所有权。 |
| Provider 写入 / 外部效果 | 本 PR 不创建、发送、重试或回读任何外部效果。既有 Excel 批次 API 合同不变。 |
| 权限 | 不扩大任何权限，也不绕过服务端 CSRF、版本、预览、审核或发送检查。 |

## 实际调用链与根因

`/admin/operation-cycles` 由 WebShell 的 operation-cycle Host 承载，V3 `excelBatches.ts` 在其中渲染 `.operation-excel-workspace`。`action(text, run, kind)` 创建按钮后在 `onclick` 内通过既有 `runAction()` 调用传入的真实 local/API handler。`renderDetailShell()` 用该 factory 创建两个 Tab，Tab handler 更新 `this.tab`、更新导航并调用 `loadSelected()`。

不可变 donor 的 canonical source 为 `web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/shared/ui/feedback.ts`：

- `initFeedback()` 对新增按钮做能力分类，并在 document capture phase 注册 delegate；
- delegate 对未标记的、文字匹配业务动作词表（包括“发送”）的按钮显示不可执行 toast；
- `__dcBound === true` 是既有唯一跳过条件。

`web/v3/couponAdapter.ts` 已在运行时完成真实绑定后为精确受管控件设置 `__dcBound`、`data-capability-state=real` 并去除旧 aria 描述。这是本仓库已验证的所有权声明先例。合并 PR #223 已交付 Excel 运营批次能力；本变更只修正它在共享反馈层下的接线时序，不复制或改变其业务合同。

## 范围

### 本 PR 包含

1. 在 `action()` 创建 V3 Excel workspace 的每个真实 handler 按钮时，立即声明 `__dcBound=true`，并按既有 coupon 先例标为 `data-capability-state=real`。
2. 声明必须发生在节点插入 DOM 前；不能等到 `runAction()`、API 请求或点击后再标记，因为共享 delegate 已在 capture phase 运行。
3. 把真实 immutable donor feedback 与 Excel workspace 同时挂载到 jsdom 契约：两个 Tab 点击不会 toast、仍会触发 `loadSelected()` 的读路径；一个未声明的“发送”按钮仍被 guard 拦截并显示不可执行 toast。

### 明确排除

- 不修改 canonical donor 或任何 materialized donor view，不全局弱化 `BUSINESS_ACTION_RE` / delegate。
- 不为无 handler 的按钮加标记；不能把真正未接入的能力伪装成可用。
- 不改 operation batch API、服务端状态机、审核/预览、版本/幂等、封面门禁、行编辑、历史读取、外部发送或 Provider 回读。
- 不把此 PR 与客户列表竞态、HXC 安全引用、渠道 API 暴露或 Excel 数据分页重构合并。

## 交互合同

| 控件类型 | 行为 |
| --- | --- |
| Excel V3 `action()` factory 创建、具有实际 `run` handler 的按钮 | 在加入 DOM 前声明为已接管；capture guard 不 toast；原有 handler 和 busy 生命周期照常运行。 |
| 两个详情 Tab | 切换选中样式并触发原有 `loadSelected()`；只显示加载或真实读错误，绝不短暂显示“后端能力未就绪”。 |
| 没有 V3 handler 的业务动作按钮 | 保持 donor guard 的 `backend_blocked` 分类和提示；不发送请求。 |

## 前端一致性与 donor 边界

继续使用现有 WebShell、Excel workspace、共享反馈层和 `runAction()`。不增加页面、组件、样式或文案，不修改冻结 donor。所有权标记复用 coupon adapter 的现有前端治理模式，范围仅限实际由 Excel V3 factory 创建的按钮。

## 验收与回归白名单

| 场景 | 预期 |
| --- | --- |
| 真实 feedback 先安装，随后挂载 Excel workspace | 新建的 Excel action 按钮在插入时就是 `__dcBound` / `real`。 |
| 点击“内容准备与发送”或“发送效果与复盘” | 不显示 `后端能力未就绪` toast，且原有 selected state 与 `loadSelected()` 读取照常发生。 |
| 点击未声明的“发送”按钮 | 仍显示不可执行 toast，且不出现 API 请求。 |
| 现有 Excel DOM 行程 | 继续覆盖审核预览、封面、行编辑、回读与版本行为。 |

定向验证：

```text
node scripts/excel-batches-dom-test.mjs
node scripts/excel-batches-pagination-dom-test.mjs
npm run typecheck
```

浏览器线上回读须在部署后独立执行；本地契约不等同线上发布验收。
