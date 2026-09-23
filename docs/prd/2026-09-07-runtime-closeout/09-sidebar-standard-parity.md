# 09 — 企微侧边栏标准版复用与业务接通 PRD

日期：2026-09-07。状态：旧仓与当前 V3 核查完成至下述披露边界；正式开发范围已冻结，待独立执行任务实现。本文不是已实现、已上线或已验收证明。

需求来源：[用户确认的交接文档](../../plans/2026-09-07-sidebar-standard-parity-handoff.md)。本 PRD 按该文档补充实际源码和生产只读证据；不以旧文件中的行为或指令扩大用户范围。

## 1. 目标与交付边界

从真实企微客户会话打开侧边栏，自动恢复合法员工登录态和当前客户，使用标准版三列导航、头部、画像、问卷、普通/周期商品和订单、优惠券、素材/雷达；修改有持久化、查看有正确归属，用户明确点击后按标准版客户端发送语义操作。

**删除侧边栏全部聊天入口及其请求**，包括“其他客服聊天”“聊天活动”、内容卡片、重试、错误信息和旧 tab 恢复。保留非聊天用户时间线。会话存档后端、数据库、通知、任务及其他消费者继续保留，存档生产接通项目不变。

一个完整侧边栏 PR，包含必要 Go、Port、前端 Host/Adapter、非破坏迁移、测试、装配和发布资源。只查旧库证据，不迁移历史答卷/画像/订单，不连接旧库提供运行时数据。不重写支付、优惠券、OneID 或执行框架，不迁移旧开放平台 56 接口，不新增 Campaign。

## 2. 基线和完整库存

- 旧仓 `AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`：完整可遍历检出，2,123 个跟踪路径/Blob，27,426,193 内容字节；所有扩展名及二进制均读，失败 0。
- V3 `AI-CRM-v3@3f5ea38c702b91e37746c2ad1b7bf4f5174e5a2a`：1,828 跟踪路径/Blob，27,788,772 字节，读取失败 0。开发前再次刷新 main 和既有 PR HEAD。
- 旧 Python 1,197 文件完成 AST 解析，语法失败 0。能力注册表 14 项；静态 ownership manifest 781 条路由，AST 找到 704 处方法路由声明。两者统计口径不同，包含组合前缀、别名和声明，**不是少实现 77 条的结论**。
- [完整路径与 SHA 库存](sidebar-evidence/legacy-tracked-inventory.json)、[V3 库存](sidebar-evidence/v3-tracked-inventory.json)、[能力/路由/服务/测试候选关系](sidebar-evidence/legacy-capability-route-map.json)、[全仓对应说明](sidebar-evidence/README.md)。哈希只证明读取与来源，不能证明业务测试通过。
- 全仓是静态能力库存与对应关系核查；深入业务审查覆盖本 PR 的侧边栏链路。其他能力没有逐页运行验收。测试文件匹配是候选而非通过记录。模板注册和动态路由依赖仍须执行者用实际 composition 验证，不声称全仓全部复刻。
- 当前开放 PR 中未找到另一个未合并 sidebar PR；本项不得覆盖 #185 客户同步及已上线 JSSDK 修复，也不混入 #186/#187/配置中心开发成果。

## 3. 首要架构分类与 Owner

OneID：读取 canonical customer、解析可信外部身份；只用 Identity Port 的 kind/scope/assurance，禁止通过姓名、文本手机号或外部 ID 形似关联。`Resolve` 不建客，侧边栏不能触发隐式合并。员工来源是受保护 viewer session，授权再次验证企业、员工与客户关系。

持久化：画像是 Customer Owner 的本地事务；读取用 Survey/Order/Coupon/Media/Radar 稳定 Port；SDK 配置及当前客户读取是 Provider/客户端读；当前会话分享是用户触发客户端交互。图片现有 send-intent/grant 继续经 Outbound 稳定 Port；服务端企微业务写仅 outbound + External Effects。业务数据、幂等收据、审计、Outbox 原子写在同一 PostgreSQL UoW；网络不得占事务。

没有新调度需求，不新增 jobqueue、Worker、重试、租约或对账内核。图片素材若需准备媒体只接现有媒体/Outbound 链路，不让 Sidebar 上传 Provider 素材。

## 4. 核查结论：症状、已确认原因与待验假设

| 症状 | 当前证据 | 结论与处理 |
|---|---|---|
| 标准版 UI/字段不一致 | V3 `web/v3/sidebar/main.ts:49` 仅 name/corp_name；独立 V3 renderer/template，不是旧 workbench 原样承接 | 确认 UI/字段接入缺口；恢复旧模板、CSS、渲染和业务操作顺序 |
| 须额外点击授权 | V3 initialize 已自动 SDK 初始化；401 后 `renderViewerSessionRequired` 显示 OAuth 按钮；旧 `boot` 自动 `maybeStartSidebarOAuth` | 确认页面 OAuth 回退策略不同；自动一次有状态回退，防循环，不绕过平台强制同意 |
| 手机号有掩码但显示未绑定 | V3 workbench 丢弃 Owner 的 PhoneAssurance，phoneBound 每次重开 false，只有当页 bind 操作置 true | 确认展示与回读契约缺陷；恢复持久状态，区分“已填写/已绑定未验证/已验证/冲突”，不能靠掩码存在推断 verified |
| 当前测试客户问卷 0 | 23:29 只读核查：旧版 8 答卷；V3 同企业 verified external 唯一解析到一个 Customer，同开放平台 verified UnionID 相同；该 Customer 答卷 0，同 UnionID 历史投影 0，V3 全库答卷 1,585 | 当前证据是该客户历史记录缺失，**不能称 JSSDK 或 OneID 匹配失败**。历史迁移单列后续；本 PR 验证新提交及现有已归属答卷正确可读，零记录如实显示 |
| 商品点击只有技术回执提示 | 旧 `sendProduct` → `sendChatMessage news` 带 imgUrl；V3 payload 没有 imgUrl，且仅写 client_callback/delivery_unknown | 确认封面载荷及提示差异。真实未进入会话的充分根因尚未确认；恢复完整载荷，核客户端权限/上下文/回调，不把改文案当修复 |
| 优惠券不可用/不完整 | 旧 CouponSidebarApplication.list_claimable 列 active 券定义及 /c/{slug}；V3 Sidebar 接 CustomerCouponReader 已领券，且 renderer 使用占位展示 | 确认读取语义接错；补可领取目录稳定 Port，保持已有客户券读取给原消费者 |
| 聊天入口报存档未启用 | V3 main.ts 主导航 other_staff_messages、画像子页 chat_activity 仍发请求 | 用户确定移除侧边栏展示和请求；不以启用存档掩盖 UI 范围问题 |
| 同一订单已支付/全额退款不同 | 重新逐字符核准截图完整订单号后，两库各匹配同一订单/客户/9900分；旧 status=paid、refund_status=full_refunded、refunded=9900；V3 history、paid、refunded=0、effect_eligible=false | **确认历史退款事实缺失，不能靠 UI 把所有 paid 改退款**。页面按本地 Order 事实显示；该历史事实修复列后续数据处理，本 PR 用完整退款事实 fixture 验证映射，绝不发起真实退款 |

生产证据在 23:39 补核准确订单号，退款差异属于已导入历史记录事实不完整，尚未修改生产数据。生产证据仅保留计数、assurance、布尔和订单摘要：[脱敏只读结果](sidebar-evidence/production-redacted-check.json)。不提交原始截图、手机号、external_userid、UnionID、Cookie 或凭据。

## 5. 复用清单与允许适配

供体全部固定上节旧提交。逐文件 SHA256/大小：[sidebar-donor-manifest.json](sidebar-evidence/sidebar-donor-manifest.json)。

| 部分 | 旧来源（aicrm_next/ 前缀） | V3 起点 | 处置 |
|---|---|---|---|
| 头部/三列导航/布局 | app/admin_console/templates/sidebar_customer_workbench.html、static/sidebar_workbench/sidebar_workbench.css | internal/webshell/templates/sidebar.html | 原样复用布局/样式；模板 URL/初始化桥及聊天删除为明确适配差异 |
| 页面渲染/字段/操作 | app/admin_console/static/sidebar_workbench/sidebar_workbench.js | web/v3/sidebar/main.ts | 复用旧渲染，不再保留两套活动 renderer；V3 身份/鉴权/请求/客户端操作桥适配 |
| 商品封面/缩略图 | static/sidebar_workbench/product-card-cover.png、static/admin_console/image_resource_loader.js | Media 现有 variants/thumbnail Port | 原样复用资源/加载行为，正确异常展示；不得复制旧缩略图加载故障 |
| 画像 | crm/customer_read_model/sidebar_profile_repository.py、crm/sidebar_write/api.py/application.py | customer/port/sidebar.go、app/sidebar.go、store/sidebar.go | Go 等价四字段，不保留旧 unionid 主键/SQL/调用方 updated_by 信任 |
| 问卷/订单/周期 | crm/customer_read_model/sidebar_v2.py、extensions/commerce/service_period/sidebar_extension_adapter.py | sidebar.Handler、customerSurveyAdapter、Order/Entitlement Port | V3 已有领域读取；按旧字段/金额/日期/详情/普通周期分类适配 |
| 优惠券 | extensions/commerce/commerce/coupons/sidebar_api.py/application.py | coupon/port/sidebar.go 与 Coupon Manager | 补可领取定义目录，不把已领取列表冒充可领取列表 |
| 身份/SDK | crm/identity_contact/sidebar_authorization.py/sidebar_jssdk.py；旧 JS initWeComSdk/boot | 已修复 V3 WeCom session/JSSDK/context bootstrap | 保留 V3 可信边界，迁入旧自动启动体验，绝不采用旧原始身份字段日志 |
| 协议/缩略图/Token 恢复测试 | tests/frontend/sidebar_jssdk_send.test.mjs、sidebar_token_scope_recovery.test.mjs、sidebar_material_thumbnails.test.mjs | 现有 V3 sidebar Browser/Chromium/PG tests | 冻结旧用例断言，按真实 V3 安全契约适配补充 |

沿用已上线单源来源锁和内容索引。先找相同 authority，有则登记引用；没有才登记一个不可变 authority。生成旧逻辑路径视图不提交第二份源。旧源不可直接指向活动 V3 文件。

旧 JS 是闭包：允许独立、可审查且可重复生成的 Host overlay，把 request/identity/SDK 边界接 V3，并删除聊天导航与 dispatch；保留渲染及原字段处理。overlay 每个改动必须有旧行范围/原因/测试，输入哈希不符失败，生成物不作为新 authority。不得用全局宽泛 fetch monkeypatch，不能只把聊天按钮隐藏而留下自动请求，也不能把整段业务重写命名为 Adapter。

## 6. 字段与读取/写入契约

### 6.1 客户画像与手机号

| 标准版字段 | V3 Owner/落点 | 规则 |
|---|---|---|
| source 用户来源 | Customer 画像注释字段 `profile_source` | 与 customer_directory_projection.source 的同步来源元数据严格分开，不能覆写后者来存运营文本 |
| industry 行业 | Customer 画像注释 | 原选项/自由填法、空值语义按旧渲染保留 |
| industry_description 行业具体描述 | Customer 画像注释 | 原长度/换行展示；服务端限制及错误提示一致 |
| needs_blockers_followup 需求/卡点/跟进状态 | Customer 画像注释 | 单一旧文本字段，不新增 CRM 状态机 |
| 姓名/公司/头像/标签/渠道标题 | Customer/WeCom/Channel 已有稳定读取 Port | 不把标签文案当身份证据，不为头部展示发送新的打标请求 |
| 手机号状态 | Customer 安全投影与 Identity 已有 assurance | 返回 phone_binding_state + phone_assurance + masked 值；declared 不能显示已验证 |

Customer 现有目录投影会被客户同步更新。四个运营字段采用 Customer Owner 的持久画像记录（customer_id 唯一外键、四字段、独立 version/updated_at），投影读取组合；如实现时发现可复用等价表则复用，不能另建客户根。新增字段不能被下一次同步覆盖。

扩展 SidebarProfileService 的明确画像命令，字段存在位区分未提交与清空；版本冲突 409，重新读取后提示用户确认，不静默覆盖。旧自动保存交互按原节流保留，提交幂等键按客户/内容/版本固定。Customer 画像 + receipt + audit +必要 Outbox 同 UoW，验证审计失败整体回滚。操作员从受保护会话来，不信 body.updated_by。

手机号沿用 DeclaredPhoneAttacher：人工填写/换号只能 declared，冲突按现有规则拒绝或待处理。把本次写入返回值与重开读取统一为持久状态；不会因旧库 mobile_verified=true 自动升为 V3 verified。当前截图客户旧 verified、V3 declared 是信任事实迁移差异，单列后续。

### 6.2 问卷、订单、周期权益和时间线

- Sidebar authorize → canonical Customer → customerport.CustomerSurveyReader → Survey CustomerHistory/CustomerHistoryWindow。保持真实 total 与游标分页一致，列时间/题数/答案详情；关联未知/冲突不跨客读取。
- 当前客户 8 条旧答卷缺失留历史数据待办；不在页面调用旧库补齐。已有导入记录如存在但同一可信 OneID 不可读，必须以其来源映射查明后只修读取，不批量迁移。
- 订单沿用 Order Query，展示订单号、¥ 金额、时间、真实支付/退款终态和详情；普通与周期分类复用已存在字段。退款显示必须依准确记录，不按同名商品概括。已查出的历史订单缺退款事实单列后续数据修复；不能靠前端推断退款，更不能补发退款 Provider 请求。
- 周期备注使用既有 Entitlement 备注命令和权限；不改周期/退款开通逻辑。
- 保留非聊天活动 timeline 及其来源详情入口；若聚合 Port 同时返回 message 事件，侧边栏使用明确过滤参数/类型白名单，不请求存档补充。不要删其他调用方的 message 类型能力。

### 6.3 优惠券、素材和雷达

- Coupon Owner 提供只读 SidebarClaimableCatalog（如有等价稳定 Port 直接复用），字段含 name/discount_minor/currency/适用商品标题及类别/claim_ends_at/公开领取 URL/真实可领状态，列表有受限分页。
- 保留原目录可见与领取资格区分：不把“active”自动当当前用户必能领取。领取页继续调用现有 Coupon 规则，受限/到期/领完要准确提示。复制 URL 是复制，不自动发券、不占用或核销。
- 公开 URL 使用 V3 PublicOrigin 和真实公开 slug，不借旧域名。不要暴露管理员 key 或签名秘密。
- Media 读取启用资源、搜索/标签/缩略图；Radar 读取实际可用链接。旧 UI field→V3 DTO 一一映射，跨页搜索/分页不丢条；媒体不可用显示真实错误，不假造素材 ID。

## 7. SDK、自动进入与当前会话操作

固定生产已恢复的 SDK 来源：`https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js`；旧模板来源为 `https://res.wx.qq.com/open/js/jweixin-1.6.0.js`。**两者版本字符串不能按数字新旧判定兼容性，本 PR 不回滚已验证识别的企业 SDK。** 前端页面复用不等于连同旧 SDK/旧鉴权装配替换。

已复核已有 [JSSDK 回归 PRD](../2026-09-07-wecom-sidebar-jssdk-regression.md) 和固定官方 SDK 源字节：SHA256 `0ade9f7a4d1adcb626e48a8c87ae4037a4509b9e22262846bd15d3f19ee0cda2`。当前重新执行 `node scripts/sidebar-wecom-jssdk-contract.mjs` 通过，覆盖 Mac/Windows/iOS/Android native bridge 握手。regular 成功后的 agent 单独重试、regular/SDK 超时后必须重开文档、迟到 callback 代际隔离、缓存异常降级等既有合同必须保持，不能直接照搬旧 boot 覆盖这些修复。

补充当前资源核验：同一官方 URL 本次取回 27,032 字节，SHA256 `3423759cba33274c3d0f8acdf6e3e7bcae44cb532f98c66d6920b7063310f90b`，与仓库冻结样本 18,565 字节不同。仅替换测试输入和对应内容摘要、保持原握手断言不变后，当前资源同样通过 Mac/Windows/iOS/Android 四类 native bridge 合同。**版本 URL 不代表不可变字节；两组握手通过也不代表真实分享或送达已验收。** 本次保持已验证的企业 SDK 入口及双握手控制器，不因字节变化直接换版本。当前资源摘要与验证边界记录于 `sidebar-evidence/donor-test-result.json`。


V3 config 请求与签名按当前完整 URL 去 hash，CorpID/AgentID 从受保护服务端配置；wx.config → ready → agentConfig → getCurExternalContact → 可信 viewer/bootstrap；不调用未用于身份归属且当前应用未授权的 getContext。保留现有 URL/企业/应用/TTL 范围缓存及失效保护。

401 时自动发起一次现有 OAuth start；state/next 由服务端校验，重入与循环记号绑定当前上下文，失败才显示重试。无 viewer 身份不得仅凭 query 中 owner/customer 参数提权。当前会话切换/返回可见触发有依据的重新读取，失效请求取消，前一客户旧响应不得覆盖新页面；不要自行新增周期轮询。

商品/周期商品/雷达消息使用旧版 `wx.invoke('sendChatMessage', {msgtype:'news', news:{link,title,desc,imgUrl}})` 合同；图片使用 `{msgtype:'image',image:{mediaid}}`。补全可访问封面和正确公开购买 URL，保持价格展示。每次用户点击只调用一次，等待当前上下文确认；发送中按钮禁用，切换客户后未执行意图作废，不转投新会话。

客户端结果判定保留 err_msg/errMsg/error/cancel/timeout 区分，不能复制旧“缺 err_msg 也算成功”的宽松判断。常规反馈如“已提交到当前会话，请在会话中确认”；取消/未确认如实显示。既有证据模型仍区分 client callback、client executed、delivery unknown，诊断不堆在主界面。不自动重发、不调用服务端群发替代。

当前会话分享协议已于 2026-09-08 补核：[企业微信官方文档](https://developer.work.weixin.qq.com/document/path/94797) 的直接 HTML 请求返回 200，正文标注更新日期 2025-04-21。网页提取工具失败后已通过公开页面正文核对，未引用第三方博客替代。官方仍保留 jweixin 调用章节，确认应用身份、当前会话入口限制、news 的 link/title/desc/imgUrl、图片 mediaid 以及明确成功回调字段与本 PR 合同一致；素材须为企微素材。摘要与取回字节哈希见 [官方协议核验](sidebar-evidence/official-sendchat-verification.json)，不提交整页官方正文。本次不切换已正常识别客户的 SDK；该核验不代表真实企微分享或客户送达通过。

## 8. 验收矩阵与门禁

| 编号 | 完成条件 | 证据 |
|---|---|---|
| S01 标准版复用 | 320/375/430/768px 页面布局，六主导航三列排列，头部/标签/字段/普通周期切换与旧版一致；无横向溢出 | 同窗口旧版/新渲染对照，Chromium computed geometry + 操作断言 |
| S02 无聊天 | 导航、旧 tab query/hash/storage、深链、重试均无聊天 UI；不开 chat/message 请求；非聊天 timeline 保留 | Chromium 网络拦截计数=0，真实页面流程 |
| S03 进入识别 | 冷开/重开/已有 session/401自动OAuth/失败重试/快速切客；不需要页面自设额外启动按钮 | 协议测试及真实企微独立验收 |
| S04 画像 | 四字段保存→重开→客户同步后保留；清空与未提交分开；冲突/非法员工拒绝 | 真实 PG CAS/并发/幂等/审计失败回滚 + Chromium |
| S05 手机 | declared/verified/pending/conflict 不误标；换号后重开相同；掩码不冒充验证 | 真实 Identity Port PG + UI |
| S06 问卷 | 已知可信客户提交后显示 total/多页/时间/题数/答案；同 scope/异 scope/冲突隔离 | 真实 PG composition Browser，不依旧库 |
| S07 商品/雷达 | 点击一次完整news载荷，封面/标题/URL正确，取消/错误/超时/切客不重复 | SDK fixture + 真实受控企微客户操作（人工另验） |
| S08 优惠券 | 可领取定义及适用商品/截止/复制；已领券不是目录，领取页按原规则校验 | Coupon PG + Browser，无自动 claim/effect |
| S09 素材 | 查询/标签/缩略图/分页/图片 grant，一次性回执、过期/重复/未知保持事实 | Media/Outbound PG + 协议 + Chromium |
| S10 订单/周期 | 精确订单号对照所有原状态、退款、金额、时间、详情、备注 | 真实 PG fixture，不以同商品名配对 |
| S11 发布 | 干净检出、空缓存、单源冻结、完整dist/manifest/深链、API实际合同；旧 SDK 修复不回退 | 架构/编译/相关专项后完整 CI、发布后只读 Browser |

旧供体三个 Node characterization 测试已执行：14 passed、0 failed、0 skipped（见 sidebar-evidence/donor-test-result.json）。这仅冻结原行为，尚不证明 V3 或真实企微通过。

原断言保留，不关闭门禁。涉及后台语义运行真实 PostgreSQL16/race；纯样式无需无关新机制。执行状态分为实现、测试、审核、合并、部署、配置应用、真实企微验收，不将任何前一步标为全部完成。

## 9. 调度、依赖与回滚

先交付本 PRD/证据，再派发一个独立侧边栏能力 PR；最多三个开发任务，现有群运营/问卷外推/配置中心在做，不启动第四个。主任务审核 #186 后安排原群运营执行者接手，若仍需 #186 修正则先完成原修正。Runner 由问卷任务完成后承接。

侧边栏修改归属：页面与 sidebar Host/handler/Customer 画像扩展/Coupon 目录 Port 属本 PR；Survey 外推配置属于 #187；config Runtime/OAuth 环境字段属于配置中心；客户同步装配 #185 不改；存档后台继续独立。`cmd/aicrm` 组合接线及迁移号由主任务协调，先领取空闲编号，不抢 0101 群运营、0102 配置。

发布前固定已审核准确 HEAD、全 CI、dist。无新身份应用参数则复用当前 V3 有效配置；配置来源旧生产仅一次受保护读取，不把旧数据库变正常数据源。生产发送测试交给有明确目标/内容授权的人工操作。

回滚保留前一 app+dist+release manifest，发布本 PR 前采集版本；新增 Customer 画像表/列不做 destructive down，旧二进制忽略新注释，保留数据。停止新客户端操作后回切资源与应用；不能撤销已发生的分享或伪造发送失败。回滚验证也要保证旧版 SDK 修复仍在基线；聊天移除的回退影响明确记录，不偷偷重开聊天功能作为验收通过。
