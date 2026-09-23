# 分销后台异常操作统一确认框

## 目标

将分销管理中的浏览器原生 `prompt` 收敛到已上线的共享 `window.AICRMConfirmation.confirm` / `openConfirmationDialog`。停用分销员、登记追回、登记商户承担在提交前展示同一套可访问的确认框；取消、关闭或字段校验失败均不发起 Owner 写入。

## 业务判断与边界

- 操作人是已认证且已授权的后台管理员。页面只在当前列表或异常记录声明该操作可用时显示入口；服务端仍执行权限、对象状态和 CAS 校验。
- 停用分销员需要审计原因；启用沿用现有行为，不新增加确认流程。
- 异常“查询最新结果”沿用既有直接读取/对账行为，不新增确认流程。
- 登记追回必须填写 `amount_minor` 的正整数金额，单位为**分**，以及凭证参考；登记商户承担必须填写正整数分金额和原因。
- 对话框确认时冻结调用时的对象 ID、版本与动作；打开后发生列表切换、授权 epoch 变化、目标失效或同一动作已在处理中时，确认不会投递到新的对象。取消零写入。
- 对话框只编辑、校验并返回临时字段。现有 Distribution Owner 继续负责金额解析、HTTP、CSRF、幂等键、CAS、读取回执与失败后的读回。未知结果继续使用原幂等键，不改变 Provider、支付或外部效果边界。

## 架构分类

```text
OneID: not involved — 本能力仅处理既有分销员/异常记录的已授权后台命令；不解析、创建、关联或合并客户身份。
Persistence: local transaction — 对话框不持久化；既有 Distribution Owner 命令维持原有事务、审计与 CAS。
External Effects: not involved in this UI change — 不新增 Provider 调用、队列、效果或重试；未知写入结果由既有命令使用原键读回。
```

## 前端基线与共享组件

- 终端：后台 `/admin/distribution`，使用现有 `admin_base` 单壳、分销列表/详情抽屉和状态反馈；不引入第二壳或原生对话框。
- 复用：`web/v3/shared/ui/confirmationDialog.ts` 与 `confirmationDialogHost` 的公共桥接。共享层扩展为通用临时字段编辑和校验，兼容既有 `reason` 调用，不能处理领域写入。
- 调用页：分销管理；Audience 的四个既有 caller 必须保持默认行为并回归。
- Product Design：本会话 Skills catalog 未提供可读取的 `product-design:*` 路由；不安装或伪称使用，沿用已审核的共享确认框视觉和可访问性基线。

## GitHub 参考

- [PR #332](https://github.com/qianlan33333-png/AI-CRM-v3/pull/332)：采用共享确认框的临时结果、required reason、焦点回收、Escape/取消零写和 caller 冻结目标模式。
- 不采用其 Audience 领域命令：分销仍使用自己的 HTTP 路径、版本、Idempotency-Key 和 Owner 读回，不能复用或跨写 Audience 数据。

## 验收

1. 停用、追回、承担分别验证取消零 POST、确认恰好一次 POST、必填与金额分校验、以及原始请求路径和正文。
2. 同一按钮重复点击、确认后重复提交、列表/授权变化和目标失效不会重复写入或误写新对象；失败保留原幂等键并按既有读回语义展示。
3. 现有 Audience 四个确认 caller 回归，确认框的 reason 默认 API 不回退。
4. 通过受影响 Node/typecheck/build，及真实隔离 PostgreSQL + Chromium：合成数据下取消零写、确认一写、失败仍复用同一键；不访问生产或真实 Provider。
5. 在 1280/1440 后台视口核对确认框、抽屉、失败和焦点状态。
