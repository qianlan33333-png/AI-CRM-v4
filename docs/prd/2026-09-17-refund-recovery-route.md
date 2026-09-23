# 退款账号核验路由修复 PRD

## 1. 生产问题与业务判断

- 生产订单 `v3pay_MJNQDGLORVJPJ7GVPYP6OIZ34U` 是原生微信支付订单，订单与 Payment 均为 `paid`，支付金额与当前可退金额均为 299 元，微信 `transaction_id` 已保存。
- 生产只读核验显示该订单没有退款记录、退款 Provider Intent 或退款 External Effect，因此本次错误没有产生退款，也不存在需要重放或补偿的外部效果。
- 管理端提交退款前，会以当前登录 Admin、Payment 与一次性探测键调用 `GET /api/admin/refunds/recovery`，取得不含 Admin 原始 ID 的 `actor_binding`，用于把浏览器中的退款草稿锁定到同一登录账号。
- Payment Handler 已实现并鉴权该接口，但 Composition Root 的内部 Payment admin mux 与外层应用 mux 都只挂载了精确路径 `/api/admin/refunds`。Go `http.ServeMux` 中不带尾斜杠的路径是精确匹配；它不会覆盖 `/api/admin/refunds/recovery`。生产请求因此返回 404，前端按失败关闭退款提交并显示“当前登录账号暂不可核验”。

参考：Go 官方 GitHub 源码对 `ServeMux` 的说明明确指出，路径末尾斜杠才代表子树匹配；例如 `/images/` 会匹配该子树，而精确路径不会自动拥有子路径。见 [golang/go `net/http/server.go`](https://github.com/golang/go/blob/master/src/net/http/server.go#L2699-L2724)。

## 2. 开发前分类

```text
OneID: not involved；退款账号核验使用已认证的 Admin principal，不读取、解析、创建或合并客户身份。
Persistence: stateless authenticated read；recovery 仅读取 Payment 自有幂等收据，当前修复不写数据库。
External Effects: existing refund Provider write remains involved but unchanged；实际退款继续复用 payment-owned intent + External Effects，当前修复不接受、不排队、不重试任何退款效果。
```

## 3. 目标与非目标

### 目标

1. 将精确路径 `/api/admin/refunds/recovery` 同时挂载到内部 Payment admin mux 和外层应用 mux。
2. 保持接口自身的 Admin session 鉴权、Payment + actor + idempotency key 三重范围约束不变。
3. 未登录请求必须到达 Payment Handler 并返回 401，而不是在路由层返回 404。
4. 管理端可取得稳定 `actor_binding`，从而继续现有退款确认流程。

### 非目标

- 不放宽退款角色、CSRF、金额、`transaction_id` 或写开关校验。
- 不为 `/api/admin/refunds/` 挂载宽泛子树，不引入未审计的新退款端点。
- 不自动发起当前生产订单退款；真实退款仍必须由用户核对后明确提交。
- 不更改 Provider 幂等键、队列、Worker、回调或对账状态机。

## 4. 验收标准

1. 路由单测证明 recovery 精确路径在两层 mux 都归 Payment Handler，未知相邻路径仍为 404。
2. Payment Handler 既有测试继续证明未认证为 401、同 Admin/Payment/key 才能读取收据。
3. 前端退款测试继续证明 actor 变化、未知结果和重复提交均 fail closed。
4. 生产部署后：未认证 recovery 返回 401；认证页面不再提示账号不可核验；页面仍要求再次输入微信交易单号和勾选人工核对。
5. 未经用户明确点击，不新增 `payment_refunds`、`payment_provider_intents` 或退款 External Effect。
