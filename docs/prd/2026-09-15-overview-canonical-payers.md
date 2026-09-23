# PRD：运营首页付款客户按当前 OneID 根去重

日期：2026-09-15  
范围：修正管理端运营首页已付款客户数。付款事实继续保留支付发生时的 `payer_customer_id`；展示计数改为这些历史付款方在本次读取快照下所归属的当前 canonical Customer root 去重数。

## 业务判断

`payments.payer_customer_id` 是支付发生时不可变的归属事实。客户后来合并后，不能重写它：既有 Payment 的 abandoned-checkout 授权已经依赖保留原 payer 并通过 OneID lineage 判断当前授权。因此，两个历史付款方后来合并到同一个 root 时，运营首页把两人计为两个“canonical payer”是错误的；金额、订单数和付款时间仍必须按各自原始 Payment 事实计算。

本 PR 只修复一个只读计数能力。它不增加付款、退款、客户创建、身份解析/建客/合并、历史 payer 回写、持久任务、Provider 读取/写入或 External Effect。

```text
OneID：仅通过新增的窄批量 canonical-root Read Port 读取当前合并状态；不 Resolve、Provision、Link 或 Merge。
持久化：只读 PostgreSQL 事务；不写 Payment、Customer、Identity 或审计表。
外部效果：不涉及。
```

## 已核实问题与参考

- `internal/payment/store/overview.go` 的 `paid_in_range` 已正确使用 `status='paid'`、可信 `paid_confirmed_at` 和半开区间；但 `COUNT(DISTINCT payer_customer_id)` 被直接命名为 `DistinctCanonicalPayers`，没有经过当前 OneID root 归并。
- Payment 明确保留合并前 payer。`TestMergedPayerCanReadAbandonedOriginalWithoutReassignment` 证明这是受保护的历史事实，不能为报表改写。
- Identity 已拥有 `customers.status` 与 `merged_into_customer_id`，并已有严格的批量递归根解析范式：内容雷达的 #263 `adminRadarVisitorCanonicalRoots` 会拒绝缺根、断链、环、非法终点和超过 127 跳的链，不能将它们静默成空或任选一个 root。
- GitHub code search 没有发现可复用的 `CanonicalCustomerRoots` 或已修复的 `DistinctCanonicalPayers` 实现；采用仓库已合入的 #263 batch-root 范式，而不复制 Radar 的外部联系人展示 Port。

## 读链、分页与快照语义

1. Composition 为 Payment overview 注入专用的、只读 `REPEATABLE READ` Unit of Work。它不替代任何写路径的普通 UoW；Payment overview 和 Identity root 批量读取在同一事务上下文中执行。
2. Payment Store 保持一条 `paid_in_range` CTE statement，原有 gross、业务订单数、趋势和缺少确认时间诊断均来自这个 statement。该 statement不聚合或截断 payer ID。另有 Payment Owner 的历史 payer keyset 读取：以 `payer_customer_id` 严格升序、`payer_customer_id > after_customer_id` 查询同一可信付款谓词，每页最多 `500` 个去重 ID；游标只由最后一条已读历史 payer 形成，直到真实空页结束。
3. Payment app 在同一只读 RR 事务内，逐页调用上述 Payment Owner 读取，再把每页最多 `500` 个 CustomerID 交给新的 Identity `CanonicalCustomerRoots` Read Port。Port 返回每个输入历史 ID 的当前 root；app 对累积 root 去重计数。它不按 payer 逐条查询、不用无界 JSON 聚合，也不会在任意业务数量处截断后仍返回 `ready`。
4. 分页受既有 overview section 的请求 context deadline（当前为 2 秒）和 PostgreSQL 事务资源约束：在真实结束前取消、超时、连接/事务资源失败时，付款区段明确返回不可用/未知，绝不保留部分 root 计数或默认 `0`。今天、7 天、30 天和任意 custom 范围均使用此稳定 keyset 路径；大于 10,000 个 payer 的正常请求仍必须精确完成，只要在该请求预算内完成。
5. 空 payer 集直接得到 `0`，不调用 Identity 或任何 Provider。`payer_customer_id IS NULL` 仍只计入既有 `MissingPayerCount`，并使付款区段保持 `data_missing`；它不被猜测成 root。
6. 任一无效 payer ID、keyset 倒退/重复、Identity 返回遗漏、缺根、断链、环、非法终点、超过 127 跳或读错误，均使付款区段不可用/未知。不得保留部分 root 计数，也不得把错误降格为 `0`、`missing payer` 或旧 raw distinct count。

`REPEATABLE READ` 的 as-of 定义为 Payment overview 事务的第一个数据库读取快照：同一个响应中的 Payment 历史集合和 Identity 当前 root 都按该时点一致读取。该快照开始前已提交的 merge 会计入；开始后才提交的 merge 在下一次 overview 请求才体现。不同领域的其它首页 section 仍按各自独立 read UoW 观察，不能据此推导跨域串行快照。

Payment Store 仍只查询 Payment-owned `payments`；它不 join `customers` 或 `customer_identities`。Identity 的实现只查询 Identity-owned merge state。两者只经稳定 Port 和 Composition Root 相连，绝不回写历史 payer。

## 稳定合同

Identity 新增只读、无身份值泄露的 Port：

```go
type CanonicalCustomerRootsReader interface {
    CanonicalCustomerRoots(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerdomain.CustomerID, error)
}
```

它接受 1–500 个正 CustomerID，输入重复先去重；结果必须覆盖每一个输入。实现沿用现有递归链完整性检查，不暴露 OpenID、UnionID、手机号、identity ID 或 Provider 数据。

Payment 的内部 Owner contract 新增 keyset `PaidOverviewPayerPage`，但 HTTP 映射不暴露任何历史 payer ID 或游标。`DistinctCanonicalPayers` 只在全部页和全部 root 成功解析后赋值。分页未结束即超时、或 root 不可用时，Overview app 以明确 reason 呈现付款 section 的未知状态，且不让 Host 将默认 `0` 当作真实人数。

原有合同保持：

- `Gross`、`OrderCount`、`Trend`、`MissingConfirmationEvidence*` 的事实谓词、时间窗、币种和去重方式不变；
- `MissingPayerCount` 仍表示原始 Payment 中 payer 为 `NULL` 的业务订单数；
- 付款发生时的 `payer_customer_id`、订单数和金额不改变；
- 不增加任意时间范围接口或下钻。本 PR 完成后，才处理独立的首页同口径明细抽屉；在此之前不得以订单 `created_at` 或外部订单 native-only 时间替代 `paid_confirmed_at`。

## 验收

1. 真实 PostgreSQL composition fixture：两笔在窗口内的 Payment 分别记录 payer A、B；A 后来合并到 B。响应仍保持两笔订单、原金额和原付款时间，但 `distinct_canonical_payers=1`；数据库中的两条 `payments.payer_customer_id` 仍分别为 A、B。
2. 同一 fixture 加入未合并 payer C 后 canonical payer 数为 2；`NULL` payer 只增加 `missing_payer_count`，不调用 OneID 也不补 root。
3. Identity Port 的 PostgreSQL 测试覆盖批量 500、重复输入、缺根、断链、环、非法终点与 127/128 跳边界；Payment app 测试证明每页、每次 Identity 调用最多 500，且没有 N+1。
4. 构造超过 10,000 个不同 payer 的 PostgreSQL fixture，验证稳定 keyset 不重不漏并精确完成；人为取消未结束分页时，付款区段为明确不可用/未知，没有 `ready`、没有部分 count，也没有把数值显示为零。
5. 并发 merge 的事务测试证明同一次 overview 的 Payment 集合和 canonical roots 采用同一个 `REPEATABLE READ` as-of；提交在快照后的 merge 仅影响下一次读取。
6. 回归现有 Payment overview 的可信历史付款时间、半开区间、退款诊断和 UI/API 授权；完整 consumer、PostgreSQL 及 Chromium overview journey 分别记录。

## 后续顺序

本计数设计与真实 merge fixture获审后，先以独立 Owner 小修补 `internal/distribution/store/order_read.go` 顶层 `SettlementConfirmedAt` 的 settlement-reference 匹配，再开始首页同口径明细下钻。该顺序避免把已知错误的“最近分账确认时间”提前当作下钻事实。
