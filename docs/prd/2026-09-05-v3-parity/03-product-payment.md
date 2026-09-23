# PRD-03 商品、购买与支付闭环

状态：批准开发；先读 00-control.md。主责“交易管理”，Terra xhigh；普通商品叶子可由“修复商品管理板块逻辑”限定协作。

## 1. 基线与分类

复用 V3 product/order/payment/public purchase 以及 `docs/07-PRD-交易管理与历史用户数据迁移.md`。旧版来源为 commerce 商品、public_product、微信支付及退款/对账叶子协议。保留完整购买链路与旧版已售数量口径。

OneID：可信支付身份解析/既有显式建客、付款人和受益人分别引用 canonical customer；持久化：本地 UoW、内部任务、Provider read、支付/退款外部效果。

## 2. 当前差异

商品管理、历史订单、支付适配、回调及对账已存在；生产记录主要是历史订单，不能由记录数推导新购买链已验证。OAuth/支付关闭是部署事实；本轮补代码缺口、隔离协议测试与跨板块结算合同，不启用生产。

## 3. 用户流程

1. 旧版商品创建/修改/上下架/复制/分享、价格与销售统计保持原操作与口径；分享进入同源公开购买链接/二维码。
2. 用户从公开页进入安全 checkout，可信 OAuth 关联付款人；受益人独立确定，不默认将每个付款人都当权益受益人。
3. 服务端读取商品/价格快照与优惠券资格；创建订单和支付意图，不能信任浏览器金额或 customer_id。
4. prepay 调用、支付回调、主动查询、退款申请/回调和未知结果对账按现有 PaymentV1 契约执行；验签结果才可改变结算事实。
5. 列表、详情、筛选和导出显示历史/原生来源、金额、支付与退款事实；排队和 Provider 接受不当作支付成功。
6. 微信小店沿用旧版确有的同步/读取/退款范围；支付宝只保留旧版已证实的读取及历史能力，不虚构新支付退款 Provider。

## 4. Owner 与和 PRD-04 的共同合同

- product 管定义和价格，order 管订单/金额快照，payment 管尝试/回调/退款/对账，coupon 管券，现有 order 权益边界由 04 扩展。
- 沿用现有 HTTP 路由、订单和 PaymentV1 Port；必要新增仅限事务化 coupon 预占/核销/释放与权威结算读/事件窄契约。
- coupon 预占与订单折扣快照同 UoW；支付成功以订单行/结算事实标识幂等通知权益。通过 Owner Port 协调，不跨域表写入。
- 成功支付后权益处理失败不得倒改支付事实；保存可恢复处理依据。退款影响按旧版规则，由 04 幂等应用。
- 支付未知时不释放券或重建不同付款请求；累计退款并发不得超过权威实付。

## 5. 历史及前端

复用现有冻结商品、订单和公开购买前端，仅修改 V3 Host/Adapter。历史订单/支付/退款导入工具按 Provider、状态、币种、金额、source key/digest 对账；历史事实不重新申请支付退款，不自动补发权益。受益人缺失保持未解析，不能复制付款人兜底。

## 6. 测试与验收

- 旧版商品所有可见动作、分享链接、已售统计、上下架及完整购买 journey。
- 本地签名 Provider 模拟 prepay、重复/乱序/非法签名回调、超时未知、原键查询、并发退款。
- PostgreSQL 订单/收据/效果同事务失败回滚，优惠券并发预占，支付成功重复不重复核销/开通；支付未知不释放。
- 历史导入重放、金额/笔数对账与未解析受益人保留。
- 03 自身可先独立 PR；联合 04 的购买-支付-优惠券-权益和退款 journey 必须在集成分支完成后才算板块测试通过。
- 不生产付款、退款或启用 OAuth。返回未合并 PR、HEAD、测试证据和部署待办。

## 7. 已核实的接线与增量

- d6 基线的公开购买 Handler、Composition Adapter 和0061迁移已经存在。局部 `ShareLocalProduct` 在缺少权威公开路由时的拒绝是安全默认，不能据此重新开发整个购买链。
- `payment/session.Service.IssueTrusted` 当前把未指定受益人直接置为付款人。本轮按已批准的独立受益人合同修复：migration0068允许未确定的会话受益人，并记录明确选择来源；旧记录原值保留，不伪造新确认事实。
- 公开购买沿既有确认动作明确“为本人购买”，由服务器按可信付款人落定；已有管理员辅助入口只采用已受信的会话受益人。前端不能传任意CustomerID；不新增赠送/代购流程。选择、建单与失败回滚必须覆盖真实PG的CAS、并发和重放。
- 03负责普通建单/结算接线和order/port/port.go；04负责coupon Port与实现、order既有权益文件和周期商品叶子。券持有人沿旧可信付款人，与权益受益人分别记录；预占返回原价/折扣/实际应付/币种/规则版本及预占引用，订单保存不可变快照，退款上限依据实付。

## 8. 下一轮03/04联合实现交接

03的PR136@1329c1e已完成独立购买链接和受益人选择缺陷，04的PR138提供领域Port但仍待自身PG修复。接线开始前从root领取PR138最终HEAD，沿用03原clone，从1329c1e建立新codex分支并合入已核实前置提交；保留父PR，不改主线。

- Coupon：OrderCouponCoordinator的ReserveWithin/ConsumeWithin/ReleaseWithin。无指定券且无可用券返回CouponApplied=false原价快照，显式无效券失败；订单冻结完整金额/币种/商品type+id+code/规则版本/预占引用，同UoW接受支付效果。选择当前付款人的券，不能按受益人误取。先核收据重放，避免原价订单重放时又选到新领取的券。
- Order：ServicePeriodEntitlementCoordinator的GrantPaidServicePeriodWithin及ApplyServicePeriodRefundWithin。商品周期按服务端CheckoutProduct.ServicePeriodDurationDays冻结；明确self选择后按受益人发放。首次来源订单成功退款按原整期撤回一次，后续同订单退款不重复扣减。历史映射只用HistoricalServicePeriodSourceCoordinator的可信订单行及实际天数。
- Product：ServicePeriodPublicReader.ReadPublicServicePeriodByCode只允许enabled周期商品精确code。新增独立周期公开Host/Payment入口，复用旧公开模板，不让普通product数字别名误命中周期商品。表单、复制、分享、上下架和DurationDays保存需完整原UI回归。
- 公开领券：按PRD04第8节补/c/{public_slug}与可用券/状态/领取入口；复用现有支付OAuth身份会话，禁止第二套认证/OneID。购买页可用券选择来自Coupon Port，浏览器不能指定客户、价格或周期。
- 数据页：04已提供CouponClaimAdminReader与BindWithClaims，需composition挂couponData，补旧统计和列表适配；spProductData/周期会员表读模型与原页面也需真正挂载。内部Port存在不算UI完成。
- 历史订单、支付、退款、会员开通、券领取/核销快照逐条对账，缺失归属/周期/时间证据不猜测；不得以补造grant/effect完成迁移。
- 实际共用PG/Provider协议/Runtime验证：公开购买→用券→支付→权益→退款，所有回调重放、unknown、并发、同键跨时刻、用户视角页面结果；本轮真实Provider仍只用本地模拟。

05任务独占各Owner新增的audience_read.go/port/audience.go及对应cmd受众适配，不与交易任务同时改支付文件；03不删除这些预留读接口。迁移0066—0074已登记，联合订单快照等若确需新迁移先向root申请编号。

## 9. PR143 总控审核退回项

本节是原验收范围的具体缺口，未增加产品能力；尚未修复和验证前不得声明03/04闭环完成。

- 周期会员表：`product/http/handler.go` 的 serviceMembers 仍返回空数组，memberGridAccess 全部 false，视图/分享仍为固定占位。须通过既有 Order 权益与权限 Port 接真实查询、筛选、备注、保存视图和旧版已有协作行为；每个可见动作必须有实际结果及 HttpApi journey。
- 为补上述既有会员表行为，批准0079由Product Owner持有保存视图、协作者及旧有分享设置元数据；Order继续持有权益及其既有备注事实，客户显示经既有Customer Port。先冻结旧版视图/协作/分享合同，复用Access主体及权限，不新造公开客户目录或第二套鉴权。越权用户、已撤销协作及无效分享引用不能读取会员资料；更新同UoW记录审计/Outbox。不得把客户身份或权益复制为Product第二份主数据。
- 周期商品公开页：当前新写 service_period_template.go 与仅作哈希检查的冻结 Python 供体没有形成实际复用。应提取旧版模板、样式、脚本并作最小 Go Host 适配，恢复旧版详情媒体、添加企微二维码、不可购买状态和真实剩余周期比例等已有行为。逐项说明必要适配；不在运行时依赖旧 Python，不重写新的页面。不以只校验未使用供体的哈希证明 UI 保真。
- 公开购买页券选项 value=0 当前显示“不使用优惠券”，但 Owner 合同为自动选择最佳可用券；按旧版实际选择合同统一界面和服务端，不能以“不使用”的文案暗中选券。
- 购买请求网络失败或结果未知后的重试必须保持同一逻辑 checkout 幂等键及已知 merchant_order_no。当前每次点击生成新随机键，会绕过原未知结果的恢复路径。首次请求前持久保存键，未知时查询/恢复原请求，只有权威终态和明确新购买才生成新键；覆盖创建响应丢失、轮询超时、刷新与再次点击。
- 补真实 PostgreSQL 的公开购买、优惠券、支付、权益联合 journey：本地签名 Provider/回调驱动结算，验证开通、续期、首次正额退款、重放、未知、同事务故障回滚和并发。现有“原价订单重放不新选券”测试保留，但它不能替代支付回调与权益的完整路径。
- 历史订单/支付/退款/权益/券的冻结快照导入、重放与逐行对账必须在隔离库完成本轮测试。生产执行留下一阶段，测试工具及隔离验收不能整体移入生产待办。每行有明确来源、摘要、目标或隔离原因；核实不会创建新的付款、退款、发送或历史补发权益。

迁移0076归 Order checkout 不可变快照，0077归 Coupon slug；不自行占用其他编号。历史与会员表可分成依赖明确的独立 PR，最终验收仍覆盖以上全部行为。

公开图片复用线索：Product LegacyAdminProjection 已持久保存 slices；Media 已有 ImageVariantReader.GetImageVariant、ImageLibraryReader.LocalImageExists。旧 `_detail_image_source` 支持 image_library_id/library_image_id/asset_id 及 URL/src，公开 `/api/h5/product-images/{code}/{id}/variants/original` 依据商品绑定读取。沿现有 Product 绑定与 Media 只读 Port 最小适配，不能把封面 Images 当作完整详情图，也不能开放任意后台媒体ID；历史媒体ID若未建立可信映射须在导入对账中明确，不能猜测。

## 10. 周期公开页渲染复查（68e811e后）

模板已真正复用，但Go Host只归一化双大括号，没有执行Python字符串字面量转义。总控按现有替换步骤提取状态脚本并交Node语法检查，fetch路径正则报 `SyntaxError: Invalid regular expression flags`。修复Host的模板提取/转义，不改冻结供体；对实际Go渲染出的完整HTML执行脚本及浏览器交互测试，不能只断言字符串标记存在。

当前按map遍历在完整页面不断ReplaceAll，然后对已插入用户内容与普通三引号函数片段再全局折叠大括号，可能改变标题/正文中的占位符或合法JS括号。模板转换应在插入动态事实前完成，动态事实只代入一次；保持原DOM/样式/行为。用含花括号和模板字样的标题、详情及企微模态脚本验证不受二次替换影响。服务中/过期/未开通/不可购买、详情图、二维码、状态刷新、报名/续费跳转均需实际浏览器无脚本错误。

沿现有可信OAuth会话适配旧公开页身份恢复；不得直接删除旧身份引导后默认为所有新打开者都未开通，也不能接受旧签名片段或裸外部ID自报可信。记录必要Host变化和原入口的测试结果。上海日期边界的到期日须在服务端初始页面与刷新后结果一致。

## 11. 会员表旧前端来源与0079交付边界

当前web/src/api/admin.ts冻结DTO仍是较早只读Member Grid合同，不能直接改该文件或更新manifest强行通过。需要的原会员表已有完整供体：AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f下 `aicrm_next/extensions/commerce/service_period/templates/service_period_member_grid.html`、`service_period_member_grid_compact_base.html`、`service_period_member_grid_public.html`，以及同领域 `static/admin_console/member_grid.js`、`member_grid_state.js`、`member_grid_share.js`、`member_grid.css`。

先核对旧真实管理入口和调用合同；按必要文件冻结复用，通过现有V3 Host和可信Access/Order/Product Ports接通视图、协作、分享、查询及备注，不重写新会员表页面。既有只读入口可继续兼容；新挂载必须由原商品数据入口实际可达。API已实现而旧界面无法进入/操作时，0079仍未完成。

6074f57预审已要求：备注在Order写事务内先核对ProductID；元数据写在同UoW复核协作授权/撤销，ID/版本成功响应不得因变量遮蔽变零；保存视图实际应用查询，sort/group不能接受后忽略；事件与Outbox不包含share bearer token。冻结HttpApi测试必须走真实API而非仅fake工作区返回成功。

## 12. 后续历史批次复用定位

复用`cmd/migrate-commerce-history`及`internal/order/migration`的既有manifest、receipt、apply/reconcile；现有order-only窄模式保留。全量reconcile当前只比较数量与金额，不能证明付款/退款/商品快照的每个来源与目标一致；本轮须补逐条事实核验及合法但内容漂移的反例，不能只改输入使其在manifest校验阶段提前失败。

已有manifest覆盖订单、支付来源、退款和受控身份归属，但没有周期来源与历史券领取/核销合同。复用已批准04的可信历史周期来源Port及Coupon Owner；先冻结旧表实际字段，再在既有导入工具扩展明确模式，必要迁移向总控申请。历史已执行结果不得通过在线付款、退款、领券或开通流程补造。缺失可信受益人/周期/发生时间保持明确未解析或隔离，每条结果可对账。独立测试快照与真实PG验证属于本轮，生产数据导入仍留上线阶段。

## 13. 公开付款恢复的实测要求

当前public.go已保存checkout key和merchant_order_no，但测试只检查脚本字符串。后续支付联合批次须实际执行浏览器脚本与HTTP：创建响应丢失、刷新后再次点击、微信取消后继续原订单支付、支付结果未知、存储不可用、同浏览器切换可信付款会话等场景。

- writeCheckout当前吞掉localStorage写失败仍继续创建；这样响应丢失后会生成新key。必须在首次可能产生支付效果前有可恢复的稳定标识，未能保存时明确阻止或沿既有服务端收据恢复，不能静默退化。
- 同key创建重试需要原冻结购买参数。当前仅保存key/订单号，刷新或修改优惠券/手机号可能以不同payload复用原key而永久冲突。复用现有可信会话和checkout收据恢复原购买，不接受浏览器指定他人客户或伪造金额；存储标识不能跨付款会话误用。
- button.dataset.invoked置1后没有在微信取消/失败后按明确用户动作恢复，再次点击可能只轮询而不再打开原订单支付。修复原订单继续支付行为，不能借此生成另一笔未知支付。

以上是同一原订单的恢复与旧支付流程接通，不新增支付平台或额外重试内核；Provider未知状态仍遵循现有EER原key查询/对账边界。

## 14. 完整会员表事实与视图差异（a55a372复核）

本节补足既定旧会员数据表，不新造数据看板。OneID读取canonical CustomerID；Order只拥有订单/周期事实，HXC只拥有已发布的来源快照，Product只拥有视图/协作/分享。通过窄Port组合；不能复制旧UnionID/手机号匹配SQL、跨Owner查表或另建同步队列。

已核实供体dd8：

- `service_period/domain.py:82`剩余天数为未来秒数向上取整，到期归0；显示、筛选、排序分页使用同一冻结时点。a55的Go/SQL FLOOR不兼容，必须修复。
- `member_grid.py`允许20条条件、8项排序、2级分组；字段具有各自操作符，允许的条件组合依旧normalize_view_config规则。现有1项排序/1级分组、仅剩余天数与备注可筛选是待迁移差异，不能通过缩小schema默认为全部完成。
- `member_grid_repo.py:40`与effective_order_counts SQL：有效付费报名订单去重后减去首次报名、下限0；排除退款及退款处理中订单。V3优先从Order现有0070已核实grant/历史来源及订单退款事实恢复同一语义，不能用entitlement.version、备注修改次数、到期日期或常量0推算续费次数。历史缺来源明确未知；真实0仍显示0。
- `huangyoucan_usage_client.py`现有只读SQL给出真实字段：正式登录来自first_login_at；token使用来自未删除消息total_tokens>0；学习计划按active优先、updated_at/id降序选一，再以课程项数限制current_seq；近7天打开次数/最后打开时间来自card_open_log。不能用Sessions7D、是否注册或LastUsedAt冒充这些不同事实。
- `huangyoucan_usage.py`旧归属解析只作为行为参考；V3复用现有HXC/OneID已验证归属，未解析/冲突不猜。现有HXC SourceRow缺上述部分字段，后续在HXC现有Provider读取、快照发布及River刷新中补齐需要的字段与窄Read Port；不重复建Product同步器，不从旧CRM运行时读取。实际来源表与可用权限先只读核验；新字段只在本轮确有旧能力需要时添加，迁移编号向总控统一申请。

内部与公开会员表都必须展示相同事实语义及未知状态，沿原授权/可撤销分享范围读取。筛选、排序、分组及翻页必须作用于完整Owner结果，不得只在当前100行前端过滤冒充全量。无需扩展新指标、AI分析或诊断界面。验收采用冻结原模板和JS的实际交互、真实Owner PG事实（含多页、并列键、过期边界、权限撤销和来源缺失）联合证明。


## 15. HXC共享字段执行边界

原会员表HXC缺失事实统一按13-shared-hxc-facts.md，由一个执行者维护HXC Owner。03先保持Order续费/查询与公开支付任务，待已审核Port交付后接线，不复制另一套HXC同步。0084已分配HXC，来源schema已由总控只读核实。


## 16. 1b148814付款恢复审核阻断

创建成功但响应丢失时，检查点merchant_order_no仍为空。随后微信授权session变化，重放相同key会因实际Payment.Create用sessionToken+key派生merchant号而创建新订单；检查点仅存payload仍不足。必须由既有可信Payment Session提供非敏感opaque绑定/服务端恢复事实，会话变化在新Create前阻断旧检查点，不自报身份。unknown/事实不完整的409也不能统一清空检查点或称授权变化；只在明确可判定的会话不匹配路径处理，未知保留原键。实际Payment app/store/PostgreSQL覆盖“响应丢失+同客户重授权/另一会话”，自写checkoutJourneyApplication的行为不能替代该验证。跨领域HTTP组合测试位于cmd/aicrm，不在Product测试中import Payment http。

## 17. 原订单恢复与终态读取复审

4d7bfc9补opaque Payment会话绑定，但已知merchant_order_no后仍先比较旧绑定，授权过期后无法查看原单。已知原单应交现有Payment Owner验证当前Provider可信付款身份后只读恢复，不再Create、不换幂等键；同可信付款身份新授权可读取，不同身份拒绝，未知单号仍保存旧键失败关闭。当前GetCheckout对paid终态仍检查过期handoff导致conflict，终态读取应在授权后独立于旧预支付有效期。同步新checkout-session、绑定和coupon_claim_id的OpenAPI与生成契约。实际PG及Host覆盖过期支付凭据、重新授权、原单重读、未知响应键不清除。


## 18. 完整会员集合的原字段组合读取

原member全局筛选/排序需要Customer展示名，HXC五字段来自第13号共享合同；不能仅补当前Order页后在浏览器筛选。批准在现有Product读编排中恢复这项原能力：复用Order稳定分页读取完整商品会员候选、Customer.DisplayNames每批不超过200和HXC有界同代Port，在内存组合事实，完整执行原20条件、8排序、2层分组及组计数后分页。Customer/Order/HXC仍各自拥有数据，不在Product落客户副本，不跨领域表查询或新建通用查询平台。

一次最多10,000候选；必须探测第10,001条，超限整次返回明确member_grid_too_large，不以截断集合冒充全量。SnapshotAt只冻结日期计算，不能声称它是数据库快照；HXC分批绑定同一已发布代，分页绑定配置、有效事实摘要和最后规范行ID，源事实改变返回cursor_stale并重新读取。游标不携带仅签名未加密的姓名/备注等个人资料。旧保存视图和公开分享复用相同编排与现有鉴权。

实际测试覆盖匹配行只在来源第二页以后、名字排序跨页、跨Owner的OR、两层分组全量计数、超限明确失败、HXC代或姓名更新使旧游标失效。NULL/unknown与真false/0分开；alliance只读旧权益metadata_json.admin_alliance的已验证历史Owner来源，不伪造字段。03/04真实资金/权益联合及历史逐行对账继续优先推进。

## 19. 其他支付渠道的冻结来源核实

供体dd8 channels/integration_gateway/payment_adapters.py的AlipayAdapter沿_GuardedPaymentAdapter._guarded_result，在production模式即使开关存在仍返回production_not_implemented；此冻结来源没有可迁入的真实Alipay生产扣款叶子。保留既有历史支付宝订单读取/导入对账，不据配置名或菜单另开发新支付渠道。微信支付及旧微信小店真实能力按原PRD继续，不因本项排除。该结论仅限定已冻结供体，不将未核实外部实现写为不存在。

## 20. 原会员表联盟字段的最小恢复

旧ServicePeriodRepository._member_payload直接读取service_period_entitlements.metadata_json.admin_alliance，原成员编辑入口也写该权益元数据。当前V3权益Owner与历史快照只有remark，因而alliance的unknown属于尚未迁入来源，不能作为完整原会员表交付。批准恢复这一已有字段，不建设新的会员体系或身份逻辑。

- OneID只读既有权益的canonical Customer；Order拥有联盟事实、历史映射、变更收据、审计和Outbox，Product经稳定Port组合读取/编辑，继续原权限与CAS。
- 0088分配Order：用可空字段区分尚未采集/无法核实的NULL和已确认原值（包括原空字符串）；长度、允许值与空值清除沿旧合同，不把缺失默认成否。旧快照缺该字段仍可安全导入为未知，新快照保留来源存在性和值。
- 复用cmd/migrate-sidebar-history既有受保护提取/快照与Order导入Port，将该字段纳入源摘要、映射和目标逐行核验；同清单重放不重复，保留源摘要而篡改目标联盟字段必须对账失败。未知源身份不猜测、不建客，无任何新权益开通或效果。
- 原冻结会员表读取、筛选、编辑回读及公开分享读取同一Order事实；编辑使用现有员工权限并同UoW提交业务、收据、审计与Outbox。真实PG验证未知/明确空/原值、重放/漂移、CAS冲突、越权与回滚；仍优先复用冻结前端。

## 21. 周期权益与持券历史的完整目标对账

0088准确270d8f98已通过其联盟切片审核，不意味着旧历史其余字段已全部逐行验证。总控复核 cmd/migrate-sidebar-history：目前目标权益额外核对alliance，而券循环仍只有来源收据与count/customer/digest检查。此项按原PRD03历史及PRD04第4/5节补齐，不增加治理或产品能力。

- 复用现有同一保护快照、映射和Owner导入结果，逐行核对实际写入的权益产品/状态/起止时间/备注/联盟，以及券规则映射/状态/领取与有效时间/核销时间/脱敏券号等事实；每个声明导入字段都对应目标或明确的旧转换。
- 实际PG使用非空已领取/已核销/过期券与权益混合快照；保留source digest但篡改目标状态、期限、核销时间、产品/券映射，或删除目标、改变隔离收据，reconcile必须失败且不把新批次标reconciled。恢复后同源对账通过，重放无新权益/领取/资金/发送效果。
- 旧联盟展示与写路径使用Python Unicode strip；源提取的BTRIM仅处理ASCII空格，不能标为等价。复用既有extract的字段转换表达旧规则，明确缺字段/非字符串/确认空值，不更改既有旧快照摘要；用空格、tab、NBSP与未知值向量验证。首次真实capture/apply前必须完成此开发核验，不把它只留生产待办。
