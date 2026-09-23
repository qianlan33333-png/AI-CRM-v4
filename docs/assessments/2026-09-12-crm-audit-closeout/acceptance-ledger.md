# CRM 迁移收尾验收台账

日期：2026-09-12（Asia/Shanghai）  
审计基线（启动时 main）：`6f09899c74a966a87af03158540a030e5c50817d`
本轮已合并状态快照：`986f83e967ed3d092b98bff2060107b4e415c64b`
对应 PRD：[CRM 迁移收尾、审计缺陷与 UI 一致性整改](../../prd/2026-09-12-crm-audit-closeout.md)

## 使用规则

每一行都是一个独立验收门。`PR`、`CI`、`merge`、`deploy`、`browser`、`provider` 分开记录，任何一列通过都不能代替其他列。状态值使用：`未开始`、`进行中`、`通过`、`失败`、`阻塞`、`不适用`、`历史证据`。本台账不记录测试客户电话、测试群 ID、私钥路径、Secret、OAuth code、openid、external_userid 或支付原始凭据；真实验收只允许使用用户批准的受保护会话范围和测试额度；会话范围不是系统中的客户白名单配置。

OneID/持久化/效果总判断：客户、身份、归属和 HXC 项目复用 Identity Port；配置和 UI 以各领域 Owner 为准；内部可恢复任务复用既有 jobqueue；企微、支付、退款写入沿用 External Effects/Payment 的幂等、收据、对账和 `outcome_unknown` 语义。本台账不授权任何额外 Provider 写入。

本状态快照的 CI 结论只指 GitHub 检查。`CI browser 通过`表示隔离 CI 的浏览器旅程通过，不能替代线上登录态页面验收；`deploy skipped`表示未发布，不是发布通过。

## 审计缺陷与 UI

| ID | 审计发现/验收目标 | PR | CI | merge | deploy | browser | provider | 当前结论/下一步 |
|---|---|---|---|---|---|---|---|---|
| AUD-01 | Radar 真实 PV/UV/查看次数与最后查看时间；无查看时仅对应次数为 0，时间为空/暂无 | #244 `025ef3375338fa126e417e09b56460b805aa0709` | 完整 CI 通过 | 已合并 | 未执行 | CI browser 已通过；线上登录态待验 | 不适用（统计读模型） | 复用 canonical 统计事实，不在 Radar 内解析身份 |
| AUD-02 | Customer 360 依赖失败不得默认为低风险 | #239 `a10f554939771b0ad1e0a708e532e1c7df73dca1` | 完整 CI 通过 | 已合并 | 未执行 | CI browser 已通过；线上登录态待验 | 不适用（只读聚合） | 依赖失败降级已进入 main，仍需发布后读回 |
| AUD-03 | GroupOps 列表与详情负责人一致 | #246 `85aa3151bd50998c129c518928c16e97536f463e` | 完整 CI 通过 | 已合并 | 未执行 | CI browser 已通过；线上登录态待验 | 不适用（目录读取） | `owner` 保持非空投影，区分未配置与目录资料缺失 |
| AUD-04 | 归档计划不可启用且状态中文化 | #246 `85aa3151bd50998c129c518928c16e97536f463e` | 完整 CI 通过 | 已合并 | 未执行 | CI browser 已通过；线上登录态待验 | 不适用 | 归档终态合同已进入 main，仍需发布后页面核验 |
| AUD-05 | 运营闭环计划分页、批次摘要批量读取，默认每页 20 条 | #248 `46887f9d6b9f345e15de69cdc5c03907da805ae9` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用（只读） | 空/重复/未知查询为 400；offset/next_offset 为可重放 int32 范围；批量读取故障逐项标为 unavailable |
| AUD-06 | 素材刷新 source_count 跨页累计且不重复 | #245 `64d6dfde76410b957dc4581eb0400bd95cb248bb` | 完整 CI 通过 | 已合并 | skipped | 不适用（后台任务） | 不适用（不改变 Provider 调用） | migration 0149 与 Outbound-owned readiness 已进入 main；全局 runtime migration gate #252 也已合并 |
| AUD-07 | readiness 覆盖启用模块所需迁移、表和字段 | #243 `4cf9b0c5f8787adb6d2dde076bc044e13df4a02d`；#252 `a726f8191695b1e04960c37d800ec3bb194d819a` | 完整 CI 通过 | 均已合并 | skipped | 不适用 | 不适用 | 当前运行时所需结构缺失会 fail closed；#252 保留 0124 并新增 0149 的缺失→503、恢复→200 PostgreSQL 检查 |
| AUD-08 | 至少 37 个活动 method/path 补齐主 OpenAPI，覆盖 dispatcher | #247 `5a057e60cbd408db6dc790cad12b4d99220e23f0` | 完整 CI 通过 | 已合并 | skipped | 不适用（契约门） | 不适用 | 覆盖 38 个活动操作、118 个显式注册、认证 AND 与退休 imports 负例；退休路径不恢复为活动操作 |
| AUD-09 | 素材库路由须挂载来源 Owner 的读取与筛选能力 | #253 `56f071be1b24dc841cd96a505195279038d48d82` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用（读取与 UI） | 已进入 main；不把隔离浏览器旅程外推为线上素材读回 |
| AUD-10 | HXC Dashboard Orval 客户端没有随主 OpenAPI 的依赖类型变化重新生成 | #254 `4425076489463450a41c0b484d8523c7a7e4fd63` | 完整 CI 通过 | 已合并 | skipped | 不适用（生成契约门） | 不适用 | HXC 客户端已按当前主 OpenAPI 重新生成；待最终发布包中复核生成物闭包 |
| AUD-11 | 已归档渠道不得继续显示或生成可用二维码，恢复必须走正常配置状态机 | #255 `a777f56778f56cb4e1c9e60d5255549bf900e646` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用（本缺陷不写 Provider） | 已进入 main；归档历史与当前 active 事实在渠道验收中分别记录 |
| AUD-12 | 渠道欢迎正文中的 `{{客户名}}` 必须冻结为可信客户名或“朋友”，未知变量应明确拒绝 | #257 `f3e453e288e26798808c02ad6adb17d396c05c61` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；发布后待新建关系条件下验收 | Provider 未以该新版本验收 | 0150 使已冻结正文不可漂移；不重放旧 welcome code，既有好友关系不能伪造新添加验收 |
| UI-01 | 运营闭环控件、反馈和移动端布局统一 | #251 `c9bdedaade3442286f6c77f6962786e01c82bf9f` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用 | 保持 Excel 标题、封面和冻结快照合同；不把 CI 通过替代线上登录态验收 |
| UI-02 | GroupOps 详情不再整页横向溢出 | #249 `252526335b13099bfd7c3368c4beb97ea62a7c8a` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用 | 宽表只在容器内滚动；最终仍需验证目标窄屏 |
| UI-03 | 客服/成员选择器单选多选文案和上限正确 | #242 `4b4b503ea16d6e8c5154ccdeec9bc5251c33b455` | 完整 CI 通过 | 已合并 | 未执行 | CI browser 已通过；实际登录态待验 | 不适用 | 调用方显式声明模式，GroupOps 单选与渠道多选分离 |
| UI-04 | 选择器窄屏搜索布局不被覆盖 | #242 `4b4b503ea16d6e8c5154ccdeec9bc5251c33b455` | 完整 CI 通过 | 已合并 | 未执行 | CI browser 已通过；实际登录态待验 | 不适用 | 仍需在最终发布页面验证 780px 断点 |
| UI-05 | Excel 移动端 CSS 修复为两列 | #248 `46887f9d6b9f345e15de69cdc5c03907da805ae9` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；实际登录态待验 | 不适用 | 仍需验证长标题、错误提示和上传控件 |
| UI-06 | 既有渠道客服显示可信目录姓名 | #250 `0a3c628a146d59176a7ec41d8ab5ac1044104354` | 完整 CI 通过 | 已合并 | skipped | CI browser 已通过；线上登录态待验 | 目录读取未做生产读回 | 本地有界目录补全，未命中必须明确 unavailable，不用技术 ID 冒充姓名 |
| UI-07 | 全站标准组件盘点、复用和接入验收 | 不适用（只读覆盖矩阵和两页运行时核验） | 7 个定向断言通过；11 个 shell/E2E 通过，2 个旧 fixture 路径跳过 | 不适用 | 未执行 | 21/21 仅入口映射，不等于 21 个真实 browser 回放；两页 DOM 核验确认 canonical automation 45 条/3 页分页正常，登录态全量重放待验 | 不适用（展示层） | automation 旧静态模板为误报，不开发；image-library 搜索、含停用、重置是 canonical active 缺陷，已交由 UI agent 独立修复 |
| UI-08 | 业务时刻按上海语义展示和回填 | #258 `d0b74d416ca2a75ee2acf403731271a046c3f663` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用 | 只限展示/输入边界，不能改写未修改字段的原 instant 或精度 |
| UI-09 | 优惠券列表显示真实商品中文名、领取时间范围与中文状态；手机可完成读取 | #259 `5cf761ea362e989a6258392f42fcfd2699ac3a42` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；本地隔离 Chromium 已见 1440 完整列与 390 列表/编辑可用；线上登录态待验 | 不适用 | 商品名由 Product Port 批量投影；未知价格不显示为 0；当前“删除草稿”为真实受控写入口，本轮未点击 |
| UI-10 | 人群计划的时间展示与提交须保持 Shanghai 业务语义 | #260 `d3d70da7e21aa59214c576d2713f76fcd586e93f` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用 | 独立于 #258 的人群计划路由；不以文档提交代替页面验收 |
| UI-11 | 运营闭环 Excel 报表的时间扫描遗漏闭环：上海时间与交付失败原因须安全呈现 | #261 `9ab5bdfbaffeac473d678012b59cff4392d4d535` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用 | 仅展示层：无时区/无效时间不得猜测；已知失败码映射为受控中文，混合文本或未知 Provider 码不透传 |
| UI-12 | 全站面向用户的失败反馈须是受控中文 | #262 `986f83e967ed3d092b98bff2060107b4e415c64b` | 完整 CI 通过 | 已合并 | skipped | CI browser 通过；线上登录态待验 | 不适用 | 不透传机器码或任意服务端文本；逐路由保留可行动的中文提示 |

## 迁移与人工/Provider 验收

| ID | 验收项目 | PR | CI | merge | deploy | browser | provider | 当前结论/下一步 |
|---|---|---|---|---|---|---|---|---|
| MIG-01 | 客户同步发现、绑定、投影、冲突、失败和 API/UI 读回 | 不适用（已有实现） | 历史证据 | 历史证据 | 已部署历史版本 | 既有读回证据；最新线上登录态待复核 | 不适用（目录读） | 只读审计的最新成功轮次为 23,497 个 already-linked，冲突和最终失败均为 0；不把该聚合替代最新 UI/API 读回 |
| MIG-02 | 历史身份 scope 与已解析问卷提交的独立来源证明 | 未开始 | 未开始 | 未开始 | 未开始 | 待人工确认 | Provider read/授权 scope 待确认 | 1,049 份提交历史上已解析到 canonical customer，但独立 scope 绑定证据仍缺失；保持待核，不改身份 |
| MIG-03 | HXC OneID/UnionID/手机号/双键匹配和冲突 | 未开始 | 未开始 | 未开始 | 未开始 | 聚合已读回；逐类人工核验待完成 | Provider read/scope 证据待确认 | 当前为 1,231 matched、1,662 unmatched、32 conflict；待人工处理，不自动合并或重放 |
| MIG-04 | 问卷定义、答案、推送历史、SCRM 历史及真实回执 | 未开始 | 历史证据 | 历史证据 | 已部署历史版本 | 正常鉴权读回已有 | 真实推送/回执待验 | 迁移数量可读回；具有身份证据的推送快照为 0，不能以历史导入或接口读取代替 Provider 回执 |
| MIG-05 | 自动化/AI 审批、内部任务、External Effects 和客户回执 | 未开始 | 历史证据 | 历史证据 | 已部署历史版本 | UI 已读回；真实执行待验 | 待验 accepted→executed→回执 | 排队、批准或 effect 状态均不单独关闭 |
| MIG-06 | 商品、订单、会员、券、支付、退款逐条对账和受控真实验收 | 未开始 | 历史证据 | 历史证据 | 已部署历史版本 | 测试券配置窗口 2026-09-08 至 2026-09-15 已在正式浏览器确认有效；券仍未发布 | 已授权测试额度；尚未执行支付、退款或回执核验 | 当前没有系统客户白名单配置；只能在执行前从受保护会话记录重新核对测试订单和对象。历史 payment #924 / effect #21（一笔历史 unknown）仍为 `awaiting_prepay` / `outcome_unknown`，无 reconciliation 或 callback；且与本轮测试客户无 payer/beneficiary 绑定。不得盲重试，后续使用独立订单与幂等范围 |
| MIG-07 | 媒资、侧边栏读取/上传、旧链接和未知效果 | 未开始 | 历史证据 | 历史证据 | 已部署历史版本 | 读回/上传部分已有 | 未执行真实企微发送/回执 | 受控群发送前置目录项可读，但成员、目标客户归属和 Provider 观察事实尚不足；不发送、不重试未知 effect |
| MIG-08 | 归属转移、消息存档、目录权限和历史回读 | 未开始 | 历史证据 | 历史证据 | 已部署历史版本 | 入口读回待复核 | 企微 archive/目录 Provider read 待验 | 不把 SDK 读取成功当作业务回执 |
| MIG-09 | 标签、配置和 Open Platform 权限/作用域及实际回读 | 未开始 | 历史证据 | 历史证据 | 已部署历史版本 | 管理页读回已有 | 个别渠道标签的本轮 receipt 见 MIG-10；其他权限/作用域仍待验 | 配置存在不等于 Provider 成功，单一 receipt 也不外推为所有标签与作用域通过 |
| MIG-10 | 受控渠道欢迎与入渠标签 | #255、#257（#257 已合并，均未部署） | #255、#257 完整 CI 通过 | 均已合并 | 当前生产仍为审计基线版本 | 2026-09-12 21:59:24（Asia/Shanghai）用户已实际收到一次欢迎正文；该次正文暴露未替换变量，不能作为 #257 的变量验收 | 同一次新入渠中欢迎 effect 与入渠标签 effect 分别有 executed/provider receipt | 该真实证据仅证明旧版本的两个效果链路各自完成；发布 #257 后需新的添加关系条件验收正文替换，不删除现有联系人、不重放旧 welcome code |

## 发布门

| ID | 门 | PR | CI | merge | deploy | browser | provider | 当前状态 |
|---|---|---|---|---|---|---|---|---|
| REL-01 | deploy opt-in 控制先合并，普通 main 合并不自动上传生产 | #241 `b094ffdad26a57b8e3e46ceb4512ac71a020d450` | 完整 CI 通过 | 已合并 | skipped | 不适用 | 不适用 | 默认未设置 `AICRM_ENABLE_ACTIONS_DEPLOY` 时 main 仍跑 CI、不会创建 deploy job |
| REL-02 | 最终 SHA Linux 完整包、安装顺序、迁移、回滚和 checksum | 待创建 | 待运行 | 待最终文档合并 | 未发布 | 待线上核验 | 不适用 | 选定最终 main SHA 后，构建输入包含此前已合并的全部内容（含 #240）。发布后新增的验收文档只能记录该 SHA 的结果，不能冒充已部署版本或替代制品证据。包与 preflight 必须逐项校验 0148/0149/0150 SQL、`release-files.sha256` 和迁移账本三条准确 version/name/checksum；还须在包清单中逐项核对 Excel `components/excel-batches/batches.py`、`components/excel-batches/requirements.txt`、`components/excel-batches/aicrm-excel-batches.service`，并确认制品不携带 `.so`，只携带独立 archive SDK runner |
| REL-03 | 本机 SSH 上传、线上 SHA/HTTPS/health/readiness | 不适用 | 不适用 | 依赖 REL-02 | 未执行上传或安装 | 待线上登录验收 | 不适用 | 严格 IP SSH 连通已验证；key preflight 仅核验 4 个 key 有效性，且实际进程的 6 个允许列表开关已核验为布尔结果，均未输出 Secret 原值。目标机外置 archive SDK 库固定摘要和无凭据 ABI health 已只读通过，仍须随最终安装前清单复核。未上传，未核对线上 SHA/HTTPS/health/readiness |
| REL-04 | 受控 Provider、支付/退款和回执独立验收 | 不适用 | 不适用 | 不适用 | 依赖 REL-03 | 测试券窗口已正式 browser 确认，仍未发布 | 渠道旧版本已有一次欢迎和一次入渠标签的独立 receipt；支付、退款未执行 | 受控验收前置的 6 个布尔条件均为 true；用户已授权单笔 0.01 元、累计 0.10 元且只对本轮测试订单全额退款。当前没有系统客户白名单配置，执行前仍须从受保护会话记录核验具体对象；尚未支付、退款或验收 #257 的变量替换 |

## 当前基础核验

- 本状态快照的已合并 main 为 `986f83e967ed3d092b98bff2060107b4e415c64b`；它不替代启动时的审计基线 `6f09899c74a966a87af03158540a030e5c50817d`，最终发布前必须重新读取 main。
- 应用 PR #239、#241 至 #262 的完整 CI 均已通过且均已合并；这些检查的 deploy 都是 skipped。这些状态均不代表 deploy、线上 browser 或最终 Provider 验收。
- #241 后，`deploy` 只有在 `AICRM_ENABLE_ACTIONS_DEPLOY == 'true'` 时才会创建；本轮所有已完成 CI 的 deploy 均为 skipped，未发生 Actions 生产安装。
- 原工作区存在用户脏修改，本台账对应的文档分支使用独立工作树，不能把原工作区状态当作本分支证据。
- 修复前基线已有独立 PostgreSQL 16.13 的 16 个领域、86 个 package 全部通过且 0 skip；该证据只用于确认测试基线，不代表当前缺陷已经关闭。没有测试数据库时的 skip 只能记录为未验证。
- 最初的只读审计阶段没有生产数据库写、Provider 调用、支付、退款、消息发送或配置变更。之后用户授权的单次渠道验收产生了欢迎和入渠标签两份分离的 executed/provider receipt，且用户实际收到欢迎；这是旧版本原样发送 `{{客户名}}` 的根因证据，不能写成 #257 已上线或变量验收通过。
- 发布前核验只检查了 4 个 key 的有效性和实际进程 6 个允许列表开关的布尔结果，未输出 Secret 原值。测试券窗口的正式浏览器核验只确认配置可见性与日期有效，券仍未发布；“删除草稿”是仅限未发行 draft 的真实写入口，本轮未点击，不能将其作为只读验收的一部分。
- 2026-09-13 的生产匿名外部效果聚合预检：`river_job` 没有未终结任务，`webhook_inbox` 没有可领取的 received/processing/retryable 项，External Effects 也没有 queued、attempted 或 retryable_failed 项；发布重启不会因现存待办自行发起新的 Provider 调用。现存非终态只剩两条历史 `outcome_unknown`：一条 Payment-owned prepay（即 MIG-06 已单列的 Payment #924 / effect #21 风险）和一条 Outbound-owned 素材效果；两者都没有当前 River job，均不得因发布、健康检查或新版本自动 retry/reconcile。该聚合不代替这两条未知结果的既有人工对账，也不代表未来新 webhook 或正常业务写入。
- 2026-09-13 的安装前只读预检：最终构建时须在 `release-files.sha256` 中逐项核对 Excel `components/excel-batches/batches.py`、`components/excel-batches/requirements.txt` 和 `components/excel-batches/aicrm-excel-batches.service`。当前目标机的外置 archive SDK 配置项唯一、库可读、runner 可执行；库 SHA-256 为 `79ced4de6b18d5e96a21cd06f325794dc8957f8925120538d56d4ce827d3dfd0`，与固定官方摘要匹配。当前 release 文件清单与文件系统均不含 `.so`；以服务用户运行的无凭据 health 仅完成 `dlopen → NewSdk → DestroySdk`，返回库可加载且句柄创建成功。此证据不读取密钥、不调用 Provider，也不代表消息存档业务读取、解密或回执验收。
- 发布后线上 SHA、HTTPS、登录态页面与 Provider/回执证据，必须用最终选定发布 SHA（包含本次 PRD/台账）的独立本地产物和构建清单核对，不能以其后文档提交形成自引用证据。
