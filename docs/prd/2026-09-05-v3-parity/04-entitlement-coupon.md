# PRD-04 周期权益与优惠券

状态：批准开发；先读 00-control.md；Terra xhigh。两个领域分可观察能力提交 PR，共用这一份产品合同。

## 1. 基线与分类

V3 已有 product/service-period 配置和读取、order 权益历史导入/读取/备注、coupon 规则与客户持券读取；不能以这些代替履约闭环。

旧版主要供体：`aicrm_next/extensions/commerce/service_period/` 的 application/domain/payment_consumer/refund_consumer/member_grid 及原前端；`aicrm_next/extensions/commerce/commerce/coupons/` 的业务、仓储和页面，以及 public_product 中券预占和结算协议。

OneID：读取 canonical 受益人/持券客户，不按姓名手机号新建匹配器。持久化：本地事务、内部持久处理；付款退款本身由 payment 负责，本板块不另做 Provider 写内核。

## 2. 用户流程

- 周期商品按旧版创建、编辑、分享及购买；会员数据表保留已有查询、筛选、视图、备注和协作行为，通过 V3 权限适配。不是只交付商品 CRUD。
- 已支付订单首次开通、未到期续期、已到期续期、到期读取和退款调整精确复用旧版日期、累计与退款公式，并把边界冻结为测试向量。
- 付款人和受益人分别保存；受益人不明时保留待处理事实，不误开通到付款人。
- 优惠券定义、领取入口、领取窗口、库存/每人限制、有效期、商品适用规则、持券列表和数据页复用旧行为。
- 下单时预占，确认支付核销，确认关闭释放；支付未知保持占用。退券与退款关系严格按旧版现有规则，不自行增加营销规则。

## 3. Owner 与稳定接口

- 不新建第二套 entitlement 聚合。优先扩展现有 order/port/entitlement.go 的业务能力；product 的周期定义仍归 product，coupon 的规则/持券/核销账归 coupon。
- 与 03 共同冻结窄事务 Ports：订单使用券的校验/预占、确认支付的核销、确认关闭的释放；返回内部券引用、金额快照和版本，不暴露券表。
- 支付/退款事实通过稳定 Port 或现有版本化事件交付权益处理；以来源订单行和相应结算事件确定幂等范围。
- 每次业务变化的事实、收据、审计、Outbox 同事务。待执行内部处理复用现有 River；不自建定时器或状态机。
- 到期语义先复用旧版时间比较；仅在确有持久到期动作时复用已有内部任务机制，不为状态显示新增周期任务。

## 4. UI 与历史数据

原样复用 service_period 和 coupon 页面，最小 Host/DTO/鉴权改造；数据表每一个旧版可见写动作必须有后端或明确已有禁用行为，不能以空响应冒充实现。

实现历史权益、开通/续期记录、券定义、领取及使用事实的 inspect/dry-run/apply/reconcile 测试工具；本轮只在隔离库执行。保留来源主键、时间、金额和逐行结果。缺可信客户映射进入 unresolved；不从历史 paid 订单自动生成新权益。

## 5. 验收

- 从旧代码冻结未到期/已到期续期、同日边界、不同长度月份、部分/全部退款、重复与乱序事件的期望结果。
- 领取并发最后一张券、每人上限、有效期边界、不同商品资格、重复领取与 payload 漂移。
- PostgreSQL 预占与订单写失败一起回滚；确认支付只核销一次，unknown 不释放，关闭释放重复安全；权益重启恢复只履约一次。
- 旧版会员数据表与券页面 journey 对照；历史记录数量/金额/时间范围/结果桶对账。
- 与 03 集成的公开购买→支付→核销→开通/续期→退款调整完整测试通过。
- 提交独立 PR 和测试证据；不调用真实会员写接口、不修改生产数据、不触发真实退款。

## 6. 已核对的旧规则与共同接口线索

- coupon/domain.py 的 relative_days 按 Asia/Shanghai 自然日计，领取当天为第一天，结束边界不含；不能改为领取时刻加 N 个24小时。
- coupon/repo.py 预占要求折扣严格小于商品原价、币种相同、有效期覆盖当前时刻；自动选择按折扣降序、到期升序、领取时间及ID排序。确认支付核销前校验 Provider 实付与币种；确认关闭释放时已经过期的券回到 expired，不能重新变 available。
- service_period/repo.py 的旧实现：仍有效时从原 end_at 延长；否则以原订单 paid_at 起算。已进一步核对 commerce/admin_transactions.py 的 _validate_refund_request：允许大于零且不超过可退金额的部分退款，没有周期商品必须全额退款的分支。refund.succeeded 事件和 service_period/refund_consumer.py 也没有全额过滤。故冻结旧行为：来源订单首次成功退款按原开通/续期天数撤回一次；没有其他未退订单时 end_at 取处理时间并标记 refunded，有其他订单时 end_at 减来源天数且不早于处理时间。后续同订单退款不再重复扣减。不能自行增加按退款金额比例折算规则；测试需明确部分退款与全额退款的相同期扣减语义。
- V3 已有 order/port.PaymentCoordinator.CreatePaymentOrderWithin、SettlePaymentWithin，以及 PaymentOrderCommand 的独立 PayerCustomerID/BeneficiaryCustomerID；03/04 应扩展这一窄边界，不新建第二条结算流水线。

## 7. 实现预审必须覆盖的恢复与兼容反例

- 领取/预占同一 operation、actor scope、客户端键并发时返回同一结果；不能因先检查收据、后等业务行锁而使第二请求误报库存耗尽。不同合法scope相同客户端键不发生全局source_key碰撞；同键业务载荷漂移必须冲突。
- 券预占快照同时冻结product_type、product_id和product_code，防止普通/周期商品的ID空间混淆；核销验证权威实付与币种。
- 同一paid事实重启后以不同处理时间再执行，仍返回原发放；ProcessedAt不作为业务载荷摘要的一部分。同一来源订单的第二次合法部分退款应正常完成且权益不再扣减，不因退款金额/处理时间不同导致支付结算事务失败。
- 已导入且仍有效的历史权益，经过一笔新续期再退款时，历史未退款来源仍参与计算；不能只统计本次新建履约收据而把旧权益清空。历史事实由Order已有Owner数据读取，迁移不触发新发放。
- 同客户/同周期商品的两个不同订单同时首次支付，需要在既有PG事务中正确累计；首次无聚合行时也不能丢一笔合法开通。
- 退款后的updated_at记录处理时间，end_at记录业务到期时间，二者不能复用同一SQL参数。
- 禁止用累计权益end_at-start_at推算last_order_id的购买天数。历史来源映射必须来自订单/逐次开通记录的实际天数，并核对可信受益人、周期商品、订单行和支付状态；缺证据保留未解析。迁移只能记录已对账事实，不能补造grant。累计62天而最后订单31天、同客户不同商品、未支付订单及矛盾映射均需反例测试。
- PR138的Coupon Claim/Reserve摘要包含服务端补入的当前时间，真实HTTP同键重试会因时间变化冲突。联合实现必须分离业务幂等载荷与执行时钟，冻结首次时间；Claim/Reserve同键跨时刻返回原结果，不能只测固定时间的并发。Consume/Release也核对权威事件时间与本地处理时间是否区分，重启不得因本地时钟变化破坏重放。

## 8. 联合交付不可遗漏的公开领取入口

供体 `aicrm_next/extensions/commerce/commerce/coupons/public_api.py` 已有 `/c/{public_slug}`、`GET /api/h5/coupons/available?target_ref=...`、优惠券状态读取与 `POST /api/h5/coupons/{public_slug}/claim`。因此仅提供内部ClaimApplication不算完成领取能力。联合03/04任务应复用旧coupon_public.html，在V3 Host适配既有微信OAuth会话和可信OneID，领取不接收客户端CustomerID，保持幂等、Cookie/CSRF等现有安全边界，并接购买页的可用券展示/选择。无可信身份不领取；不新建第二套OAuth或客户匹配。

旧分享slug、过期/售罄/每人限额/已领取状态、仅微信内领取提示及数据页统计按供体冻结；Admin表单/列表已有页面不重写。PR138已实现内部领取与结算Port，以上公开流程仍需后续联合PR完成。

## 9. 联合审核剩余条件

PR138 的领域修复已通过真实 PG 审核并纳入集成，但不代表会员表、公开购买与历史验收已完成。PR143 的具体退回要求以 PRD03 第9节为准：消除会员表空实现、实际复用周期公开模板、统一自动用券文案、保持未知购买的原幂等键、补签名支付回调与权益的真实 PG 联合旅程、完成隔离历史导入对账。以上均属于本 PRD 原定能力。
