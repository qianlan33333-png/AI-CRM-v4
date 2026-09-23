# 优惠券中文展示与上海时间

## 业务判断

管理员在优惠券一级列表需要判断规则能否领取、适用哪些商品、开放领取的时间范围。现有页面把持久化的 `target_refs` 直接作为商品名称，且把规则的 `claim_starts_at` / `claim_ends_at` 标为“领取时间”，会让用户误认为这是某位客户已经领取的事实。

本项将列表列名定为“领取时间范围”。它只展示 Coupon Rule 的领取开放区间；客户实际 `claimed_at` 仍由领取明细拥有和展示。状态、商品不可用和读取失败均以中文呈现，但 HTTP API 的状态 enum 与错误 code 保持不变。

## 分类与边界

- **OneID：不涉及。** 本项没有读取、解析或改变客户、外部身份或归属。
- **持久化：既有本地读取。** 不新增 Coupon 记录、迁移、内部任务或幂等收据；Product 在自己的 PostgreSQL Unit of Work 内执行只读投影。
- **外部效果：不涉及。** 不调用支付、退款、Provider 或 outbound，不改变优惠券领取、创建或发布状态。
- **跨领域边界：** Coupon 只传递自己的带类型目标引用，Product 通过稳定 `ProductTargetBatchReader` 返回最窄的 `name` / `found` 投影。Coupon 不访问 Product 表，也不凭引用猜测名称。

## 已核对参考

- [PR #22](https://github.com/qianlan33333-png/AI-CRM-v3/pull/22) 已确立 Coupon 使用 Product 稳定 Port。这里延续该边界，并将逐个选项读取改为 Product Owner 的有界批量读取。
- [PR #256](https://github.com/qianlan33333-png/AI-CRM-v3/pull/256) 统一管理端的上海时间展示和输入转换。本项复用同一日期 helper，不修改冻结的 `web/src/api/admin.ts`。

## 规则与实现

1. `GET /api/admin/coupons` 和详情读取返回可选 `target_products`。每项严格对应一个原始 `target_ref`，状态只有 `available` 或 `not_found`。`not_found` 表示 Product Owner 确认该历史目标不存在；Product 查询错误使整个 Coupon 读取返回 `503 unavailable`，不能伪装成删除。
2. Product Owner 用一次、带类型的 `ANY` / `UNION ALL` 查询完成一页目标投影。单页上限沿既有 `200` 条规则和每券 `100` 个目标计算为 `20,000`；不循环创建 Product UOW 或单项查询。停用、归档的原类型商品仍可返回其历史中文名称；相同 ID 的另一类型不得串名。
3. 一级列表显示真实商品中文名；确认删除显示“商品已删除或不可用”；读取异常显示明确的暂不可用状态。页面不得显示 `standard_product:<id>` 或 `service_period:<id>` 作为名称，也不得编造名称或全商品语义。
4. 一级列表显示 `YYYY-MM-DD HH:mm:ss` 的上海领取时间范围，标题为“领取时间范围”，不含“北京时间”。券状态映射为中文；原始 API enum 保留给程序使用。390px 下表格横向滚动，列不截断。
5. Coupon Host 在实际编辑表单边界处理 `claim_starts_at`、`claim_ends_at`、`use_starts_at`、`use_ends_at`。输入被解释为上海壁钟时间并发送 RFC3339 instant，因此浏览器本地时区不会改变结果。未修改的编辑原样保留已有 instant 与小数秒精度；未挂载编辑器的直接 API 调用不被 Host 改写。

## 验收

- 后端测试验证 Product 批量 Port 只有一次 UOW/查询边界，包含停用普通商品、归档周期商品、缺失目标和同 ID 类型不匹配。
- Coupon HTTP 测试验证多券多目标的一次批量投影、读取失败的 `503 unavailable`、最大合法 `200 × 100` 目标页。
- 浏览器 DOM 测试验证匿名管理员视图的真实中文商品名、删除目标、中文状态、上海时间范围、无技术引用和 390px 横向滚动；本地过滤不重新请求统计或列表。
- 浏览器时区测试在 UTC 与 America/Los_Angeles 下提交同一四个上海壁钟输入，断言发送相同 RFC3339 instant；编辑未变更时保留原始小数秒。
- OpenAPI 的 `target_products` 是权威契约变动；P5 authority ledger 仅在根代理完成最终 diff 审核、明确 base/head 后登记。
