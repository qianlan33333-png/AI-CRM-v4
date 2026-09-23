# 交易历史生产只读发现报告

日期：2026-09-03
目标：为一次性迁移 `150.158.82.186` 的历史用户身份与交易事实建立不含 PII 的源基线。

## 结论

当前只读入口只暴露了身份宇宙和微信支付订单视图，不能覆盖退款、微信小店、支付宝，所以全量迁移门禁未通过。已生成的本机 `0600` 快照只用于保留已授权事实，不可作为 apply 输入，也没有进入 Git。

## 已验证聚合

| 数据集 | 行数 | 关键对账 |
|---|---:|---|
| `audience_read.identity_universe_v1` | 25,649 | 24,255 个唯一身份；1,369 个 person |
| `audience_read.orders_v1` | 769 | 订单金额 95,518,218 分；607 paid、161 closed、1 paying |

微信支付订单视图未发现空订单键、重复订单键、负金额或缺失身份；44 行缺少 `external_userid`，但视图内仍有其他 identity key。首笔支付时间为 2026-05-18，最新为 2026-09-03。

## 已确认但不可读的源事实

PostgreSQL catalog 可确认以下表存在，但受限账号直接查询返回 permission denied：

- `wechat_pay_refunds`
- `wechat_shop_orders`
- `wechat_shop_refunds`
- `alipay_pay_orders`

在这些数据集获得列白名单只读视图或 `SELECT` 权限前，`coverage` 不得标成完整，`migrate-commerce-history --mode=dry-run` 和 `--mode=apply` 会失败关闭。

## OneID 门禁

- `external_userid` 只能使用已确认 Corp scope。
- 小程序/Open Platform 身份必须使用各自 App/Open Platform scope。
- 当前生产设置没有可确认的微信开放平台 scope，因此 UnionID 行只能留在未解析桶，不能猜测 scope、跨渠道合并或自动建客。

## 快照证据

本机已留存一份部分覆盖原始快照，权限 `0600`，SHA-256 为 `41e38fc37d9aeffb746f583ef26bd4f193db3335e48eb65ef54f1945a994b3ea`。该文件含 PII，不提交 Git、不通过聊天传输，并且由于缺少退款/小店/支付宝覆盖，明确禁止 apply。

## 解除阻塞的最小条件

1. 为受限只读账号提供上述四张表的列白名单视图，或授予只读 `SELECT`。
2. 确认微信开放平台 scope ID；若业务从未绑定开放平台，应明确确认 UnionID 不参与本次建客。
3. 提供面向 v3 生产的受控快照上传/迁移执行通道；当前 `crm-prod` 强制命令只读，不能上传或执行迁移器。

任何一项未满足时都禁止部分 apply，避免身份漏迁、财务行静默丢失或形成不可对账的半迁移状态。
