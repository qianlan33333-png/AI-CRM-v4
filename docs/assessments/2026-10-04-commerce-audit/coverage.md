# 商品、裂变、一级分销审查与候选说明

本轮从国内 main `94d4b845f9d9ddcc30e7e05432032ba1677fac5a` 开始；本机权威目录 `/Users/qianlan/Downloads/新CRM/main` 原工作树干净，国内远端只读核对为同一 SHA。QA739、旧 GitHub main 均未作为起点。候选分支 `codex/commerce-adversarial-audit-20261004` 位于独立 worktree；最终 SHA/tree、命令结果见工作区外的 `commerce-evidence/final-receipt.json`，避免提交内自引用。没有合并、推送、部署、生产写入或真实资金/客户消息操作。

## 已确认缺陷及最小修复

| 缺陷、复现与用户影响 | 根因与最小修复 | 实际证据 |
|---|---|---|
| 服务期商品已有一张封面，编辑发送 `images: []` 后仍显示旧图；无图普通/服务期商品复制返回冲突 | `append(nil, empty...)` 把明确空数组压成 nil：更新误判为旧客户端省略字段，复制写入不满足数组约束。用 `slices.Clone` 保留输入省略/清空差别；持久化输出一致性按图片内容比较 | `images-before.txt` 真 PG 失败；`images-after.txt` 真 PG 清空、遗漏保留、相同键重放、同键异载荷冲突、收据/outbox原子验证；`images-http.txt` 实际登录HTTP写入/复制/回读通过 |
| 合法支付宝成功通知缺失/损坏 `gmt_payment`，将通知时刻当作真实付款时间；影响付款时好友资格与服务期起点 | 签名证明通知内容真实，不能把 `notify_time` 当实际付款事实。标记未知付款时刻，在原UoW写 `query_required` callback receipt 并复用已有River原单查询；仅有效 `send_pay_date` 才结算 | `alipay-time-before.txt` 真 PG 证明错误已支付；新组合回归采用本地验签HTTP和本地签名query response，验证队列失败回滚、通知重放、查询失败不结算、恢复查询只结算一次、无提前权益，真实付款时间进入订单历史及服务期起点 |
| 活动已到期，close任务延迟，管理员仍能 active→disabled；close随后跳过排名快照 | 持久状态落后于由 `[StartsAt, EndsAt)` 定义的有效状态。在锁住活动行后拒绝到期后的状态修改，继续由原close任务结束并生成快照 | `referral-expiry-baseline.txt` 为 disabled/0；`referral-expiry-regression.txt` 为 conflict→ended/1；到期前停用仍通过 |
| 商品购买/服务期身份/初始素材读取缺 `AbortController` 会抛错；transport或JSON body忽略abort时永久等待 | 为三处既有读取添加feature detection及独立deadline，覆盖响应体；付款创建仍仅发送一次并保留原请求恢复逻辑 | `product_transport_deadline.test.mjs` 对实际源码执行缺API、fetch/body停滞与正常错误语义回归；修复前/后证据单独保存。JSDOM证据不冒称旧WebKit真机验收 |
| 手写购买页实际发送的地址、收集级别、provider/channel、推广context不在禁止额外字段的OpenAPI内；支付宝结账POST缺失 | 补共享checkout schema与两种path provider/channel约束；保留身份、价格、佣金禁止由浏览器声明 | `checkout-contract-before.txt` 合法body被拒绝；`checkout-contract-after.txt` schema/route parity和真实字段示例通过，客户ID、价格与错provider均被拒绝；同步活跃OpenAPI的source-index/lock，不修改冻结donor |

上述是具体失败的窄修复；没有新建状态机、库存内核、身份匹配、任务队列或Provider执行流程。支付宝原单查询可能延迟展示付款完成，这是保留正确付款事实所需的恢复路径。未改历史已错误结算数据；若生产存在该类历史记录，需另行授权只读定位及针对性修复。

## 功能、入口、状态、持久化及依赖清单

下表区分实际检查与未深入覆盖，不能把包测试通过解读为逐行审完所有代码。逐文件清单见 `file-coverage.csv`；三个独立审查报告保留更具体的读代码位置。

| 能力与入口 | 状态/持久化/任务及直接依赖 | 本轮证据与边界 |
|---|---|---|
| 普通商品：`/admin/wechat-pay/products`；`/api/v1/products`；`/api/admin/wechat-pay/products/*` | 名称/编码/描述、CNY整数分、库存配置、图片、兼容projection；草稿、启停、归档、删除、复制、分享；Product版本CAS、operation receipt、audit/outbox；Media公开原图绑定 | app/store/handler关键区段与全域回归，真实HTTP清空/复制/重放；UI素材/保存脚本。没有独立SKU变体/多规格库存模型，不虚构该能力 |
| 服务期商品：`/admin/service-period-products`；对应admin API；`/s/{code}`、`/s/{code}/pay`、`/api/h5/service-period-products/{code}` | 同商品配置，duration、服务期definition、支付宝策略；付款人/受益人、Order权益；详情只读可信session | 图片边界修复、政策/联系人矩阵、历史权益与退款store真实PG；新支付宝缺时→原query→权益起点；未完成旧Safari真机完整购买链 |
| 图片与素材选择、表单历史值、支付方式、联系人/地址、会员数与会员grid | 兼容字段的 nil/[]/false/省略语义；Media目录证明；Order entitlement读；grid视图/分享/collaborator owner与readonly角色 | Product域测试包括真实grid CRUD/CAS及HTTP权限；素材JSDOM。全部后台tab和辅助技术未逐屏审查；外部成员分享的实际撤销/字段范围已于第二阶段验证 |
| 购买后标签/完成二维码或跳转；商品外推配置、字段映射、测试/投递读回 | Product冻结动作，Order paid consumer，Channel QR，Outbound/EER效果；配置与accepted事实归Product，真实效果不归Product | 配置域与paid action事务测试；真实PG+Chromium付款完成/旧管理入口一致性。真实企微、外推接收方、网络最终回执未验收 |
| 公开普通详情/购买：`/p/{code}`、`/pay/{code}`；checkout-session、purchase-status、微信/支付宝checkout | 可信付款session+binding；服务端锁Product冻结当前价格/启用状态与Order snapshot；优惠券claim、电话密文、地址、promotion/activity context；请求恢复使用原key | HTTP字段/OpenAPI，真实PG冻结价/同键重放/联系方式矩阵，真实Chromium公共购买。优惠券领取全流程与跨OAuth身份恢复未独立穷举 |
| Order/Payment：原单查询、签名通知、退款与权益 | pending→paid/failed；退款预留→确认/失败；整数分、累计退款上限；payment/order锁、receipt、history、audit、outbox；同UoW消费者发权益/裂变销售/分销佣金；EER+River异步 | 全域真实PG；并发通知只一次、下游失败回滚、退款未知/查询先于回调、权益赠与隔离、部分/全额退款撤权；原query查询失败/重复通知。Provider均本地合成、无真钱 |
| 裂变：`/referral`、invite/activity handoff、`/api/v1/referral/*`、`/api/admin/referral/*` | campaign draft/active/disabled/ended、有效半开时间；个人/战队、邀请/参加、关系绑定/撤销、积分、奖励、排名快照/导出；唯一campaign/customer、关系/邀请锁、receipt；close River；销售归因/退款负向账本 | app/domain/http/store与迁移关键区段；过期关闭修复；真实PG重复参加/关系/销售退款与组合OAuth；Chromium活动旅程。海报/导出/战队全部边界未做独立UI敌手矩阵 |
| 一级分销：`/distribution`、`/d/{credential}`、`/api/v1/distribution/*`、`/api/admin/distribution/*` | 报名协议、资格/启停、产品政策、凭证、下单冻结归因；单个distributor；整数实付分×冻结基点向下取整，排除运费；佣金等待/资格复核→结算/异常；退款累计复核River；既有EER分账 | 真实PG组合：推广人不能任一购买方、凭证并发/键scope、佣金实单生成、政策UoW、累计退款重定价/末个资格撤销、结算重启重放/关闭指令；Chromium报名/政策。真实收款账户、分账到账、提现不验收 |
| 身份、报名、问卷及人群 #27 | canonical customers.id由既有identity Port与HttpOnly trusted session解析；好友必须严格早于真实付款秒；问卷版本/提交key/答案与身份分离 | 查实际挂载与DTO、#27资格消费者、近期问卷恢复/点击/deadline修复；重跑相关问卷JSDOM；不把演示S页面当报名能力，不宣称问卷全模块重审 |

## 安全、竞争和恢复结论

商品管理员写入检查认证、Admin/SuperAdmin角色与CSRF；Viewer读、readonly grid collaborator禁止写。公开购买/裂变/分销从可信session解析当前客户，不接受客户端客户ID、付款价格、佣金或receiver。真实PG组合验证付款人/受益人/推广人隔离及customer-scoped读取。目标核心表没有租户维度：本仓库的当前模型以独立CRM部署与客户对象授权为边界，本轮没有跨租户运行证据，不把对象权限测试称为多租户隔离验收。

并发/幂等重点不是返回码：读回订单价格、付款时间、callback和operation receipts、paid事件次数、权益起点/撤权来源、归因和佣金金额、活动快照，以及事务失败后不可留下半成功。商品/订单使用版本与行锁；Order paid/refund fanout共UoW；分销异步退款按累计值及全局锁序复核；既有重启/回放用例在本地数据库运行。仍未对所有River任务进行操作系统级kill/restart和所有乱序组合穷举。

## 问卷经验迁移与待审清单

近期问卷修复说明：历史恢复/自动填充可只有可见DOM值而无input事件；旧WebKit祖先capture先disable可吞掉target提交；abort不保证请求或body结束；超时后必须保留原提交key。商品保存/checkout目前读取实际DOM、target listener内disable，未复现同一点击问题；图片空数组与读取deadline则在商品路径确实复现并修复。现代Chromium和JSDOM通过均不替代旧WebKit可信触摸证据。

明确剩余项：

- Referral product-context同键契约及期限不一致已在第二阶段修复并由真实HTTP/PG验证；不据此推导历史重复计佣。见下方增量。
- `stock_quantity`当前是非负配置字段，checkout不扣减；没有0=无限/售罄、预占/释放的合同。不能直接把该字段改造成库存余额，也不能据此宣称库存售卖已验证。
- 未完整逐行审完所有商品、支付Provider/退款HTTP、grid与外推适配器；未逐屏验证后台全部tab、券领取、全部服务期身份/OAuth恢复组合、海报/导出、所有权限矩阵。
- 无真实微信/支付宝沙箱账号或production Provider最终验收；无真实资金、分账、提现、消息。无旧iOS/WebKit真机/可信触摸。未跑完整仓库全量、所有模块race、Linux installed readback或生产部署。
- 报名/问卷/身份/权益仅纳入与本轮商品归因及付款直接相连的路径；AI、群运营、渠道消息、完整问卷后台和其他模块留后续审查。本候选不承诺消除所有未知bug。

## 验证收据

环境为Mac、隔离PostgreSQL16、localhost:55464随机schema，Go1.26.6；首次环境运行用系统Node24.21.0，最终前端检查改用已安装的锁定Node24.18.0/npm11.12.1。原数据库、冻结donor与原工作树没有业务写入。具体最终命令、pass/fail/skip、最终SHA/tree及是否清理数据库服务，以工作区外 `commerce-evidence/final-receipt.json` 为准。原始JSONL/日志均保存在该目录，避免把大量运行日志混入候选源码。

## 第二阶段增量（继续51e0516，未回退源码）

| 缺陷与影响 | 最小修复 | 业务验收与原始证据 |
|---|---|---|
| 归档周期商品的原匿名成员分享仍可读，但管理员无法撤销（404） | 创建仍锁可写商品；撤销仅锁历史商品，已有Store只做enabled=false及resource/id/version匹配 | 实际composition登录、CSRF/Viewer拒绝，跨商品409不撤销原分享、匿名基础+访客过滤、无私有字段、归档新分享404、原分享撤销/重放200、旧token410；phase2-share-scopes-after.txt |
| 本人佣金读取非法cursor误报503 | 入口复用已有正整数游标合同返回400 | 实际Order结算生成交错两客户佣金，真实Distribution session，汇总/分页/foreign cursor/篡改参数/错用途或过期cookie隔离；phase2-distribution-self-final-pass.txt |
| WeChat通用退款接受超长或非法退款单号，接受后不能由Provider执行 | 仅确认WeChat订单后按官方64 ASCII及字符集合拒绝，原raw receipt payload不变，其他Provider不变 | 修复前65字符/slash接受，后400且不调用app，合法64及允许符号仍接受；payment/http目标用例和Coupon真实PG。修复前后文件是工具结果摘要而非原stdout |
| 已认证裂变product-context同actor/key重复签发、跨campaign同键不冲突，cookie固定24h与DB活动期限不同 | 既有actor receipt锁+同UoW，分域HMAC派生token，不保存raw token；回执引用campaign；0222只扩展操作白名单 | 真实HTTP/PG同键原token/期限、不同campaign409、不同actor独立、两并发首次一context一receipt、过期旧键重放原快照、新键拒绝、下游过期不冻结归因；context-phase2-concurrent-after.txt |
| 普通与周期后台清空图片后PG []、HTTP null违反数组合同 | 两个HTTP响应边界及直接返回的LocalProduct投影构造非nil空数组，保留请求字段语义 | 两实际WebKit引擎tap删除后真实PUT/GET/PG复现；phase2-webkit-entry-after.log及webkit15.4-service-images-null-before.log，最终以准确HEAD WebKit回执为准 |

0222真实旧库升级保留campaign_posters原回执JSON和append-only trigger，旧result_kind及唯一键不变；完整Referral app PG包含奖励、关系撤销、海报、活动close、销售退款与升级。初稿遗漏campaign_posters被回归捕获并修正；属于候选实现中间回归，非旧版本业务bug。迁移仅隔离测试，未在生产运行。

WebKit旅程范围是实际15.4/26.0引擎的商品HTML/JS、真实tap、无input值恢复、手动原key重试、价格1234↔12.34、最后图片PUT[]/GET/PG、有无身份服务期。WeChat UA及OAuth拒绝返回tab态模拟；checkout POST及全部外部请求拦截，其他后台API真实PG。无物理iPhone或Provider验收。原Safari UA/重复图片seed/授权按钮disabled/Go期望名称错误为夹具问题，保留失败日志，不计业务缺陷。最终运行结果见final-receipt-phase2.json。

仍未覆盖：库存0/有限量销售规则待用户回答；退款跨管理员同key碰撞已在交付补核复现并修复；真实Provider查询/支付/退款/分账到账、完整OAuth实际账号恢复、后台全部tab/grid/角色、所有海报/团队/导出UI/kill-restart任务矩阵、券领取逐客户端、完整问卷/其他模块、Linux安装与真实手机。当前单企业系统只验证客户对象隔离。后续不得把本地fixture或包PASS解释为这些能力验收。

最终提交SHA/tree、干净状态、检查通过/失败尝试/skip和PG清理以工作区外final-receipt-phase2.json为准；firstwave final-receipt.json保留历史51e0516证据，不作为最终新候选回执。

准确3069f9e组合检查发现普通copy仍直接输出LocalProduct images:null，已扩展该投影的最小修复。保留旧失败收据，最终候选另见final-receipt-phase2.json。增强实际HTTP回归同时检查PUT、同键重放、空图复制/重放、GET的wire []，不以len(nil)==0替代合同。

## 交付补核增量

实际HTTP/PG证明不同管理员对不同支付使用同key，compat自动退款编号因只哈希key而撞全局refund_no，generic明确不同refund_no仍因EER全局accept键不含actor而409。最小修复只补全既有actor-scoped合同的两处键；旧compat编号由已有Payment+actor+key回执恢复，完整载荷照常校验。两入口各两actor各一refund/各一effect；同键原载荷重放、改金额409、旧编号重放通过；worker停用，无Provider执行。原始refund-scope-before.log/after.log及最终准确源码收据。

补齐两项明确核心路径：WebKit15.4/26实际操作/s/{code}/pay，恢复无input地址、tap购买、当地503后手动沿原key/原body重试，无自动循环；checkout POST仍拦截。真实composition领券从管理员登录/CSRF创建、发布、分享到可信payer cookie公共页、拒绝匿名/客户字段篡改、并发同键同claim/期限、额度拒绝、适用商品可用券读回，PG issued_count=1、claim=1、holder来自session、claim receipt=1。没有新增Coupon生产逻辑；201/envelope和receipt表期望错误归为夹具。

具体剩余核心入口与外部条件分类见完整报告新增“逐项未验证清单”；海报、战队、奖励撤销、导出、grid等已有PG/HTTP合同测试，不把“未完成所有UI组合”写成这些能力完全未审。

## 有终点的三项高风险补审（2026-10-05）

新增真实失败：Alipay查单任务discarded后原签名回调重放无runnable任务；会员备注跨商品引用/版本冲突的Order稳定Port错误误报503。前者只恢复原Provider读取目标，保留旧任务/receipt/cancelled，River去重；后者只补404/409映射。真实有效Admin、Viewer即使有edit协作者也不得写、未授权客户、缺CSRF、existent/absent对象、跨商品/客户会员引用及CAS均有真实HTTP/PG证据。管理员在单企业内具全局管理权限，不虚构product.created_by为所有权限制。

新补本地页面源码双WebKit恢复、真实River查询失败/权益grant事务回滚/两runtime重启竞争；两引擎checkout均0 POST（拦截配置），券2次同key HTTP请求只1claim正确holder，退款1POST只1refund/effect，刷新/回读无新意图。服务期active/renewed/expired/refunded回读及A→B客户切换/迟到A响应保护；普通paid/reload无扣款，全退款后需显式新购买。PG状态fixture不是本轮重新付退款业务；相关真正资金/权益业务由composition和退款消费者测试交叉证明。派发pageshow事件不等同物理BFCache。

详情/缺陷前后证据与精确边界见完整报告的“高风险补审”增量。已验证的是列出的金额/归属/私有数据/一次效果/事务不变量；不声称穷尽所有角色动作与真实Provider/真机。实际Provider、物理设备、库存规则决策及Linux安装仍未验收。本轮不推送、合并、部署、真实资金或客户消息。
