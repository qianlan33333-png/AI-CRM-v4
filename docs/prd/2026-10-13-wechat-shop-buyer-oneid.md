# 微信小店买家资料与 OneID 修复

复用已授权微信小店父 PRD。本轮按工作台转交并在原对话核实的用户最新“按原顺序完成预发布、先不晋升生产”指令，衔接准确累计 base 501ef2b32865e5028ad734e08d4dd7df54f028f4（tree 65de81cda911eb3283e64b4ec0d8c2e0af756371）；原 V5 d7b2cec 和全部历史引用保留。生产排查应用版本 8023435693fca28e4d8ef03a18031747dcc2294c。原任务 01a1291c-c9d4-7be3-aac6-037536644d66。

## 复现与判断

源单 3739797044778671872 已于 10月13日03:00 入库为 CRM 5655，但付款客户为空，保存的外推 Phone/Account 为空。官方详情确实返回 `order.unionid` 和 `delivery_info.address_info.virtual_order_tel_number`；UnionID 值与用户编号 1023798 的企微已验证身份一致。当前 Adapter 只读取 OpenID 和 recharge_info，漏掉上述字段。用户确认小店与企微使用同一开放平台 wx0ca836834b18e989。

用户明确指定买家展示昵称采用 `source_infos[].account_nickname`，按该业务映射实现；昵称仅用于展示，不参与身份匹配、不改客户档案姓名。手机号是订单联系事实，不升级为已验证手机号。

```mermaid
flowchart TD
 A[官方订单详情] --> B[读取联系手机号与指定昵称]
 A --> C[读取带配置平台 scope 的 UnionID 与 App scope OpenID]
 C --> D[现有 OneID Resolve 及锁定可信主体]
 D -->|已有客户且无冲突| E[关联当前订单买家]
 D -->|尚无客户| F[保留加密买家资料与待关联索引]
 G[以后企微验证并关联 UnionID] --> H[同一 UoW 的 Identity Observer]
 H --> E
 B --> I[现有订单详情与列表字段]
 E --> I
 E --> J[保留原支付快照与已接受外推]
```

## 边界和最小实现

- OneID：使用 Provider 内部构造的 VerifiedFact、配置中已确认的开放平台 scope、现有 Resolver / SubjectReader。Resolve 不建客；身份冲突不自动合并，也不按手机号或昵称猜归属。已由管理员确认的 OneID 合并按锁定的 canonical lineage 归一当前买家，保留原支付和外推事实。
- 持久化：Order 只写自己拥有的订单与小店资料；Phone、昵称和外部身份原值加密。待关联索引只保存 scope 与摘要。Identity 通过稳定 Port / composition bridge 在原 UoW 内通知 Order，关联、审计与 Outbox 同事务，不新增队列或轮询。
- 外部效果：仅纠正新订单的输入字段。历史已接受/未知结果的意图、支付快照、效果键和冻结发送体保留，不因资料回填再次开通。未关联客户、缺手机号/账号仍按原已确认规则发送，不新增完整性门槛。
- `virtual_order_tel_number` 为虚拟发货联系字段；保留为订单联系资料并脱敏展示，不用号码建立 verified 身份。
- 关联既支持客户已存在后订单到达，也支持先订单后企微。复用 Identity 当前身份键锁，避免首次订单与企微同时到达时漏关联；后续响应省略可选资料时保留已读取内容。既有旧单通过现有指定窗口读取补齐资料，不修改日常频率。
- 管理端继续用现有 `payer_name`、`payer_customer_number`、`payer_phone_masked` 和联系层级展示；小店手机号可在未关联时脱敏显示，指定 account_nickname 优先用于买家显示。原生订单行为不变。

## 成熟参考与页面

复用 WxJava 微信小店模块的 [订单 UnionID](https://github.com/binarywang/WxJava/blob/develop/weixin-java-store/src/main/java/com/binarywang/wxjava/store/bean/order/OrderInfo.java)、[虚拟发货联系方式](https://github.com/binarywang/WxJava/blob/develop/weixin-java-store/src/main/java/com/binarywang/wxjava/store/bean/order/OrderAddressInfo.java)、[来源昵称字段](https://github.com/binarywang/WxJava/blob/develop/weixin-java-store/src/main/java/com/binarywang/wxjava/store/bean/order/OrderSourceInfo.java)。不增加 SDK 运行时。

Product Design 已读取 index / audit / image-to-code、完成 user-context preflight；无已登记参考条目。使用用户本次订单截图作为视觉目标，复用现有订单详情布局和字段。截图保存在仓外 /tmp/aicrm-shop-buyer-20261013/01-reference-order.png，已打开检查；不增加视觉资产、页面框架或布局。

## 验证与交付

具名用例覆盖真实字段合同、手机号与昵称映射、缺资料正常入库、同 scope UnionID 关联、跨 scope / 跨根冲突拒绝、先企微后订单、先订单后企微、并发/重放和事务回滚；补资料后付款快照与已接受效果/发送体不变。HTTP / 浏览器确认现有买家栏、用户编号、脱敏手机号和原生订单展示。

五项影响：合同为现有订单字段补齐；机制为加密小店资料和 OneID 关联；直接模块为 Payment Adapter、Order、Identity 与 composition；页面为现有订单列表/详情数据；证据为 Provider 合同、真实 PG 两种先后顺序、拒绝/并发/回滚及浏览器读回。新增限制仅复用已有 scope、身份冲突、加密及效果幂等边界。

开发完成后实际推送国内准确 ref，按路由交给现有发布工作台做 Linux 与最终统一预发布。本轮仅在隔离合同验证资料回填及关联前后意图、效果数量和发送体摘要保持一致；不执行生产回填。未来集中生产批准后，迁移前按现有流程备份，再独立读回此源单的客户关联、昵称和脱敏手机号；用户已确认此单人工开通，禁止补外推。

## 当前串行发布边界

本轮累计基线已包含工作台先前接收的健康修复及 `0244_survey_submission_subject.sql`，小店增量保留 `0245_wechat_shop_buyer_details.sql`。迁移文件和已应用 checksum 不改；不把已存在的累计基线能力再次算作本小店改动。解决检查清单冲突后，所有小店运行时与测试代码和 V5 一致；组合根保留累计基线已有 `BindV1AI(..., overviewReadUoW)` 调用，未覆盖基线修复。工作台沿原队列、锁、attempt 和收据串行接续，不创建第二发布路径。

用户此前明确要求补齐订单3739797044778671872手机号、昵称和OneID，并确认人工开通；后续在发布工作台明确要求“小店、订单导出、裂变 UI 全部完成预发布后再集中正式上线，先不要晋升生产”。本轮以后者为准，仅交付小店预发候选。生产单笔资料回填计划保留，待未来集中生产批准后执行；意图300原 body/key、次数1及 provider_accepted/HTTP200保持不变，禁止补外推。

## 首次外推的数据读取顺序补齐

用户本轮明确要求先访问官方详情，基于已读取的资料外推。回调只持久化收据和订单读取任务；自动补拉和配置唤醒也复用 `readOrder`。官方读取失败、订单不匹配或支付事实无效时返回错误，不创建外推；读取成功但可选手机号/账号确实缺失时，保留已确认的置空发送规则。

```mermaid
flowchart TD
 A[回调或每日补拉或配置唤醒] --> B[读取官方订单详情]
 B -->|读取失败| C[原 River 任务重试]
 B -->|成功| D[提取电话 账号 昵称及带 scope 的身份]
 D --> E[同一事务保存订单及资料 解析 OneID]
 E --> F[使用已读取联系资料及可信买家生成首个外推意图]
 F --> G[事务提交后由现有 External Effects 发送]
```

代码审查发现原读取顺序正确，但配置后恢复仍从首次入库冻结的 contact/paid snapshot 取联系和买家字段；首次为空、后续详情有值时会继续发送空值。最小补丁使用本次详情的联系资料，缺省时保留本单此前官方读取的资料及原联系快照；加密买家资料同时保留账号。第一次生成意图使用当前可信 OneID 解析结果，付款金额、明细和支付时间继续使用原冻结事实。Outbound 仍在同事务查重，已生成意图（queued/accepted/unknown/terminal）的 body 和 key 不变，不重复提交。

分类沿用父 brief：OneID 为可信解析；Persistence 为 Provider 读取后同一 PostgreSQL UoW；外部效果复用原意图接受和 Worker。没有新增限制、任务、迁移或 UI。参考沿用 WxJava，已再次核对其 `OrderAddressInfo.virtual_order_tel_number` 合同。

验证新增未配置时首次空值、后续详情补齐再配置、随后详情省略可选字段及并发首推；接收测试接口实际断言电话/账号/付款金额。保留 queued/accepted/unknown 发送体不变和缺资料仍提交用例；读取失败不得调用 importer 的现有合同随本次执行。此补丁替代候选 dc7b417 的输入选择，仍不授权重发源单 3739797044778671872 / intent 300。
