# 问卷支付后动作同款配置复刻

## 业务判断

- 问卷的“提交后动作”和“外部推送”是问卷自身运营能力，必须出现在问卷运营配置页，不能只存在于商品管理。
- 管理后台复刻旧版 `AI-CRM` 的字段、顺序、开关和独立保存方式：提交后动作提供“展示渠道二维码 / 直接跳转”；外部推送提供 Webhook、订阅类型、到期时间、服务周期、频率、备注和自定义参数，并保留测试推送。
- 提交成功或识别为已提交后，才执行直接跳转或展示渠道二维码。外部推送仍由既有 Survey completion effect 接受、排队、执行和留痕。
- 只影响上线后的配置与新提交，不处理历史答卷，不改商品配置。

参考行为冻结自 `qianlan33333-png/AI-CRM` 的 `dd8d60d`：`questionnaire_operations.html` 与 `questionnaire_operations.js`。v3 复用其用户可观察行为，不复制旧仓 Python 服务、身份表或运行依赖。

## 架构分类

```text
OneID: reads canonical customer only; no new identity key or matching
Persistence: Survey configuration plus Outbound-owned editable endpoint
External effect: existing Survey -> External Effects -> Outbound path only
```

- Survey 继续拥有问卷配置和完成动作；Outbound 拥有管理员填写的外部推送端点，并套用运行时签名与身份披露策略。
- 配置、端点、审计和 Outbox 在同一个 PostgreSQL Unit of Work 内保存。
- 不新增队列、Worker、重试器、Provider writer 或跨领域表访问。
- H5 跳转仅允许同源相对路径或公开 HTTPS；动态 URL Link 在服务端解析，拒绝本机、内网、重定向和超限响应。

## 验收

- 问卷运营页可按旧版布局配置、回读并独立保存两项能力，刷新后状态一致。
- 渠道二维码标题、副标题在公开完成页生效；未填写时使用旧版默认文案。
- H5 直接跳转、动态 URL Link、重复提交完成动作均使用同一安全解析结果。
- 启用外部推送后，新提交生成既有 completion effect；测试推送经过同一 Outbound Provider，日志页可查看回执。
- 浏览器旅程覆盖页面字段、保存回读、默认完成页、二维码和直接跳转；部署后以生产管理页回读和新问卷提交验证为准。
