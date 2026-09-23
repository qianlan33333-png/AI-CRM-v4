# 订单分销只读展示

## 目标

管理端订单列表给出集中、可扫描的分销摘要；订单详情按订单商品行展示该笔订单冻结的分销事实：分销员显示名、佣金比例、退款复核等待天数、初始／当前应付／已付佣金、状态和原因、预计可结算时间、分账成功确认时间，以及多笔分账、调整和异常证据。

订单详情只能使用订单归因时冻结的策略快照，不能以商品当前策略替代。`due_at` 显示为“预计可结算时间”。`distribution.settlement_paid.v1` 的审计发生时间显示为“分账成功确认时间”；它是系统确认事实，不是银行或 Provider 实际到账时间。没有该事实时显示“未记录”。

## 行为

- 非分销订单显示“非分销订单”，不请求或扫描分销管理列表。
- 已归因但尚未形成佣金的订单只显示“已归因 · 未形成佣金”；它不推断支付或分账仍在等待。
- 零佣金、退款调整、冻结、异常、已付和多商品／多分销员逐行展示，金额保持 CNY 最小单位转换后的准确值。
- 分销读取暂不可用时展示“分销信息暂不可读取”，不影响订单原有详情、退款读取或退款命令。
- 订单列表一次按当前页的 Order ID 批量读取；详情只对当前已授权订单读取。`ReadAdminOrderDetail(attributionID)` 继续仅服务分销管理详情，绝不把 Order ID 当 attribution ID 使用。
- 商户单号只在支付渠道内唯一。列表行使用服务端返回的 `detail_url`；详情按 `(provider, merchant_order_no)` 定位，旧的无 provider 链接保留既有歧义安全语义。错误或重复 provider 不退回默认渠道。
- 分销摘要以 canonical Order ID 关联。冻结 renderer 不保留该 ID 时，V3 Host 只用当前响应的私有 token 关联；缺失或不匹配 token 必须清空摘要和详情链接，不能复用上一页或旧响应。

## 边界和分类

OneID：**读取 canonical 客户显示名**。Distribution 在自己的读事务结束后经既有 `customer/port.DirectoryDisplayNameReader` 批量取得展示名；不解析、创建、绑定或合并身份。

Persistence：**既有 PostgreSQL 只读模型**。新增 Distribution owner 的稳定批量 Read Port；不新增表、写命令、事务、任务、队列或重试机制。Order 在自己的授权通过后组合该安全投影。佣金、调整、结算、异常和审计确认时间须在同一 PostgreSQL statement snapshot 中读取，不能在 READ COMMITTED 事务内拆成多次读取而混合并发分账前后的事实。

Provider / External Effects：**不涉及**。不调用 Provider、不创建外部效果；分账时间只读取 Distribution append-only audit fact。退款流程和支付资金规则保持不变。

## 现有实现参考

- GitHub 历史 `dcfc0229 feat(distribution): implement first-level referral lifecycle` 的冻结归因、佣金、调整和结算模型。
- GitHub 历史 `8d6353d3 fix: add distribution application entry and admin details` 的 Distribution owner admin detail；其 `ReadAdminOrderDetail` 参数是 attribution ID，因此本能力另设 Order-ID 批量 Port。
- 当前 `web/v3/orderAdapter.ts` 的订单详情事实分区和 `web/v3/shared/ui/detailDrawer.ts` 的共享后台详情容器样式；本次复用这些呈现约定，不修改冻结 donor。

## 兼容性

公共分销佣金列表新增 `settlement_confirmed_at`；它取 `distribution.settlement_paid.v1` 的系统确认时刻。现有 `paid_at` 暂继续输出同一事实作为已弃用兼容别名，客户端不再将其称为到账时间。
