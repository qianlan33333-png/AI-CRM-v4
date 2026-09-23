# 商品支付后动作与外部推送旧版一致性 PRD

## 结论与范围

本 PR 交付一个用户可观察能力：普通商品与周期商品在 V3 管理端按旧 AI-CRM（commit [`dd8d60d`](https://github.com/qianlan33333-png/AI-CRM/tree/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f)）的商品编辑合同，配置“购买后动作”和“外部推送”；支付成功后按该合同展示引流二维码或直接跳转，并向商品配置的外部地址投递旧版 `transaction.paid` 协议。

只包含这两项商品能力及其已有支付成功消费链路。不会新增或修改问卷、企微标签、服务期权益、订阅、支付、退款、身份归属或客户资料能力；周期商品只复用同一商品动作／推送合同，不改变其权益结算规则。

旧版行为证据：

- 管理端两块面板、字段顺序、保存入口和读回：[`wechat_products.html:261`](https://github.com/qianlan33333-png/AI-CRM/blob/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/aicrm_next/extensions/commerce/commerce/templates/wechat_products.html#L261) 与 [`wechat_products.html:351`](https://github.com/qianlan33333-png/AI-CRM/blob/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/aicrm_next/extensions/commerce/commerce/templates/wechat_products.html#L351)。
- 商品保存与外推单独保存：[`wechat_products.html:901`](https://github.com/qianlan33333-png/AI-CRM/blob/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/aicrm_next/extensions/commerce/commerce/templates/wechat_products.html#L901)。
- paid payload 的固定字段：[`service.py:234`](https://github.com/qianlan33333-png/AI-CRM/blob/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/aicrm_next/platform/external_push/service.py#L234) 与 [`external_push_admin.py:329`](https://github.com/qianlan33333-png/AI-CRM/blob/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/aicrm_next/extensions/commerce/commerce/external_push_admin.py#L329)。
- 动态 URL Link 的 `response_key`、安全验证和 fallback：[`resolver.py:65`](https://github.com/qianlan33333-png/AI-CRM/blob/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/aicrm_next/platform/navigation_target/resolver.py#L65)。

GitHub 供体仅用于冻结上述旧版行为，不引入旧仓运行依赖、模块、迁移历史或代码副本。

## 开发前分类

```text
OneID: reads canonical customer only. paid payload 只通过 identity Port 读取经验证身份；管理端配置、跳转配置不解析或创建客户，不新增客户主键或匹配逻辑。
Persistence: local transaction + Provider write/external effect. 商品配置由 Product Owner 持久化；paid intent、业务状态、审计／Outbox 与 External Effects acceptance 必须留在同一 PostgreSQL Unit of Work；网络调用由 outbound/External Effects 异步执行。
```

`internal/product/port` 是商品配置的跨域入口；支付成功消费者只经现有 `order/port.PaidEventConsumer` 和 Product read Port 协调。不得新增队列、Worker、重试表、支付回调或 Provider writer；企微写入不在本项范围内。

## 旧版管理端合同（必须逐项一致）

V3 保留现有 `admin_base`、`RenderProducts` 和 Product Host 壳；仅将两个商品编辑页内的这两块 panel 还原为旧合同。不得把 donor HTML、第二壳、secret 输入框、JSON 编辑器、订阅下拉或旧 V3 的 `configuration_reference`／`field_mapping` 表单暴露给管理员。

### 购买后动作

面板标题为“购买后动作”。右侧仅有状态“未启用／已启用”和一个开关；关闭时正文收起。

启用后，按顺序显示两个互斥选项：

1. “支付后展示引流二维码”：字段依次为“引流渠道码”“二维码主标题”（最长 40，placeholder 为“留空沿用：报名成功”）和“二维码副标题”（最长 100，placeholder 为“留空沿用：扫码添加企微领取后续资料”）。
2. “支付完成后直接跳转”：使用既有 completion-target 控件，字段及文案依次为“跳转类型”（`H5 跳转地址`、`动态 URL Link 接口`）、“H5 跳转地址”、 “动态 URL Link 接口”、 “响应字段”。保存按钮为“保存购买后动作”。

切换到直接跳转时，lead 字段隐藏；切回引流时，跳转字段隐藏。保存后重新加载编辑页，开关、选项和已保存字段必须逐值回显。新商品尚未有 ID 时先保存商品；编辑商品可独立保存该动作。

H5 地址继续只接受安全的 HTTPS 绝对地址或同站相对地址。动态 URL Link 按旧语义：服务端取得已允许的 HTTPS `source_url`，读取 JSON 的配置 `response_key`，随后兼容 `url_link`、`url`、`http`、`link`，并只跳转至安全允许的 HTTPS 目标。动态解析失败时使用已存的安全 fallback；无 fallback 时返回失败，不能伪造成功或在浏览器暴露凭据。

### 外部推送

面板标题为“外部推送”。右侧同样只有“未启用／已启用”与开关；关闭时正文收起。启用后的控件、文案和顺序固定如下：

1. “推送地址”
2. “备注”
3. `push_type`
4. `expires_at_ts`
5. `day`
6. `frequency`
7. `custom_params`（键值行）
8. “新增参数”“测试推送”
9. “保存外部推送”

不增加 secret、签名密钥、配置引用、field mapping、JSON 或订阅控件。测试推送先以同一表单值保存当前外推配置，再调用现有 test endpoint，并显示返回的 delivery 状态和 delivery ID；它是已有商品配置的受控测试。保存外部推送只写该配置，不把它伪装成商品主体保存成功。保存、刷新与重新进入编辑页必须完整回显上述字段；创建商品后才允许独立保存外推。

## 支付成功与协议合同

支付成功时必须把完成目标和引流 QR 取自当次订单的不可变快照，而非随后编辑的商品当前配置。完成动作只返回一个最终动作：直接跳转配置优先；未配置可用跳转时才保留已有引流二维码；两者都未启用则保持现有默认完成页。已购买用户再次进入、支付状态恢复和重复支付通知都读取同一订单快照，且同一支付 checkpoint 只触发一次跳转。未支付或全额退款订单不得以 paid 结果放行跳转或引流 QR。

外推只在 canonical ProductID 的 paid order line 上消费。每个 order line 使用稳定的 paid source reference、product slot 和原始 idempotency key；同一事件重放、并发消费及重启不能生成第二个 Provider effect。配置、意图、effect binding、审计／Outbox 和 EER acceptance 与订单结算同事务提交；Provider HTTP 调用不持有该事务。`accepted`／`queued` 不可称为接收方成功，`outcome_unknown` 只能沿原 key 查询、回调或对账，不能换 key 盲重试。

新配置及 paid 消费严格采用旧 payload 形状：顶层为 `phone_number`、`type`、`day`、`frequency`、`remark`、`submitted_at`、固定 `questionnaire_title=微信支付开通黄小璨会员`、`delivery_id`、`event=transaction.paid`、`order`、`product`、`buyer`；`order` 为 `id/order_no/out_trade_no/status=paid/paid_amount/paid_at/pay_channel=wechat`，`product` 为 `id/code/name/price`，`buyer` 为经 V3 identity Port 兼容读取的 `id/openid/unionid/phone`（openid 保持脱敏）。V3 planner 另补 `transaction={transaction_id,trade_state=SUCCESS,success_time}` 和仅在 canonical source event 存在时补 `domain_event_outbox_id`。`custom_params`、`expires_at_ts`、`occurred_at`、`tenant` 不得进入 paid payload；`expires_at_ts` 也不得成为 paid 投递的到期拦截条件。测试 payload 保留旧测试语义，不能反推改变 paid 合同。

## V3 差异及收敛方式

| 项目 | 当前 V3 | 本 PR 收敛 |
| --- | --- | --- |
| 后台外推配置 | `productAdapter` 后置注入卡片，存在 V3 reference／field-mapping 路径，控件文案和布局不同 | 在普通／周期商品编辑页装配旧 panel 合同；仅保留 V3 壳和已验证的 Product Host／反馈组件 |
| 完成动作 | 现有 public completion 已有 QR／redirect 基础，但 target 快照和 URL Link resolver 未由这套旧 panel 完整冻结 | 在 paid order 的同事务消费中冻结旧开关、模式、目标和 QR；支付页及已购恢复只读该快照，不扩展到 mini program 或其他购买后能力 |
| paid 外推 | 已经复用 External Effects，但允许 field-mapping 分支且会因 `expires_at_ts` 规划 `planned_config_expired` | 新旧商品 paid 分支固定旧 payload；移除 paid 的到期阻断和 mapping 替换，不改变 EER、outbound、原 key 重试与回执机制 |
| 历史数据 | 已存在 `configuration_reference`、`field_mapping`、历史 intent／receipt | 数据列和历史记录原样保留；UI 不显示旧 V3 表单。兼容读回时由现有 endpoint Port 将 reference 解析为“推送地址”；无法解析不清空或覆盖原值。历史 intent／receipt 不重写，不重放已支付订单。 |

## 实施步骤

1. 在 Product 的 admin projection／HTTP contract 中冻结旧 `completion_target` 与外推配置 request/response；normal 和 service-period 使用同一字段、校验、版本／CAS 与 idempotency 行为。兼容 read 写保留既有 reference／field-mapping 数据，旧字段未出现在新 UI。
2. 扩展 `web/v3/productAdapter.ts` 和 V3-owned 商品表单模板／样式：在两种商品编辑 route 的既有 Product Host 中渲染上述两个 panel，复用现有 toast、保存恢复和受控表单状态；不改 donor 或 `admin_base`。
3. 将 completion action 的 order snapshot、public projection、动态 URL Link resolver 和恢复支付页接到同一旧合同；为 H5、URL Link 成功、fallback、失败、已购恢复、重复 paid event、未支付与全额退款写服务及浏览器断言。
4. 在 `internal/outbound/commerce_push.go` 的 paid consumer 固定旧 payload 和无到期阻断，保留 `internal/externaleffects/port.TransactionalAccepter`、加密 intent、四 digest、effect binding、outbound Provider／对账路径。不得改造支付 Provider、增加 commerce worker，或让 Product 直连 receiver。
5. 通过已有 composition 的 `cmd/aicrm/order_paid_event_fanout.go` 验证 consumer 仍在 Order settlement UoW 内；历史配置和 receipt 只读兼容，不作补发。

预计受影响 V3-owned 路径：`internal/product/{port,app,http,store}`、`internal/outbound/commerce_push.go`、相关 migration、`cmd/aicrm/{commerce_push_adapter.go,order_paid_event_fanout.go}`、`web/src/admin/templates/{productForm.html,spProductForm.html}`、`web/v3/productAdapter.ts`、Product public completion adapter 与其测试。最终以实际最小 diff 为准。

迁移编号在实施时以合入主线后的 ledger 为准：主线已发布 `0170`／`0171`，本 PR 的未发布 schema 变化顺延为 `0172_order_checkout_post_purchase_action.sql`、`0173_product_paid_purchase_action_target_snapshot.sql`、`0174_product_external_push_test_delivery_id.sql`；不得重写已发布迁移。

## 前端一致性记录

| 参考页面 | 复用组件 | 公共扩展／新增 | 受影响调用 | Product Design skill | 验收证据 |
| --- | --- | --- | --- | --- | --- |
| 旧普通商品编辑页；V3 `/admin/wechat-pay/products/{id}/edit`、`/admin/service-period-products/{id}/edit` | `RenderProducts`、`admin_base`、`productAdapter`、现有 toast／保存恢复 | 两个 Product-owned panel；不新增页面壳或通用 Provider 组件 | 普通商品、周期商品 | 本会话 catalog 无可调用的 Product Design route；未使用，未安装 | 认证 Host Chromium Journey：真实页面、保存、刷新、逐字段回显与 paid 链路 |

组件索引确认两个 route 都归 `PROD`／`productHost`，而非可从问卷、H5 或侧边栏挪用 UI。组件只采集／展示 Product Owner 的 HTTP 数据，不承担身份解析、Provider 调用或效果状态机。

## 验收与提交门禁

1. Product app/store/HTTP 专项测试覆盖：两种商品的旧字段校验、CAS／idempotency、保存和 GET readback；reference／mapping 的兼容保留；历史 intent／receipt 不变。
2. Outbound／order 集成测试覆盖：同一 paid 回放和并发仅一条 intent／effect；同事务失败全回滚；配置 disabled、receiver unavailable、identity unavailable、Provider outcome unknown；无 `expires_at_ts` 阻断；paid payload 的 order/product/buyer、V3 transaction 三字段和可选 `domain_event_outbox_id` 精确匹配，且精确缺少 `custom_params`、`expires_at_ts`、`occurred_at`、`tenant`。测试推送的真实 Product HTTP POST、同 key replay 与 receiver 必须得到同一非空 `delivery_id`；它仍不是送达证明，历史无该字段的 Product receipt 可读可重放。
3. Completion 测试覆盖：lead、H5 redirect、动态 Link response-key 命中、fallback、无 fallback 失败、订单快照不随商品后续编辑漂移、支付状态恢复、单次 redirect、未支付和全额退款拒绝 paid completion。
4. 在 `cmd/aicrm` 新增／扩展 `Test…ChromiumJourney`：普通与周期商品真实 Host 页的开关、字段顺序、独立外推保存、刷新回显；支付成功动作和 paid external-effect 接收／状态证据。不得用 Mock、静态 toast 或仅 queue 成功替代。
5. 代码完成后，干净已提交树上依序运行 `python3 scripts/dev_preflight.py fast`、`python3 scripts/dev_preflight.py compile`、受影响领域专项及 browser Journey；记录 HEAD、tree、status、命令与证据目录。随后提交、推送 draft PR；不合并、不部署，也不把本地 browser／compile 说成完整 CI 或生产 Provider 验收。

## 审核点

- OneID 只读取已验证身份，未创建／合并／猜测客户。
- paid intent、订单结算与 EER acceptance 确认同一 PostgreSQL UoW；没有第二队列、Worker、重试或 Provider writer。
- 普通和周期商品都严格呈现旧两块 panel；没有旧 V3 reference／field mapping 表单或超范围的标签／权益／问卷功能。
- 订单完成目标在 paid UoW 中冻结；旧 paid payload、V3 transaction 补充字段和 URL Link fallback 有精确测试；历史配置和 receipt 没有静默删除、重写或补发。
