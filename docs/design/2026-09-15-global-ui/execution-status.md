# Global UI 执行状态与逐页验收账本

更新时间：2026-09-16 09:22:08（Asia/Shanghai；事件时间均保留原始时区）
固定审计基线：a4a89ce55e3dc745aca0baf6634d684700bbce8b
当前 main：09f6d7243228f6415cf550dc8711e28b4003889f
当前原目录：`/Users/qianlan/Downloads/新CRM`（只读保留用户改动）
文档工作树：`/Users/qianlan/aicrm-worktrees/governance-recover-20260915`

本账本把“分支／CI、合入 main、发布、认证浏览器读回、真实业务／Provider 回执”分开记录。目录存在、接口骨架、CI、截图、fixture、Mock、Node、合入或 deploy skipped 都不单独构成业务完成。

## 2026-09-16 当前证据校准

#340、#346、#354、#357、#355、#358、#359、#360 与 #361 已进入该时点的 main；tests/governance 候选仍须随最终 merge 重新绑定。以下是可复核的隔离浏览器事实，不是部署或生产认证 readback。

| 范围 | clean SHA 与实际日志 | 已确认页面事实 | 保留边界 |
| --- | --- | --- | --- |
| remaining 四类 | `e2dded70f8f08c20f2d9080b3bbcd0f716cd65fb`；UTF8 PG Chromium 19.335s | 13 图覆盖 archive entry/detail、Radar、coupon public、shared member grid；根审确认单页头、可见图和当前显示 1 行 | 仅 fixture；等待最终 main 与生产读回 |
| admin desktop | `74743a12a2e21686e67881061c88eccecc7feb17`；UTF8 PG Chromium 49.910s | coupon list/form/data、service list、external-effects、channels new、runtime、API docs、owner migration、member grid 的 1280/1440 几何与可见操作 | CouponData 当时仅几何通过，语义以后一行记录为准 |
| CouponData 修复后 | `2800105c549f68143f36d248bc77e829b35a7042`；83.147s；`coupon-data-final-2800105c8c71/coupon-data-final-utf8.log` | 两桌面图确认 1/100、发行100、指定商品1项、可用状态与编辑表单完整字段；#360=`7552ebd3` 已入 main | artifact suffix 不等于完整 SHA，以本行完整 SHA 为准；未部署/生产 readback 未完成 |
| public survey | `fc8f72393c5633ff1327b705c8cd073ec8c61cb5`；44.853s；`public-survey-final-fc8f7239f54d/public-survey-final-utf8.log` | auth/all/one/error/result 各 375/390/430；OAuth auth/error 是明确停止态，正常提交/result 保持 Owner journey；#361=`09f6d724` 已入 main | artifact suffix 不等于完整 SHA，以本行完整 SHA 为准；未部署/生产 readback 未完成 |

目录 `public-survey-final-fc8f7239f54d` 的唯一最终日志在 09:22:08 覆盖此前同路径的流式输出；不再重复运行或重写该目录。先前语义失败原件仍保留在 `public-survey-mobile-07a41fd4cf5a`。后续每次只使用新 attempt 目录。

## 基线漂移与治理边界

本候选开始核验时 origin/main 为 a4a89ce55e3dc745aca0baf6634d684700bbce8b；随后 #349=8be30e38355df55e15c565b324dfc0f8119a9524、#348=fbf79b645c5626f48cf98fd99bdecfa0163db09e、#350=4ed751f1fd82ebdf8f13f7b1c43e40e5817b4d65、#345=1e91d8f8bff409d6d0e8c91c4191b12cc2efec4a、#352=b4bb355d2b15324748fdc4f4d2a81e6e3523836b、#351=d2eda00380fa3fee94bbd303398228a20bcd2894、#343=d8f4a7360567cf9719cc0e513236b354343474d8 和 #347=040eb6759c3ac3f1d81e78de40d3a4d61b4c1c74 进入主线，随后 #353 以 merge `ada1bdc902a4ba603382d03bc8d78e2983446696` 进入当前 main；CI `35034837242` 全适用 lane、check、quality 通过，clean source `dff54688`、tree `d17da85` 与 review 一致，formal map `166/225`、stage `154/214`。a4 仅作为固定审计基线保留，合入事实不自动升级为部署、认证页面 readback 或业务完成；本任务没有重跑全量验收。

本工作树包含测试、测试配置 getter、治理文档、组件索引和验收账本；不改业务 runtime、不部署、不删除原目录内容。冻结 donor 只作为行为或视觉证据来源，不作为新的能力来源。

## 已合入主线的可观察能力

| 范围 | merge／run 事实 | 已证据 | 当前边界 |
| --- | --- | --- | --- |
| 基础共享组件 | #317 → `84c34e5ad784d3f4cf20082b6a83d39919bff7e5`；CI `34924244166` 必跑通过 | `SelectionSession`、Tag、Staff、Group、Material、Composer/只读内容、状态示例进入主线 | 只证明组件合同；调用页授权、保存读回和 Provider 仍逐页核验 |
| Radar 分页 | #324 → `0e73d42c433892a1f5088eea08d3a079c1c98455`；CI `34926594842` | 管理列表筛选／分页 | 不代替素材保存、发布或业务 readback |
| 经营与导航 | #293 → `cbbca8c64e59b9ad5e1d58026d8cb01c9314bb0e`；#299 → `8dd0ea6b8a6a7f5d790eb4111249bc98a5844cb7`；#339 → `654eec`；#338 → `0f58ef` | `/admin` overview、首页导航、页头状态示例等已进入主线 | 未部署；认证页面和真实经营数据 readback 仍缺 |
| 订单／商品／客户 | #301 → `6400a61bf4b2c95d50cb7eeb9aa2521f9d969133`；#291 → `38ca5ce75374ad3415e6c5615eb66b8db6812fad`；#316 → `645f19b2d5a267ca620d15e19a6f7fe7e26880a5` | 订单分销事实、商品售卖信息分销、canonical customer/OneID profile 已合入 | 生产订单、支付、客户权限读回未完成 |
| 公共商品／支付 | #325 → `be46042f363b6573df11402f70954a3ebe131770`；CI `34938888021` | auth、manifest fail-closed、nested UoW、375/390/430 视口证据 | 未部署，未有支付／退款 Provider 回执 |
| 提交式搜索／IME | #331 → `44c3232394322148ad6c4ccdf4020558ded6b25c`；#332 → `12cb131157caf13f720d779237bb25dc58faab4e` | 残余提交式搜索、共享确认框、取消零写入和调用方幂等边界 | 未部署；渠道空态和业务写入 readback 单独追踪 |
| 企微 sidebar | #330 → `65d69b54a2336c98da9bff2ab218562683205445`；CI `34944435507` | 360/420 scoped presentation、上下文重试隔离和收据边界 | 真实宿主授权、绑定读回、Provider receipt 未完成 |
| 分销管理基础 | #309 → `933a448d9d25312b11429e3fcb57d655dbc4fa5d`；CI `34947729113` | 指标、筛选、表格、详情抽屉、异常文案 | 当前用户提出的顶栏、四指标、tabs/筛选行与观察时间／逐卡说明删除已随 #353 merge `ada1bdc9` 进入当前 main，Distinct order ReadPort 也随该合入绑定；未部署，认证数据 readback 和 Provider/settlement 结果仍待 |
| 群运营、页头、缩略图、固定话术 | #333 → `2c3a98bfaf66dc8acfe7f1e1f31a5fc4e3cf4760`；#335 → `2a4e0dd`；#342 → `f0d737`；#344 → `dca8065f`；#341 → `a4a89ce` | 保存反馈、页头操作、共享缩略图、fixed_script composer PUT→GET 已进入或已核实主线 | deploy/readback/provider 仍分开；fixed_script 不能写成全生命周期已完成 |
| 商品媒体与编辑页 | #349 -> 8be30e38355df55e15c565b324dfc0f8119a9524；#348 -> fbf79b645c5626f48cf98fd99bdecfa0163db09e；#350 -> 4ed751f1fd82ebdf8f13f7b1c43e40e5817b4d65；历史时点 current main=`ada1bdc9` | unified media、商品图片 active draft／排序、商品编辑页共享页头已进入主线；既有 quality evidence 仍按各 PR 保存 | 历史 main 值不覆盖本页 L5 的当前 main；未部署，真实商品素材保存、认证页面 readback 和发布后资产核对仍待验 |
| #349 unified media | head `a36648fc16541c165c85ecb7b3a6f4e93a4a9e75`；CI `34983732835` 全 PASS；merge `8be30e38355df55e15c565b324dfc0f8119a9524`，tree `c1d26d76df6c1f54be76c33b40ffa17d8a8195da` 完全匹配 | actual PG MediaRefresh 27.07、Go 28.534；source/stage hash、formal 3-stage/P5、1440 证据 | #349 已在 main；本候选不重新全量验收，root 负责发布后真实素材 readback |

已合入记录只证明对应范围在 main；`deploy` 为 SKIPPED 的 run 不代表发布。

## 当前候选与实证索引

| 能力／PR | 当前 head 与 CI | 已核实证据 | 未完成边界 |
| --- | --- | --- | --- |
| #343 settlement confirmed | merge `d8f4a7360567cf9719cc0e513236b354343474d8`；CI `35031684418` 全 lanes/check/quality 通过，deploy SKIPPED | root quality snapshot exact success；Distribution-owned settlement facts 与 authority ledger 已按当前 main 绑定 | 已进入 main，仍未部署；settlement readback、生产页面和 Provider 业务结果独立待验 |
| #347 paid records | head `66143cd19c67d2bfd17e66505f54b35d8e18076f`；CI `35033284745` 全 lanes/check/quality 通过；merge `040eb6759c3ac3f1d81e78de40d3a4d61b4c1c74` | root verified clean source `384f383ee8e91047b7865708e61155fa5ad49d25` and tree `238b6b6de1787b36ba086ec71a44dde83e00d2f2`; PG/authority evidence remains scoped | 已进入 main，仍未部署；订单认证 readback、支付／退款／分账和 Provider receipt 未验 |
| #345 admin list visual fixes | head `f8a3658149cbb9a6b25d131244d06a2c9f06d77d`；CI `35027252551` 全适用检查通过；merge `1e91d8f8bff409d6d0e8c91c4191b12cc2efec4a` | local visual/action evidence and exact-head quality retained | 已进入 main，仍未部署；生产页面 readback 未验 |
| #340 channel read state | merge `a01d6f2e19c72500c94c8944dc55c905b63ba8e1`；CI `35036235000` 全适用检查通过 | read-state 证据保留 | 已进入 main，未部署；渠道空态、权限和认证 readback 仍待验 |
| #346/#361 public survey | merges `aec4c2609b016fdf18b2d5cbf546e76f0ce30eb4` / `09f6d7243228f6415cf550dc8711e28b4003889f` | UTF8 PG 下 PublicSurveyPresentation Chromium 实际通过；H5 实际可达 auth/all/one/error/result 的移动证据已绑定 #361 | 已进入 main，未部署；移动端 readback、问卷提交结果和业务写入回执仍需单独验收 |
| 公共分销生命周期／#351 | merge `d2eda00380fa3fee94bbd303398228a20bcd2894`；CI `35030233550` 全适用检查通过，deploy SKIPPED | public authorized read state、375/390/430 和 UTF8/认证边界证据已按 PR 保存 | 已进入 main，仍未部署；公共中心、归因／佣金真实 readback 和 Provider receipt 待验 |
| 管理分销确认／#352 | merge `b4bb355d2b15324748fdc4f4d2a81e6e3523836b`；CI `35028879267` 全适用检查通过，deploy SKIPPED | 共享字段、目标版本与访问代次捕获证据 | 已进入 main，仍未部署；后台认证页面与真实数据 readback 待验 |
| 分销 compact／#353 | merge `ada1bdc902a4ba603382d03bc8d78e2983446696`；CI `35034837242` 全适用 lane、check、quality 通过；clean source `dff54688`、tree `d17da85` 与 review 一致；formal map `166/225`、stage `154/214` | QR dialog、四指标、同排 tabs/filter 与 distinct order ReadPort 已随 merge 进入当前 main | 已进入 main／未部署；认证数据 readback、生产页面和 settlement/Provider 结果仍待验 |
| 问卷列表 read state／#354 | merge `71acf1e81db0743954696999cba18e8864799cc5`；CI `35039557156` 全适用检查通过 | 失败、空态、无匹配和 retry 合同继续独立记录；旧失败/取消 run 不计为通过 | 已进入 main，未部署；认证浏览器与业务写入 readback 待验 |
| Radar CSP／#355 | merge `23a3174260d7f7646fb58c94de4644fc82a7300d`；CI `35042545968` 全适用检查通过 | same-origin tracking 修复范围仅为同源 `connect-src 'self'`；e2 已验收可见 fixture 与 Owner receipt 证据；不涉及 Provider、迁移或身份 | 已进入 main，未部署；公开授权／UnionID assurance 与生产认证 readback 仍待，不为本次 UI 发布触发新的 Provider 效果 |
| archive 单标题／#356→#358 | #356 旧栈候选 `b6097924` 保留为历史；#358 merge `03a06d0a080751d8494d8df45a31639f1cbcf0e9` | 保留共享 topbar，移除 archive list/detail 重复 h2；e2dded 隔离证据已复核 entry 1280/detail 1440 | 已进入 main，未部署；真实 archive readback 待完成；#356 不再作为当前交付 |
| member-grid visible rows／#359 | merge `08a33879d674a22968c2d237145e67a707f3557c`（自有修复 `17be2718`）；双模式 Host/HTTP-JSDOM 定向证据已保存 | 管理员和公开页缺失/null total 都显示“当前显示 N 行”，明确 total 保持；折叠分组不冒充已加载记录；e2dded 隔离证据显示当前 1 行 | 已进入 main，未部署；真实会员 projection readback 待验 |
| Chromium readiness tests／#357 | merge `d9eb7147f6cd20148d7cd2ff35e7354d60b17172` | 原生鼠标动作与有界 readiness polling；不改 runtime 业务代码 | 已进入 main；不构成页面或业务验收 |
| remaining-pages tests-only | clean tree `e2dded70f8f0`；UTF8 PG Chromium 19.335s；日志 `/Users/qianlan/aicrm-artifacts/remaining-pages-chromium-20260916/remaining-pages-e2dded70f8f0.log` | 13 张截图覆盖 archive entry/detail、Radar/Coupon/Grid；桌面与 public survey 扩展证据以本页“当前证据校准”为准 | 隔离测试证据已通过；相关 runtime 均已合入 main，生产认证 readback、部署和 Provider 业务仍未验收 |

证据文件 `/Users/qianlan/aicrm-artifacts/root-ui-review-20260915-341.md` 为本轮主索引；本账本只引用明确命名的 evidence，不全盘扫描日志。`343-distribution-confirmed-510f1b05-verification-20260915.md` 证明 canonical customer/OneID 读取、Distribution-owned PG read projection 和 Go/dedup audit，未证明发布或 Provider 效果。

## 2026-09-16 07:32:41 根审收口补记

- 99 路由矩阵曾确认四类真实证据缺口：archive list/detail（22–23）、Radar public（79）、coupon public claim（84）和 member-grid（50、86）。早期 `eaa84fc5` 的 archive 重复标题、grid `Number(null)` 导致总数 0、Radar 1px fixture 截图全部拒绝；随后 clean tree `e2dded70f8f0` 的 UTF8 PG Chromium 19.335s、13 张截图和根审复核已消除这四个隔离测试问题。桌面补验已覆盖 `/admin/coupons`、couponForm、couponData、`/admin/service-period-products`、`/admin/external-effects`、`/admin/channels/new`、`/admin/api-docs`／runtime releases、`/admin/owner-migration` 与 member-grid 的 1280/1440 几何、单页头和可见操作。问卷的实际公共移动路由是 auth/all/one/error/result，均已有 375/390/430 证据；`index/loading/done/signup/active/expired/pay/qr` 为未挂载 build carrier，渠道没有独立公共移动 UI，均记 N/A 而不伪造流程。以上不替代 production 认证 readback 或 Provider 结果。
- detached `b8691792b1e35de8d7f058fbf0e8bbdbf4dea5d2` 的首次 Linux `release-fast` 包因 AppleDouble 被拒；attempt2 设置 `COPYFILE_DISABLE=1` 后由 root 独立核验通过：458 tar members = 441 regular + 17 directories，440 manifest payload hashes 全匹配，无 AppleDouble、链接、绝对/父路径或重复 canonical path，archive SHA256 为 `1504a8ea17a4b150c914bc43c9e86e3264d36df50beb05da45354c0beb10eb46`。该包仍是 detached b869 的预检、未部署；最终发布必须从 clean merged main 重建。
- 本地中文 PostgreSQL Journey 统一使用 `postgres://qianlan@127.0.0.1:55441/aicrm_ui_utf8_20260916?sslmode=disable`；旧 SQL_ASCII 数据库不能作为中文 PG 测试依据。该修正没有改生产 schema 或新增迁移。
- 生产只读 `/readyz` 仍报告 `f594e33b010db8bef62a9a890c3d80350f2008a0`；认证浏览器／扩展当前不可用，不能写生产页面 readback 或 Provider 业务通过。

## 正式发布路径（只读定位）

正式发布 runbook 是 deploy/README.md：生产 API 监听 127.0.0.1:8080，Caddy 对外代理 id-dev.youcangogogo.com，版本目录为 /opt/aicrm/releases/<40-char-git-sha>，活动版本由 /opt/aicrm/current 指向。CI 的 deploy job 仅在 main 的 check 成功且 AICRM_ENABLE_ACTIONS_DEPLOY=true 时运行；当前变量未启用，推荐 root 使用本地完整包路径。流程仍由 deploy/upload-release-chunks.sh 分块上传、deploy/install-release.sh forward-only 安装并核对 /readyz。SSH 入口只引用配置中的目标参数，本任务没有读取或打印凭证，也没有发起 SSH 发布。

当前推荐的可复用构建命令（最终 clean main tree；本轮只记录命令，不代表完整包已执行）：
export PATH=/private/tmp/aicrm-node-v24.18.0-darwin-arm64/bin:/private/tmp/aicrm-npm-11.12.1-20260915/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/bin:/bin
export AICRM_V2_FROZEN_DONOR_DIR=/private/tmp/aicrm-release-v2-donor-20260915
export AICRM_SIDEBAR_DONOR_DIR=/private/tmp/aicrm-release-production-donor-20260915
export COPYFILE_DISABLE=1
export GOWORK=off
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
export CC='/opt/homebrew/bin/zig cc -target x86_64-linux-gnu'
export GITHUB_SHA=<40-char-final-main-sha>
scripts/run-donor-view-consumers.sh release-fast

已核实前置：`/private/tmp/aicrm-node-v24.18.0-darwin-arm64/bin/node` 为 v24.18.0；npm 工具目录为 `/private/tmp/aicrm-npm-11.12.1-20260915/bin`；V2 frozen donor 为 `/private/tmp/aicrm-release-v2-donor-20260915`（SHA `6bfbe5816bb89913c70adaca87d6a486260e016e`）；sidebar frozen donor 为 `/private/tmp/aicrm-release-production-donor-20260915`（SHA `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`）；CC 为 `/opt/homebrew/bin/zig cc -target x86_64-linux-gnu`。b869 的首次 Linux amd64 cross-build 产物因 AppleDouble archive rejection 被拒，不能作为完整 `release-fast` 或上线包；随后使用 `COPYFILE_DISABLE=1` 重建的 attempt2 已通过 regular-file 集合和 manifest 严格检查，但仍只是 detached b869 预检。最终包需从 clean merged main 重建并再次检查；生成包后由 root 按 `deploy/README.md` 的分块上传和 `deploy/install-release.sh` 继续，本任务不上传、不安装。

只读生产版本核对：2026-09-16 04:27:23（北京时间）请求 `https://id-dev.youcangogogo.com/readyz` 返回 `{"release_sha":"f594e33b010db8bef62a9a890c3d80350f2008a0","status":"ready"}`。该响应证明当前公开 ready 端点报告的版本与运行状态；它不证明新候选已部署，也不替代 root 的认证页面、PostgreSQL、支付／退款、分销、分账或 Provider 回执验收。

可执行上线顺序：#345/#343/#347/#351/#352/#353/#340/#346/#354/#357/#355/#358/#359/#360/#361 已在当前 main；root 继续按当前 SHA 绑定 remaining-pages tests/governance，#356 旧栈不再作为当前交付。完成最终 check/quality 后再从 clean merged main 生成无 AppleDouble 的完整包。随后按 deploy runbook 分块上传和 installer，用 /healthz、/readyz 核对同一 release SHA，再用已登录生产 `/admin/distribution`、商品和素材页面完成认证回读与视口交互核验。支付、退款、归因、系统分账、发送和 Provider receipt 继续按已有合同／既有回执分别记账；本次 UI 发布不要求额外触发新的资金或外部写入。任一证据缺失，相关门禁保持待验收。

## 用户最新分销 UI 调整与验收界线

以下用户调整尚未由当前 main 的页面级生产 readback 证明，不能从 #309 的既有页面证据推断为完成：

1. 顶栏只保留“申请二维码”；移除“打开申请页”“复制申请链接”，二维码展示复用商品管理二级页的分享 dialog／页头操作。
2. 概览压为单行四项：“成交额、待结算佣金、已结算佣金、待处理异常订单”；数字适当放大，删除旧总说明。
3. “分销员、订单、异常”和“当前页筛选”同一行，删除“仅筛选当前已加载页，不扫描后续页，也不代表总数。”
4. 删除观察时间、逐卡口径说明和所有附带脚注；页面只显示四项指标及其真实值，指标字段缺失或为负数时显示“待确认”，真实零值保留。

数据边界仅作为内部取数合同：成交额与期间初始佣金共用支付确认时间；退款和调整不进入初始佣金；待结算、已结算和待处理异常为当前状态；已结算表示系统成功确认。root 已核实 OpenExceptionCount 当前是异常记录数，新增字段必须由 Distribution-owned ReadPort 返回 COUNT(DISTINCT commission.order_id)；既有 record count 保留原消费者，不能直接改 label。字段缺失或为负数时显示“待确认”，真实零值保留，不能以零兜底或静默隐藏指标，也不能把这些语义渲染成卡片说明。该新增字段及本轮 compact 页面已随 #353 `ada1bdc9` 合入当前 main；当前缺口是未部署、认证页面与真实数据 readback，以及 settlement/Provider 结果，不能把 main 合入写成生产通过。

## 逐页路由与视口矩阵

规范链：`canonical route → handler／领域 UI adapter → Render* 或 mount → manifest assets → page caller`。旧 alias/carrier、静态 carrier、reserved placeholder、login/logout、H5/build artifact 单列，不能按新页面重复计数。完整 1–99 条目与类型见 `skills/aicrm-v3-frontend-consistency/references/component-map.md`。

| 页面／范围 | canonical 证据与当前级别 | 视口矩阵 | 缺口 |
| --- | --- | --- | --- |
| `/admin`、overview、orders | `/admin` overview API、订单分销 ReadPort；#299/#301 main；局部 C3 | 后台 `1280/1440` | 未部署、未认证 readback；支付／退款／分账仍独立 |
| /admin/distribution | #309/#343/#347/#352/#353 已在当前 main；#353 merge=`ada1bdc9`，CI `35034837242` 全适用 lane、check、quality 通过 | 后台 1280/1440 | 四指标／二维码 dialog／同排 tabs-filter 已进入 main；认证数据 readback、部署和 settlement/Provider 结果仍待验 |
| /admin/products、product editor、media | #291/#345 main；#348=`fbf79b6`、#349=`8be30e3`、#350=`4ed751f` 均已进入当前 main `23a31742` | 后台 1280/1440 | 上传／排序／共享页头和素材真实保存 readback、发布后资产核对仍待验 |
| `/admin/channels`、group ops、automation | #331/#333 main；fixed_script #344 PUT→GET 可用 | 后台 `1280/1440`；企微相关 `360/420` | 渠道空态、固定话术其余生命周期、真实企微授权／发送收据待验 |
| `/admin/customers/{id}` | #316 canonical customer/OneID profile C3 子旅程 | 后台 `1280/1440` | 列表、权限变化和生产 readback待验 |
| `/admin/radar-links`、`radarForm.html` | #324 局部 C2；#327 已关闭，仅作历史 adapter 记录 | 后台 `1280/1440` | 单项素材保存／GET、PDF/image 替换移除、发布待验 |
| `/admin/component-states` | `internal/webshell/handler.go → RenderComponentStates → componentStatesStyles/componentStatesHost`，C0/C1 | 后台 `1280/1440` | 本地 fixture 不能代真实页面或业务数据 |
| 企微 sidebar | #330 main、360/420 scoped evidence | `360/420` | 真实宿主授权、绑定读回、Provider receipt |
| public product/payment/survey/distribution | #325/#351/#353/#346 main；问卷 #361 候选已有 auth/all/one/error/result 375/390/430 UTF8 PG/Chrome 证据 | 公共 375/390/430 | 发布后认证 readback、问卷提交／业务写入、分销业务／Provider 回执仍需分别验收 |
| H5/build artifacts | auth/all/one/error/result 是实际挂载路由；index/loading/done/signup/active/expired/pay/qr 是 N/A carrier | 仅实际路由 `375/390/430` | artifact 生成成功不代表移动端业务 readback |

C0=源码路由、Host、`Render*`/mount、manifest 静态闭包；C1=共享组件合同和状态行为；C2=实际挂载壳、视口和交互；C3=认证业务数据、关键交互与服务端 readback。发布、线上版本核对、Provider receipt 为额外门禁。每页继续覆盖 loading、空、无匹配、失败、403、只读、真实零值、unknown、取消、重复点击、刷新、分页、IME、权限变化和窄屏。

## 共享组件核对与剩余清单

共享 `SelectionSession`、Tag/Staff/Group/Material adapter、Composer/只读内容、Confirm、页头操作、状态反馈按 V3-owned 入口复用。组件只负责选择、呈现和交互状态；目录、权限、保存、上传、发送、调度、支付和 Provider 由调用方／所属领域负责。

剩余工作：

- root/集成链继续绑定 remaining-pages tests/governance 的最终 SHA、CI、合入状态；#340/#346/#354/#357/#355/#358/#359/#360/#361 已在 main，#356 旧栈候选只保留历史。已有实现但缺页面级视口证据的 route 已按上方收口补记，不视为新增业务页面。
- 对当前 main 已含的 #345/#348/#349/#350 完成无 AppleDouble 最终包、部署后版本、商品／素材认证 readback；不要用静态 carrier、截图或旧 artifact 代替真实保存读回。
- 问卷列表空／无匹配／失败 retry 与 archive、Radar public、coupon public、member-grid 的隔离证据已闭合；生产认证 readback 和服务端业务事实仍待，不把旧 eaa 截图或测试稳定性 PR 写成页面通过。
- 支付、退款、分销归因、系统分账、发送和 Provider 回执按既有合同与既有证据分层记录；不为本轮 UI 发布额外触发新交易或外部写入。
- 恢复 Product Design 路由后补 focused audit；当前 catalog 缺失已如实登记，不能伪造完成。

当前无本任务 production deployment、部署后 readback 或 Provider 业务验收；全局 UI 保持未完成，直到每个范围的相应门禁形成当前证据。
