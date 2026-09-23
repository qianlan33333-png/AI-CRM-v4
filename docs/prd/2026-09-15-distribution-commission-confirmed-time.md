# 分销用户收益：系统分账确认时间

## 问题

主线已用 `SettlementConfirmedAt` 和兼容字段 `paid_at` 表达系统分账成功确认，
但 `ListCommissionsByCustomer` 仍只按佣金聚合读取审计事件。若同一佣金存在不同
`settlement_reference` 的事件，公开收益页可能展示不属于实际结算单的确认时间。

本项收紧为：只有 `paid_minor > 0` 且审计 payload 的 `settlement_reference` 与该
佣金真实结算单匹配时，才展示系统确认时间。它不代表银行到账，也不从记录更新时间推断。

## 范围与边界

- **OneID：读取既有 canonical customer。** 已有可信会话提供既有 Customer 范围，只在
  `d.customer_id` 范围读取其佣金；本项不新增身份解析、建客、归属或合并。
- **持久化／外部效果：不涉及。** 仅改 Distribution Owner 的稳定只读投影；用户页继续消费主线已存在的
  `settlement_confirmed_at` / `paid_at` 兼容字段，不写入结算、审计、订单或 Provider。
- **确认事实：** `paid_at` 只取同一 `commission_id` 下、payload 的
  `settlement_reference` 与实际结算单一致的
  `distribution.settlement_paid.v1` 审计时间；多笔匹配结算取最后一笔（`MAX`）。
  没有匹配审计时字段缺失，页面继续显示“未记录”。
- **非范围：** 不处理收益页状态切换／加载更多的请求竞态，不扩展筛选、分页或
  数据规则。

## 真实入口与参考

| 项目 | 真实路径 | 本次处理 |
| --- | --- | --- |
| 用户收益 API | `internal/distribution/store/readmodel.go` → `internal/distribution/http/handler.go` | 将公开 CommissionPage 的确认时间限制为匹配结算单的审计事实。 |
| 用户页 | `web/v3/distributionCenter.ts` | 沿用主线已有收益页、共享状态标签和“系统分账成功确认时间”文案；本项不改布局或另建本地标签映射。 |
| 同域参考 | PR #301 `internal/distribution/store/order_read.go` | 复用按 `settlement_reference` 匹配审计的只读事实，不读取 Order／Payment 表。 |

Product Design 路由：当前 Skills catalog 未提供可读的 Product Design 资源；未伪造调用。沿用主线已有收益页的卡片、明细列表和共享状态标签，不重构布局。

## 验收

1. PostgreSQL 覆盖 `paid_minor > 0` 但没有匹配审计、同佣金匹配审计、其他佣金或
   错误结算单引用的审计、以及其他分销员客户；只返回本客户记录，且只有精确匹配
   的审计时间进入 `settlement_confirmed_at` 和兼容字段 `paid_at`。
2. 公开 HTTP 响应在有确认事实时给出 `paid_at`；没有时省略该字段，读取不因金额
   扫描失败。
3. 用户页继续使用主线“系统分账成功确认时间／未记录”展示现有 API，
   不宣称银行到账。
