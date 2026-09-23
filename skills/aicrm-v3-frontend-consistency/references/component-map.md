# 前端真实组件索引（全局 UI 治理核对，2026-09-15）

固定审计基线为 a4a89ce55e3dc745aca0baf6634d684700bbce8；当前 main 为 `09f6d7243228f6415cf550dc8711e28b4003889f`，已含此前记录的 #345/#348/#349/#350/#352/#351/#343/#347/#353、#340/#346/#354/#357/#355，以及 #358=`03a06d0a`、#359=`08a33879`、#360=`7552ebd3`、#361=`09f6d724`。tests/governance 候选仍须随最终 merge 重新核对。索引用于选择现有入口，不授权跨领域读取或写入；动工前除路径外，还要追踪 canonical 路由、handler／adapter、Render* 或挂载、manifest assets 和页面调用。链路固定为 canonical route -> handler／领域 UI adapter -> Render* 或对应 mount -> manifest asset -> page caller。未挂载实现、历史快照和构建产物不能单独作为当前标准；冻结 donor 只作行为／视觉证据，禁止修改。

2026-09-16 路由证据补记：`/admin/couponData.html` 继续由 Coupon Owner → frozen `AdminController` → `couponAdapter` 装配；展示只能使用该页已有 Coupon DTO，技术 `targetRefs` 不能跨 Product Owner 反查。`/q/{slug}` → Survey OAuth/session Owner → `h5AuthAdapter` → `surveyPublicHost` → frozen H5 runtime；#361 已在构建阶段为 auth/all/one/result/error 注入 Host 与样式，实际可达 H5 状态是 auth/all/one/error/result，分别有 PostgreSQL Chromium 375/390/430 证据。`index/loading/done/signup/active/expired/pay/qr` 仅是 build carrier，渠道没有独立公共移动 UI；它们不作为缺页开发或成功业务流。隔离浏览器图仍不等于 production readback。

| 场景 | 入口 | 调用／装配 | 边界 |
| --- | --- | --- | --- |
| 管理端单壳 | `internal/webshell/templates/admin_base.html` | `internal/webshell/renderer.go` 的 `RenderDistribution` | 仅一个 `.admin-sidebar` 和 `.admin-topbar`；分销领域只提供经 manifest 验证的正文 assets。 |
| 企微客户侧边栏 | `web/v3/sidebar/main.ts`、`web/v3/sidebar/presentation.css` | `RenderSidebar` → sidebar manifest assets → `sidebar_workbench_v3_overlay.js` | 仅复用签名 context 与 `SidebarBridge`；不挂员工、群、标签 picker 或自动化话术目录。403／context 失效清空客户缓存和操作入口；素材标签来自 Media Owner。 |
| 问卷管理与编辑器 | `web/v3/surveyAdapter.ts`、`web/src/admin/sections/questionnaireEditor.ts` | `internal/survey/ui.go` 解析 manifest，`RenderSurvey` 在冻结 admin bundle 前装配列表 bridge；编辑器由 `questionnaireEditor` 入口挂载 | 一级列表复用冻结 table、检索和操作；bridge 仅映射问卷名称。编辑页保留标题用于答题/预览，状态以服务端 readback 为准。 |
| 公共问卷 H5 | `web/v3/surveyPublicHost.ts`、`web/v3/surveyPublic.css` | `internal/survey/http.Handler.publicEntry` 的 `/q/{slug}` 保留 OAuth/session 入口；构建阶段为 auth、all、one、result、error、done 注入 Host 与样式，再由 Survey manifest 装配 | Host 只呈现既有题目、校验、提交中、错误、失效链接、默认完成页与结果读回状态；提交 key、答案写入、OAuth/OneID、completion action 和结果 token 始终归 Survey Owner。 |
| 通用详情抽屉 | `web/v3/shared/ui/detailDrawer.ts`、`web/v3/shared/ui/detailDrawer.css` | `distributionAdmin.ts` 导入行为，`sharedDetailDrawerStyles` 由 `admin_base` 挂载 | 组件只负责焦点回收、Escape/关闭和视觉容器；调用方必须读取自己的受权服务端事实，不能由抽屉伪造状态。 |
| 顶栏动态操作 | `web/v3/shared/ui/pageHeaderActions.ts` | 挂载既有 `.admin-topbar > .admin-topbar-meta`；`distributionAdmin.ts`、图片库与 `pageHeaderActionHost.ts`（企微标签、AI 计划详情）复用 | SSR `PageAction` 仅承载链接；客户端命令可新建，或以 `mountPageHeaderActionElements` 移动调用页已绑定的原节点。不得创建第二标题栏、复制领域命令或跨 owner 转移控件。 |
| 商品编辑分销配置 | `web/v3/productAdapter.ts`、`web/v3/productDistribution.css` | Product UI 的 `ProductCSS` 经 `admin_base` 装配 | 仅“售卖信息”承载启用分销开关、本商品佣金比例与退款复核等待天数；其他维度不写 `distribution_policy`，商品编辑页不提供申请链接、复制或二维码。公共分销中心保持其既有入口；不新增壳、配色、身份或资金模型。 |
| 分销管理详情 | `web/v3/distributionAdmin.ts`、`web/v3/distribution.css` | `RenderDistribution` 的 `#distribution-admin-root` 和 Distribution manifest assets | 只使用现有后台 table、tab、button token；公共分销中心样式必须限定在 `body[data-ui-surface="distribution"]`，不能重置后台全局样式。 |
| 分销主线状态 | `distribution-center-lifecycle-20260915`、`distribution-admin-confirmation-20260915`、`codex/distribution-admin-compact-delivery-20260916` | 公共 #351 已合入 `d2eda003`；后台 #352 已合入 `b4bb355d`；compact #353 已合入 `ada1bdc902a4ba603382d03bc8d78e2983446696`，CI `35034837242` 全适用 lane、check、quality 通过，clean source/tree `dff54688`/`d17da85` 与 review 一致 | #351/#352/#353 已进入 main 但均未部署；最终发布、认证 readback、settlement/Provider 结果仍待完成，不能把 main 合入当作现行生产页面。 |
| 素材缩略图共享呈现 | `web/v3/shared/ui/materialThumbnailPresentation.ts`、`web/v3/shared/ui/materialThumbnailPresentation.css` | #342 的共享 thumbnail、#349 unified media、#348 商品图片 active draft／排序和 #350 编辑页共享页头通过 `RenderMedia`／产品 caller 装配；三个 canonical admin route 是 `/admin/image-library`、`/admin/miniprogram-library`、`/admin/attachment-library`，renderer keys `images`/`mpLib`/`attach` 与 donor 文件名只是装配标识；`/api/admin/attachment-library/upload` 仅为 API 兼容别名 | 只负责已授权 URL 的 loading／loaded／error／no_url 呈现；不读库、不上传、不把 static carrier 当媒体能力。#349=`8be30e3`、#348=`fbf79b6`、#350=`4ed751f` 均已进入当前 main `23a31742`；真实 PG/Go、商品保存和发布后 readback 仍按 caller 复核。 |
| 固定话术 composer | `web/v3/shared/ui/contentComposer.ts`、`contentPresentation.ts` | #344 `dca8065f`／CI `34967296577`；`agentEdit.html` 通过 automation Host 装配 fixed_script PUT→GET | fixed_script 已有授权保存回读；Prompt 与其它 lifecycle 仍由各自 Owner 维护，不能用 fixed_script 证据升级整个 automation 页面。 |

## 全局统一 UI 增补清单（2026-09-15）

本节登记当前 main 的实际 Host、授权读取和未完成合同。#317 已以 merge 84c34e5ad784d3f4cf20082b6a83d39919bff7e5 合入共享组件；随后 #324、#293、#301、#291、#316、#329、#299、#325、#331、#332、#330、#309 和外部 #333 分别合入或登记 Radar 分页、经营汇总 API、订单分销事实、商品售卖信息分销、客户档案、状态示例、经营首页/导航、公共商品/支付呈现、残余提交式搜索、共享确认框、企微 sidebar、分销管理与群运营保存/启用反馈；当前 main 另含 #345、#349、#348、#350、#352、#351、#343、#347、#353、#340、#346、#354、#357、#355。治理审计基线仍为 a4a89ce55e3dc745aca0baf6634d684700bbce8b。上述已合入项均未因此完成部署或生产读回。未真实页面验收的调用不得因共享模块存在而标覆盖。商品编辑分销行由商品专项 PR 维护；公共商品/支付呈现仍需发布门禁和真实支付回执。

| 场景 | 入口 | 调用／装配 | 已核实状态与边界 |
| --- | --- | --- | --- |
| 管理端全局反馈 | `web/v3/surfaceFeedbackHost.ts` | 构建脚本向生成的 admin、H5、sidebar 与分享文档注入反馈 Host | 只映射加载／资源失败表现；不把业务失败伪装成成功，也不改变领域命令。 |
| 已提交文本搜索，第一批 | `web/v3/shared/ui/committedTextSearch.ts` | 渠道：`channelCenterAdapter.ts`；标准选择器：`standardComponentsHost.ts`；其他生成页面：`surfaceFeedbackHost.ts` | **#287 能力已随 #317 进入 main；#331 残余提交式搜索已以 merge `44c3232394322148ad6c4ccdf4020558ded6b25c` 进入 main，run `34940198506` PASS，deploy SKIPPED。** 只精确注册渠道、优惠券、问卷、标签、计划、素材库与标准群聊／标签／客服／素材选择器控件；不全局拦截表单。渠道真实 Chromium 输入法 Journey 由 run `34876725254` 通过，普通 Enter／空词回到调用方授权目录，composition 候选 Enter 不触发查询；渠道空列表当前只有表头、缺空态，作为独立待修缺口，不推断整套渠道失效。 |
| 标签选择器 | 冻结 `wecom_tag_picker.js`；V3 `standardComponentsHost.ts` 与 `web/v3/shared/ui/tagPickerAdapter.ts` | 当前真实 V3 caller 为 Channel、Customer、Product；各自 Host 提供页面授权的目录 loader 与 commit 回调 | #305 能力已随 #317 进入 main；#329 已把 Tag 状态卡片（loading、empty、error、forbidden、readonly、invalid、刷新失败保留）与 IME/焦点示例带入 main。共享合同覆盖分组、搜索、单／多选、回显、失效、403、刷新和失败保留；选择器不替客户打标，问卷等未列 caller 不得套用。 |
| 客服／员工选择器 | 冻结 `operation_member_picker_dd8d60d.js`；V3 `standardComponentsHost.ts` 与 `web/v3/shared/ui/staffPickerAdapter.ts` | Channel 与 GroupOps 使用 V3 adapter；兼容 Host 仍负责冻结员工 picker 的加载与方法捕获 | #311 能力已随 #317 进入 main；#329 已把 Staff 状态卡片与授权/刷新失败示例带入 main；Channel 有真实授权 scope、刷新和保存前草稿测试。非群运营调用不能套用 GroupOps scope，Customer／owner migration 仍需真实页面验收。 |
| 真实群聊选择器 | 冻结 `group_chat_picker.js`；V3 `selectionSession.ts`／`groupPickerAdapter.ts` | #300 能力已随 #317 进入 main；GroupOps 使用 V3 session，其他页面仍由各自 Host 决定是否挂载 | Owner scope 分页、失效回显、`chat_reference`、保存锁和保存读回由 #300 分支 CI `34892980966` 覆盖；该 run 没有部署。群邀请素材命令不在此项，其他调用不得套用 GroupOps 证据。 |
| 素材选择器：Radar | `web/v3/shared/ui/selectionSession.ts`、`materialPickerAdapter.ts`、`radarAdapter.ts` | #296 能力已随 #317 进入 main；`RenderRadar` 的 `RadarAssets.StandardHostJS` 加载标准 Host，Radar 注入图片／附件目录 loader | adapter 状态测试和 Radar renderer callback Journey 只证明单项添加与弹窗行为；Radar owner 只允许一个 image 或 PDF `media_item_id`，仍缺真实单选／替换／移除／重开回显和业务 save/GET。组件不读取 `AdminApi`、不扩大目录 scope。 |
| 素材选择器：产品及其他调用 | web/v3/productAdapter.ts、web/v3/shared/ui/selectionSession.ts／materialPickerAdapter.ts | RenderProducts 的 ProductAssets.StandardHostJS 与 Product Host；#341=a4a89ce5、#348=fbf79b6 已把 typed media draft、active draft 图片上传／排序接入当前 main，#350=4ed751f1 统一编辑页共享页头 | Product caller 保留单项 owner callback 与 typed media_item_id 草稿边界；当前 main 已具备这些装配，真实商品保存、排序和页面 readback 仍待验。该证据不等同于所有 caller 已迁移为多选 V3 session；调用方仍需提供受权 loadPage、实际 selectedRecords 和 onCommit。
| 话术／内容编辑器 | 冻结 `send_content_composer.js` 与 V3 `standardComponentsHost.ts`；V3 `contentComposer.ts`／`contentPresentation.ts` | #307/#310 能力已随 #317 入 main；GroupOps 与 `excelBatches.ts` 使用 V3 composer／readonly presentation；#344 `dca8065f` 已为 fixed_script 提供授权 PUT→GET | 共享合同含编辑、素材排序、预览和只读展示，预览不能触发发送；#329 已把 Composer 编辑、预览、只读、校验失败和素材状态示例带入 main。fixed_script 的主线证据归 #344；Prompt 与其它生命周期仍需各自 Owner 证据，不能合并成全自动化完成。 |
| 经营首页与导航 | overview 的 `overviewAdmin.ts`、`navigationHost.ts`、`admin-navigation.v3.json` | [PR #293](https://github.com/qianlan33333-png/AI-CRM-v3/pull/293) merge `cbbca8c64e59b9ad5e1d58026d8cb01c9314bb0e` 提供 API；[PR #299](https://github.com/qianlan33333-png/AI-CRM-v3/pull/299) merge `8dd0ea6b8a6a7f5d790eb4111249bc98a5844cb7` 装配正文与导航；run `34935805795` 全必跑 lane PASS，deploy SKIPPED | C3 子旅程：四项 primary、两项 secondary、趋势、分销期间/库存口径和七组导航有主线测试；未发布、未做生产认证 readback。 |
| 订单分销事实展示 | 交易 Order Host 与 distribution Stable Read Port | PR #301 merge 6400a61bf4b2c95d50cb7eeb9aa2521f9d969133；run 34930500560 全必跑 lane PASS，deploy SKIPPED；paid records 证据位于 #343 之后的 #347 集成栈，旧验证 head c968f733／CI 34982622904 仍作历史证据 | C3 子旅程只读取订单级冻结快照、复核期、预计／实际结算及异常证据；不得读取商品当前比例或跨领域写入。#347 authority ledger 的 PG 62.354 与 quality snapshot 已核实，最终 head、合入、部署和生产订单 readback 仍由 root 绑定。

未列为「已核实」的页面不因路径相似而复用上表读取范围或命令。每次迁移更新本表时记录 canonical 路由、实际 Host／asset、数据来源和授权范围、选择回调、状态验收及尚未完成合同项。

## 共享组件状态示例与调用清单（2026-09-15）

`/admin/component-states` 是当前 main 中由 `internal/webshell/handler.go` → `Renderer.RenderComponentStates` → manifest 的 `componentStatesStyles`／`componentStatesHost` 挂载的认证后台状态示例页。它只使用本地内存 fixture，不保存业务数据、不读取 Provider；PR #317 的 run `34924244166` 必跑 lane 全 PASS，不能因此升级任何真实业务页面。

| 组件 | V3-owned 入口 | 当前真实调用 | 状态示例 | 当前级别与缺口 |
| --- | --- | --- | --- | --- |
| SelectionSession／dialog | `web/v3/shared/ui/selectionSession.ts`、`selectionDialog.ts` | Group、Material、Tag、Staff adapter 共用；调用方持有目录读取和 commit | component-states 覆盖 Group／Material 的 loading、empty、error、forbidden、readonly、invalid，以及表单／IME Enter 和焦点回收 | C1 合同＋C2 挂载／视口；不证明业务数据或保存成功 |
| Group | `web/v3/shared/ui/groupPickerAdapter.ts` | `groupOpsHostAdapter.ts`、`componentStatesHost.ts` | 本地 Group loader 与 #300 GroupOps 测试 | C3 仅限 #300 的 GroupOps 子旅程；其它页面保持 C0，不能套用 Owner scope |
| Material | `web/v3/shared/ui/materialPickerAdapter.ts` | `groupOpsHostAdapter.ts`、`radarAdapter.ts`、`productAdapter.ts`、`componentStatesHost.ts` | 本地 Material loader；#296 adapter 状态／Radar callback 证据 | C2 仅限弹窗／状态子集；Radar 只允许一个 image 或 PDF `media_item_id`，仍缺单选／替换／移除／重开回显和 save/GET；Product 等其它 caller 的多选由各自 owner 决定 |
| Tag | `web/v3/shared/ui/tagPickerAdapter.ts`、`standardComponentsHost.ts` | `channelAdmissionHost.ts`、`customerAdapter.ts`、`productAdapter.ts`；冻结 `wecom_tag_picker.js` 由 standard Host 加载 | #329 已把 Tag 状态卡片（loading、empty、error、forbidden、readonly、invalid、刷新失败保留）与 IME/焦点示例带入 main | 共享合同 C1；每个真实 caller 按自己的 source／scope 验收，不能以 component-states 升级；确认不自动给客户打标。 |
| Staff | `web/v3/shared/ui/staffPickerAdapter.ts`、`standardComponentsHost.ts` | `channelAdmissionHost.ts`、GroupOps Host；冻结 `operation_member_picker_dd8d60d.js` 仅作 donor | #329 已把 Staff 状态卡片与授权/刷新失败示例带入 main | 共享合同 C1；Customer、owner migration 和非群运营范围的真实页面验收仍缺；不能套用 GroupOps scope。 |
| Composer／只读内容 | `web/v3/shared/ui/contentComposer.ts`、`contentPresentation.ts` | `groupOpsHostAdapter.ts`、`excelBatches.ts`；fixed_script 由 #344 main `dca8065f` 提供 | #329 已把 Composer 编辑、预览、只读、校验失败和素材状态示例带入 main | 共享合同 C1；预览不触发发送；#344 fixed_script 授权 PUT→GET 证据已进入主线，Prompt、其它 lifecycle、发布和线上回读仍独立验收。 |

状态示例的 C1/C2 只标示组件合同和实际挂载的证据范围。Tag、Staff、Composer 的状态卡片已随 #329 进入 main，但不因 `standardComponentsHost` 可加载、donor 存在或某个 CI lane 通过而把其它后台、企微 sidebar、H5 或公开 survey 页面标为完成。

## 路由／页面条目逐项验收矩阵（2026-09-15）

本矩阵以历史 clean main 3eda04cbd56d7bfdf44ba2a15d573ee29703926a 的实际路由装配为初始盘点基线；固定审计基线为 a4a89ce55e3dc745aca0baf6634d684700bbce8，当前 main 为 `09f6d7243228f6415cf550dc8711e28b4003889f`，已含 #345、#348、#349、#350、#351、#352、#343、#347、#353、#340、#346、#354、#357、#355、#358、#359、#360、#361。未合入能力的 branch head、CI 和本地证据单独记录，不能写成 main 已发布；候选 head 与 main、部署、认证 readback 分栏理解。条目总数以表中连续编号自动核算，涵盖 canonical route、alias、reserved placeholder、登录／退出和构建 artifact，不能描述为相同数量的 canonical 页面。C0 只表示源码路由、Host 和 assets 静态核对，不能当作页面通过；C1 是共享组件合同测试，C2 是实际挂载壳与视口证据，C3 才是认证业务数据和保存后读回。视口要求按页面类型执行：后台桌面 1280/1440、企微 sidebar 360/420、公开或 H5 375/390/430。除已测证据明确列出的子集外，表中的状态统一表示未执行/待验收的逐项检查集合。

99 条连续条目是治理台账，不是页面数量或 99 个页面全通过：当前表混合 canonical route/view、alias/carrier、reserved placeholder、login/logout 和 H5/build artifact。alias 只验证重定向、query、权限和 active state，不能复制 canonical 页面证据；reserved 只记录受控不可用态；login/logout 单独验证认证边界；artifact 只记录生成器与资源闭包。每个条目仍按 C0–C3 逐项升级，任何未知、无权限、不可用、真实零值和失败必须保持可区分。

Host／assets 缩写：`WB`=`webshell.RenderAdmin` + `admin_base`；`CH`=`RenderChannels` + `channelCenterHost/standardComponentsHost`；`SUR`=`RenderSurvey` + `surveyHost/questionnaireEditor/standardComponentsHost`；`RAD`=`RenderRadar` + `radarHost/standardComponentsHost`；`GRP`=`RenderGroupOps` + `groupOpsHost/operationPicker/groupPicker/materialPicker/composer/readonly`；`AI`=`RenderAIAssistant` + `aiAssistantHost` 及 group/material/composer/readonly；`ORD`=`RenderOrders` + `orderHost`；`PROD`=`RenderProducts` + `productHost/standardComponentsHost`；`COUP`=`RenderCoupons` + `couponHost`；`MED`=`RenderMedia` + `materialSaveHost/imageLibraryFilterHost`；`AUT`=`RenderAutomation`；`OP`=`RenderOperationCycles` + `operationCyclesHost`；`CFG-V3`=`RenderRuntimeConfig`；`CFG-D`=`RenderConfig`；`DIST`=`RenderDistribution` 或公开 Distribution handler；`PUB-*` 为各领域公开 handler；`LOGIN`/`SIDE` 为 `RenderLogin`/`RenderSidebar`。`E-T`=共享组件合同测试，`E-M`=素材 360/420/1280 视觉参考与 Radar Host 布局证据，`E-G`=群聊 360/420/1280 视觉参考，`E-PG`=GroupOps PostgreSQL/认证 Chromium Journey，`E-CH`=#287 渠道 Journey，`E-O`=#299 本地 consumer 证据，`E-ORD`=订单／下钻截图；历史或失败 CI 不计为通过，deploy skipped 不计为发布。

远程选择器的空词 Enter 必须从服务端重新请求调用方授权的无关键词分页目录；只有渠道本地过滤可以回到当前页面已加载的全量结果。选择器共享合同还需分别观察 IME 候选 Enter/Escape、取消不提交、403 保留 draft、跨页选择、`chat_reference` 与素材 key 隔离、只读和焦点、保存锁及失败后读回。

| # | 路由／页面条目（类型） | Host | assets | 组件 | 视口 | 状态 | 已测证据 | 待办 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | `/` → `/admin`（根跳转） | WB | admin shell | `admin_base` | 1280/1440 | 重定向、未登录、已登录 | C0：`cmd/aicrm/composition.go` 路由核对 | 认证浏览器确认最终落点和导航 active 状态 |
| 2 | `/admin`（首页/`index.html`） | WB | admin shell | `admin_base` | 1280/1440 | loading、空、错误、403、只读 | C3 子旅程：#299 merge `8dd0ea6b8a6a7f5d790eb4111249bc98a5844cb7`，run `34935805795` 全必跑 lane PASS，#293 API merge `cbbca8c64e59b9ad5e1d58026d8cb01c9314bb0e` | overview 正文、V3 导航、生产发布和认证页面 readback 仍待验收；不把主线代码或本地下钻 evidence 当线上完成 |
| 3 | `/admin/automation-conversion`（`automation.html`） | WB | admin shell + audience assets | `admin_audience` | 1280/1440 | loading、空、错误、筛选、只读 | C0：`renderer.go` audience 分支 | 自动化运营真实数据、分页、错误和权限态逐页验收 |
| 4 | `/admin/automation-conversion/packages/{id}`（`audienceEdit.html`） | WB | admin shell + audience detail assets | `admin_audience_detail` | 1280/1440 | loading、不存在、错误、只读 | C0 | 真实方案详情读回和返回列表路径 |
| 5 | `/admin/operation-cycles`（`cycles.html`） | OP | tokens/labs/`operationCyclesHost` | 运营闭环 Host | 1280/1440 | loading、空、失败、只读、执行中 | C0：`internal/operationcycle/ui.go` | 真实周期、执行事实、复盘数据和错误态 |
| 6 | `/admin/operation-cycles/cyclesDetail.html?id={ordinal}`（`cyclesDetail.html`） | OP | 同上 | 周期详情/执行记录 | 1280/1440 | loading、不存在、失败、只读 | C0 | 详情和执行记录服务端 readback；`cycles.html` 旧 alias 只应重定向 |
| 7 | `/admin/automation-conversion/group-ops/ui` → `/admin/groupops.html`（alias carrier） | GRP | groupops bundle | GroupOps list Host | 1280/1440/420/360 | 重定向、未登录、loading | C0：`groupops/ui.go` | 认证浏览器确认 alias 不产生第二套页面 |
| 8 | `/admin/automation-conversion/group-ops/groups/ui` → `/admin/groupops.html`（groups alias） | GRP | groupops bundle | Group directory entry | 1280/1440/420/360 | 重定向、loading、403 | C0 | alias 的 scope、布局和权限读回 |
| 9 | `/admin/groupops.html`（active list） | GRP | groupops/standard picker/composer/readonly | 群运营计划列表 | 1280/1440 | loading、空、错误、403、只读 | E-T；C1 仅共享合同 | #300 能力已随 #317 进入 main；CI `34892980966` 的 plan/preflight/frontend/browser/backend/archive/check 全通过，deploy/quality-report skipped；仍缺发布后认证页面读回 |
| 10 | `/admin/groupopsDetail.html?id={id}`（`groupopsDetail.html` active） | GRP | 同上 | 计划详情、群聊选择器、素材选择器、内容 composer | 1280/1440/420/360 | loading、404、403、部分失败、保存中、保存后 readback | E-T、E-G；#300 的 E-PG 子旅程 | #300 能力已随 #317 进入 main；Owner scope、`chat_reference`、保存锁和保存后 readback 有分支 CI 证据；新认证 Journey、发布和其它调用页仍待验收，截图只作视觉参考 |
| 11 | `/admin/groupopsDetail.html?id={id}&history=1`（history） | GRP | readonly + donor history bundle | 只读历史详情 | 1280/1440 | loading、空、失败、只读 | C0 | 历史事实和只读操作逐页浏览器验收 |
| 12 | `/admin/automation-conversion/group-ops/plans/{id}` → detail（canonical API-shaped alias） | GRP | groupops bundle | 详情 Host | 1280/1440 | 重定向、未登录、不存在 | C0 | 确认 alias 保留 query/授权且不绕过 canonical detail |
| 13 | `/admin/channels`（`channels.html`） | CH | channel + standard CSS/Host | 渠道码列表、提交式本地筛选 | 1280/1440 | loading、空、错误、403、历史只读 | E-CH：#287 run `34876725254`；#331 merge `44c3232394322148ad6c4ccdf4020558ded6b25c`，run `34940198506` PASS，deploy SKIPPED | 认证 Chromium 渠道 Journey 已验证 composition 候选 Enter 不过滤、普通 Enter 单次提交和焦点／选区保留；当前空列表只有表头、缺空态，作为单独待修缺口，不能扩写为整套渠道失效；其它列表状态、发布读回和非渠道 caller 仍待验收 |
| 14 | `/admin/channels/new`（`channelForm.html` new） | CH | 同上 | 渠道表单、群聊/标签/素材入口 | 1280/1440 | 草稿、校验失败、403、保存成功/失败 | C0 | 真实创建回读、取消不提交和外部效果前置条件 |
| 15 | `/admin/channels/{id}/edit`（`channelForm.html` edit） | CH | 同上 | 渠道编辑、客服/标签/素材 | 1280/1440 | loading、404、脏表单、保存失败、只读 | C0 | 真实编辑读回、权限边界和 Provider receipt |
| 16 | `/admin/cloud-orchestrator/plans`（`aiassistant/list.html`） | AI | AI host + group/material/composer/readonly | AI 助手计划列表 | 1280/1440 | loading、空、错误、403、只读 | C0：`aiassistant/ui.go` | AI 计划真实数据、审阅状态和执行事实逐页验收 |
| 17 | `/admin/cloud-orchestrator/plans/{id}`（`aiassistant/detail.html`） | AI | 同上 | AI 计划详情、审阅/执行 readback | 1280/1440 | loading、404、审阅中、失败、outcome_unknown | C0 | 真实审阅回读与 External Effects receipt；不能把 queued 当 executed |
| 18 | `/admin/cloud-orchestrator/campaigns`（reserved placeholder） | WB | admin shell | 受控 placeholder | 1280/1440 | unavailable、403、只读 | C0：webshell admin spec | 需确认是否保留入口或绑定实际 Campaign Host，不能按 AI 计划页通过 |
| 19 | `/admin/cloud-orchestrator/observability`（reserved placeholder） | WB | admin shell | 受控 placeholder | 1280/1440 | unavailable、错误、只读 | C0 | 观测真实 projection 和状态颜色逐页验收 |
| 20 | `/admin/external-effects` → `/admin/campaigns.html?view=external-effects` | WB/External Effects | tokens/labs/admin | External Effects shell | 1280/1440 | 重定向、loading、空、错误、outcome_unknown | C0：`externaleffects/ui.go` | 外部效果只读事实、对账和 job query 的认证浏览器 readback |
| 21 | `/admin/campaigns.html?view=external-effects&job={id}`（campaign artifact） | WB/External Effects | 同上 | 外部效果详情/历史 | 1280/1440 | loading、404、accepted/attempted/unknown/reconciled | C0 | 逐项绑定 execution status 和 receipt；不把历史 CI 当页面证据 |
| 22 | `/admin/message-archive`（archive list） | WB | admin shell + archive assets | 客户/会话存档列表 | 1280/1440 | loading、空、错误、403、只读 | C2（隔离）：clean tree `e2dded70f8f0` 已复核 entry 1280；#358=`03a06d0a` 已入 main | 旧证据的重复 topbar/card title 已被拒绝；隔离单标题几何通过，认证客户选择、已入库消息 readback 和 PII 脱敏仍待生产验收 |
| 23 | `/admin/message-archive/customers/{id}`（archive detail） | WB | 同上 | 客户会话详情 | 1280/1440 | loading、无记录、错误、403、只读 | C2（隔离）：clean tree `e2dded70f8f0` 已复核 detail 1440；#358=`03a06d0a` 已入 main | Customer/Identity 归属、时间线真实数据和发布后 readback 仍待验收 |
| 24 | `/admin/customers`（`customers.html`） | WB | admin shell + customer Host | 客户列表、筛选、分页 | 1280/1440 | loading、空、错误、403、只读 | C0：`renderer.go` customers 分支 | OneID/客户 API 认证读回、分页和手机号脱敏 |
| 25 | `/admin/customers/{id}`（`customerDetail.html`） | WB | admin shell + customer assets | 客户档案与历史入口 | 1280/1440 | loading、404、冲突、错误、只读 | C3 子旅程：#316 merge `645f19b2d5a267ca620d15e19a6f7fe7e26880a5`，run `34933355598` 全必跑 lane PASS，canonical customer/OneID/phone/profile/tab 证据 | 订单、问卷、存档分区逐页认证读回、发布仍待验收；main 代码不等于线上页面完成 |
| 26 | `/admin/user-ops/ui`（reserved placeholder） | WB | admin shell | 漏斗/用户运营 placeholder | 1280/1440 | unavailable、403、只读 | C0：route registry | 不得把 HXC 或 overview 证据套用到此页；确认真实 Host |
| 27 | `/admin/hxc-dashboard`（HXC dashboard） | WB/HXC | tokens/labs/hxc admin | HXC 投影 | 1280/1440 | loading、空、错误、403、只读 | C0：HXC UI binding | 真实投影、指标来源、筛选和错误态 |
| 28 | `/admin/hxc-send-config`（reserved config） | WB | admin shell | 受控 placeholder | 1280/1440 | unavailable、403、只读 | C0 | 绑定或明确下线此 route；不与自动化话术混验 |
| 29 | `/admin/questionnaires`（`questionnaires.html`） | SUR | survey host + standard tag CSS | 问卷列表、提交式筛选 | 1280/1440 | loading、空、错误、403、历史只读 | C0；组件合同未接入 | 真实问卷/版本/答卷 readback 和筛选状态 |
| 30 | `/admin/questionnaireDetail.html`（`questionnaireDetail.html` new） | SUR | editor + survey assets | 问卷编辑器 | 1280/1440 | 草稿、校验失败、取消、保存失败 | C0 | 编辑器真实保存回读、预览不触发发送、素材/标签授权 |
| 31 | `/admin/questionnaireDetail.html?id={id}`（edit） | SUR | 同上 | 问卷编辑器/版本 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 版本和题目数据真实 readback |
| 32 | `/admin/questionnaireDetail.html?mode=assessment`（assessment） | SUR | survey host + editor | 评估只读/结果视图 | 1280/1440 | loading、空、失败、只读 | C0 | 评估数据、权限和无答卷态 |
| 33 | `/admin/questionnaireOps.html?id={id}`（`questionnaireOps.html`） | SUR | survey host + standard CSS | 问卷运营/历史 | 1280/1440 | loading、空、错误、403、只读 | C0 | 外部效果回执与历史 readback；不把问卷列表证据复用为完成 |
| 34 | `/admin/questionnaires/new`（registry route） | SUR | survey assets | 预期 questionnaire detail carrier | 1280/1440 | 404/redirect、未登录 | C0：route registry；当前 `surveyPage` 未接受该路径 | 修正 canonical route 或明确失效，避免导航指向未挂载页 |
| 35 | `/admin/radar-links`（`radar.html`） | RAD | radar + standard Host | 内容雷达列表 | 1280/1440 | loading、空、错误、403、只读 | C2 子旅程：#324 merge `0e73d42c433892a1f5088eea08d3a079c1c98455`，run `34926594842` 必跑 lane PASS、deploy SKIPPED；E-M/E-R 只覆盖 picker 布局 | Radar 筛选分页的认证浏览器读回、统计 projection、权限和状态仍待验收 |
| 36 | `/admin/radarForm.html`（`radarForm.html` new） | RAD | 同上 | Radar form + material picker | 1280/1440 | 草稿、候选 Enter、Escape、素材 403、保存失败 | E-M、E-R；#341 `a4a89ce5` 已把 typed media draft 接入主线，页面仍保留单项 owner callback | Radar 只允许一个 image 或 PDF `media_item_id`；仍需真实单选、替换、移除、重开回显和业务保存/失败读回 |
| 37 | `/admin/radarForm.html?id={id}`（edit） | RAD | 同上 | Radar edit + material picker | 1280/1440 | loading、404、已有素材、取消、错误、只读 | C0；无真实单项业务 evidence | 回显实际素材记录、删除/替换、保存后服务端 readback |
| 38 | `/admin/radarDetail.html?id={id}`（`radarDetail.html`） | RAD | radar + standard Host | 雷达详情、访客/统计 | 1280/1440 | loading、无事件、错误、403、只读 | C0 | 统计可用/不可用和访客归因逐页验证；OneID assurance 不由页面自报 |
| 39 | `/admin/radar-links/new`（registry route） | RAD | radar assets | 预期 Radar form carrier | 1280/1440 | 404/redirect、未登录 | C0：registry；当前 `radarPage` 未接受该路径 | 修正 canonical route 或导航，不能把 `radarForm.html` 证据套过来 |
| 40 | `/admin/wecom-tags`（`tags.html`） | WB/Tags | tags admin + tokens/labs | 标签目录/本地同步意图 | 1280/1440 | loading、空、错误、403、只读 | C0：Tag UI binding；#329 状态示例已随 main 纳入 `/admin/component-states` | 真实目录、同步 receipt、客户标签命令和权限仍待验收；状态 demo 不代替页面业务 readback |
| 41 | `/admin/wechat-pay/transactions`（reserved transaction view） | WB | admin shell | 受控不可用态 | 1280/1440 | unavailable、403、只读 | C0：webshell spec；订单 Host canonical 是 `/admin/orders` | 明确 placeholder 与交易页边界；不得声称订单验收 |
| 42 | `/admin/orders`（`orders.html`） | ORD | order host + tokens/labs | 订单列表、筛选 | 1280/1440 | loading、空、错误、403、只读、unknown | C3 子旅程：#301 merge `6400a61bf4b2c95d50cb7eeb9aa2521f9d969133`，run `34930500560` 全必跑 lane PASS、deploy SKIPPED；E-ORD 局部截图 | 订单列表仍需逐页认证浏览器和订单快照 readback；不把 outcome_unknown 当失败或到账 |
| 43 | `/admin/orderDetail.html?id={order_ref}`（`orderDetail.html`） | ORD | order host | 订单详情、分销事实 | 1280/1440 | loading、404、退款中、unknown、只读 | C3 子旅程：#301 稳定 distribution Read Port 与 E-ORD 1280 success/1440 unknown 截图；#347 已随 `040eb675` 进入 main，旧验证 head `c968f733`、CI `34982622904` 和 quality snapshot 仍作历史证据 | 生产部署和认证订单 readback 仍待；文案使用“系统分账成功确认时间” |
| 44 | `/admin/wechat-pay/products`（`products.html`） | PROD | product host + standard CSS/Host | 普通商品列表 | 1280/1440 | loading、空、错误、403、只读 | C0：Product UI binding | 商品真实列表、分页、配置状态和选择器调用页 |
| 45 | `/admin/wechat-pay/products/new`（`productForm.html` new） | PROD | 同上 | 普通商品表单、分销配置 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C3 子旅程：#291 merge `38ca5ce75374ad3415e6c5615eb66b8db6812fad`，run `34931828069` 全必跑 lane PASS、deploy SKIPPED | 售卖信息只读分销开关／比例／等待天数并做保存后商品快照 readback；其它业务板块保留，不显示分销配置 |
| 46 | `/admin/wechat-pay/products/{id}/edit`（`productForm.html` edit） | PROD | 同上 | 普通商品编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C3 子旅程：#291 merge `38ca5ce75374ad3415e6c5615eb66b8db6812fad`，run `34931828069` 全必跑 lane PASS、deploy SKIPPED | 真实编辑 readback；不放分销员申请入口、链接、复制按钮或二维码，Product preopen 风险另见素材消费者记录 |
| 47 | `/admin/service-period-products`（`spProducts.html`） | PROD | product host + standard CSS/Host | 周期商品列表 | 1280/1440 | loading、空、错误、403、只读 | C0 | 周期商品真实数据、分页和入口 |
| 48 | `/admin/service-period-products/new`（`spProductForm.html` new） | PROD | 同上 | 周期商品表单 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0 | 真实保存/发布 readback |
| 49 | `/admin/service-period-products/{id}/edit`（`spProductForm.html` edit） | PROD | 同上 | 周期商品编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 真实详情与会员数据入口 |
| 50 | `/admin/spProductData.html?id={id}`（member grid；aliases `/admin/wechat-pay/spProductData.html`, `/admin/wechat-pay/products/spProductData.html`, `/admin/service-period-products/spProductData.html`） | PROD/member-grid | product member-grid assets | 会员数据表格 | 1280/1440 | loading、空、错误、403、只读、分页 | C2（隔离）：`74743a12` 1280/1440 与 `e2dded70f8f0` public Grid 复核；#359=`08a33879` 已入 main | 旧截图把 `null` 算成总数 0，且折叠分组不等于已加载行；管理员和公开页均以“当前显示 N 行”描述可见 DOM，真实分页、授权和会员 projection readback仍待生产验收 |
| 51 | `/admin/coupons`（`coupons.html`） | COUP | coupon host + tokens/labs | 优惠券规则列表 | 1280/1440 | loading、空、错误、403、只读 | C0 | 规则、领取事实和核销快照真实读回 |
| 52 | `/admin/couponForm.html`（`couponForm.html` new） | COUP | 同上 | 优惠券表单 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0 | 创建后 slug/规则 readback |
| 53 | `/admin/couponForm.html?id={id}`（edit；alias `/admin/coupons/{id}/edit`） | COUP | 同上 | 优惠券编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | alias/canonical 一致性与保存后事实 |
| 54 | `/admin/couponData.html?id={id}`（`couponData.html`） | COUP | coupon host | 领取数据/核销事实 | 1280/1440 | loading、空、错误、403、只读、分页 | C0 | 领取记录和客户归因真实 readback |
| 55 | `/admin/alipay/transactions`（reserved transaction view） | WB | admin shell | 受控不可用态 | 1280/1440 | unavailable、403、只读 | C0：route registry | 明确支付域边界；没有真实支付宝页面证据前保持未验收 |
| 56 | `/admin/image-library`（canonical；renderer page key `images`，donor template `images.html`） | MED | `RenderMedia` → `media-assets` manifest：tokens/labs/admin/materialSaveHost/imageLibraryFilterHost | 图片素材库 | 1280/1440 | loading、空、错误、403、只读、上传失败 | C0；#349 compact image table controls、#348 active draft 图片排序和 #350 编辑页共享页头均已在当前 main；素材 shared tests 不等于库页面 | 本地素材、私有 blob、缩略图失败 fallback 和发布后保存回读 |
| 57 | `/admin/miniprogram-library`（canonical；renderer page key `mpLib`，donor template `mpLib.html`） | MED | `RenderMedia` → 同一 `media-assets` manifest；API `/api/admin/miniprogram-library` | 小程序素材库 | 1280/1440 | loading、空、错误、403、只读 | C0；页面 key 与 donor 文件名是装配标识，不是额外路由 | 真实元数据、缩略图失败和素材状态 |
| 58 | `/admin/attachment-library`（canonical；renderer page key `attach`，donor template `attach.html`） | MED | `RenderMedia` → 同一 `media-assets` manifest；API `/api/admin/attachment-library`（`/upload` 是 API 兼容别名） | 附件素材库 | 1280/1440 | loading、空、错误、403、只读 | C0；页面 key 与 donor 文件名是装配标识，不是额外路由 | MIME/类型隔离、下载/预览状态和审计 |
| 59 | `/admin/automation-agents`（`agents.html`） | AUT | tokens/labs/admin | 自动化话术列表 | 1280/1440 | loading、空、错误、403、只读 | C0：`internal/automation/ui.go`；fixed_script #344 main `dca8065f`／CI `34967296577` | 与 GroupOps/运营闭环分开验收；真实 Agent/Prompt 数据、状态和最新 main 挂载仍待逐页回读 |
| 60 | `/admin/agentEdit.html`（`agentEdit.html` new） | AUT | automation bundle | 自动化话术表单 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0；fixed_script 已有 #344 授权 PUT→GET 证据 | fixed_script、Prompt 和其它生命周期分开验收；需 latest main 同步、真实 create code/取消不提交和线上 readback |
| 61 | `/admin/agentEdit.html?id={id}`（edit） | AUT | automation bundle | 自动化话术编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 真实更新、type/saved query 和审计；Prompt 与固定话术分离需独立验收 |
| 62 | `/admin/owner-migration`（`ownerMig.html` action） | WB/Owner Handoff | admin shell + owner Host | 负责人迁移预览/提交 | 1280/1440 | 草稿、校验失败、403、部分失败、读回 | C0：`mountOwnerHandoffUI` | 本地/企微受理、冻结预览和最终接替 readback |
| 63 | `/admin/ownerMig.html?contact_history=1`（legacy history） | WB | admin shell + history assets | 负责人联系历史只读 | 1280/1440 | loading、空、错误、只读 | C0 | 保持只读，不加载 mutation-capable Host |
| 64 | `/admin/config`（`config.html`/runtime center） | CFG-V3 | runtime config Host | 配置中心 | 1280/1440 | loading、空、错误、403、只读 | C0：`configPage` | 按既有分类权限验收；安全配置仅顶级超管，另核对配置版本和浏览器 readback |
| 65 | `/admin/configDetail.html?cat={category}`（`configDetail.html`） | CFG-V3 | runtime config Host | 配置分类 | 1280/1440 | loading、未知分类、校验失败、只读 | C0 | 逐分类真实配置、保存草稿/应用 revision readback |
| 66 | `/admin/config/releases`（runtime release list） | CFG-V3 | runtime config Host | 发布列表 | 1280/1440 | loading、空、错误、403、只读 | C0 | 发布状态、进程 revision 和应用事实 |
| 67 | `/admin/config/releases/new`（runtime release new） | CFG-V3 | runtime config Host | 发布草稿 | 1280/1440 | 草稿、校验失败、冲突、失败读回 | C0 | 真实发布前后 readback；不能以保存草稿代替应用 |
| 68 | `/admin/config/releases/{id}`（runtime release detail） | CFG-V3 | runtime config Host | 发布详情 | 1280/1440 | loading、404、unknown、只读 | C0 | 应用/回滚事实和 revision 绑定 |
| 69 | `/admin/config/app-settings`（reserved config alias） | WB/CFG-D | admin shell 或 config donor | 旧配置入口 | 1280/1440 | unavailable、redirect、403 | C0：registry；未见独立 configPage | 确认 canonical redirect/placeholder，禁止按配置中心通过 |
| 70 | `/admin/config/login-access`（access page） | WB/Access | admin shell + access Host | staff login permissions | 1280/1440 | loading、403、校验失败、保存失败、只读 | C0：webshell `admin_access` | 仅顶级超管、角色迁移/服务端 enforcement/浏览器读回 |
| 71 | `/admin/api-docs`（`apidocs.html`） | CFG-D/OpenPlatform | config donor + open platform assets | API 文档/调用方管理 | 1280/1440 | loading、空、错误、403、只读 | C0：OpenPlatform UI mount | 凭证不进入日志，调用方保存/撤销和权限 readback |
| 72 | `/admin/oneid`（reserved unavailable） | WB | admin shell | 明确不可用态 | 1280/1440 | 404、未登录 | C0：composition 明确 `NotFoundHandler` | 保持不可用；OneID 只能通过后台 Port/API 验收 |
| 73 | `/login`（login page） | LOGIN | login assets | 登录表单 | 1280/1440/390 | 初始、CSRF 失败、凭据失败、WeCom 入口 | C0：`RenderLogin` | 认证浏览器确认 Cookie/redirect/错误信息，不记录凭据 |
| 74 | `/auth/wecom/start`（redirect route） | LOGIN/WeCom | auth provider | OAuth 起始跳转 | 390/430 | disabled、失败、回跳 | C0：route mount | Provider 验证、state/回跳和 cookie 读回 |
| 75 | `/logout`（session route） | Access | none | 会话终止 | 390/430 | 已登录、重复、CSRF/方法错误 | C0 | 认证浏览器确认退出后受保护页面不可读 |
| 76 | `/sidebar/bind-mobile`（sidebar `index.html`） | SIDE | sidebar assets | 企微 sidebar shell | 360/420 | loading、未绑定、绑定失败、已绑定、只读 | C0/C2 子旅程：`RenderSidebar`；#330 merge `65d69b54a2336c98da9bff2ab218562683205445`，review `754cbce2`，run `34944435507` PASS，deploy SKIPPED | 未部署；真实企微宿主、context token/JSSDK/API 授权、绑定 readback 与 Provider receipt 仍待完成，不能因窄屏 presentation 证据升级整页 |
| 77 | `/distribution`（public distribution center） | DIST/PUB-DIST | distribution public CSS/JS | 分销员中心 | 375/390/430 | loading、未授权、空、错误、settlement unknown | C0：`mountDistribution`；#351=`d2eda00` 已进入当前 main，仍按公共页逐页验收 | 未部署；支付派生 session、归因／佣金真实数据和写入回执待验 |
| 78 | `/admin/distribution`（distribution admin） | DIST | distribution CSS/detail drawer/admin JS | 分销管理 | 1280/1440 | loading、空、错误、403、只读、异常 | C2 子旅程：#309 基础能力已在 main；#343=`d8f4a73`、#347=`040eb67`、#352=`b4bb355`、#353=`ada1bdc9` 已进入当前 main；#353 CI `35034837242` 全适用 lane、check、quality 通过，clean source/tree `dff54688`/`d17da85` 与 review 一致 | 当前 main 已含 settlement/paid-records/confirmation 与 compact 四指标／二维码 dialog／同排 tabs-filter；仍未部署，认证数据 readback、部署和 Provider 结果仍待，分销状态不代替结算确认 |
| 79 | `/r/{public_code}`（Radar public viewer） | PUB-RAD | Radar public handler | 内容雷达公开页 | 375/390/430 | loading、授权中、已授权、失效、404、内容失败 | C2（隔离）：clean tree `e2dded70f8f0` 已复核 Radar 390；#355 已进入 main | 早期 Radar fixture 仅 1px，现有隔离可见 fixture 与 Owner receipt 通过；公开页认证/UnionID assurance 和生产认证 readback仍待，不为本次 UI 发布触发新的 Provider 效果 |
| 80 | `/p/{code}`（public product detail） | PUB-PROD | product public handler | 商品详情 | 375/390/430 | loading、空、失效、已购买、错误 | C2 子旅程：#325 merge `be46042f363b6573df11402f70954a3ebe131770`，run `34938888021` 必跑 lane 与 quality-report PASS、deploy SKIPPED；public auth/manifest/视口证据 | 未部署；公开真实商品数据、购买状态和发布后页面 readback仍待验收，页面成功不等于订单到账 |
| 81 | `/pay/{code}`（public product checkout） | PUB-PROD | product public handler | 商品结算 | 375/390/430 | 授权中、可购买、已购买、失败、outcome_unknown | C2 子旅程：#325 publicCommerceJourney、manifest fail-closed、nested UoW 修复和有效视口证据已入 main，run `34938888021` 必跑 lane 与 quality-report PASS | 未部署；支付幂等、原订单保留、真实 provider receipt 和生产回读仍待完成，不把页面成功当到账 |
| 82 | `/s/{code}`（service-period detail） | PUB-SERVICE | service-period public handler | 周期商品详情 | 375/390/430 | loading、失效、已购买、错误 | C0 | 周期商品与会员权益 readback |
| 83 | `/s/{code}/pay`（service-period checkout） | PUB-SERVICE | service-period public handler | 周期商品结算 | 375/390/430 | 授权中、可购买、失败、unknown | C0 | 原订单恢复、幂等和支付 receipt |
| 84 | `/c/{slug}`（coupon public claim） | PUB-COUP | coupon public handler | 优惠券详情/领取 | 375/390/430 | loading、失效、已领取、不可用、错误 | C2（隔离）：clean tree `e2dded70f8f0` 已复核 Coupon 390；相关 runtime 已入 main | active-window fixture 与隔离 Chromium 证据已通过局部状态；领取事实、客户归属、重复领取反馈和失败态仍待生产 readback |
| 85 | `/q/{key}`（public survey share entry） | PUB-SUR | survey public handler | 问卷分享入口 | 375/390/430 | loading、需授权、过期、错误、已完成 | C3 子旅程：#346=`aec4c260` 与 #361=`09f6d724` 已入 main；`fc8f7239` UTF8 PG Chromium 覆盖实际 H5 auth/all/one/error/result 的 375/390/430 | 未部署；移动端 readback、问卷提交结果和业务写入回执仍待单独验收 |
| 86 | `/shared/service-period-member-grid`（shared member grid） | PUB-SERVICE/member-grid | member-grid assets/icons | 会员数据共享表格 | 375/390/430 | loading、空、错误、过期、只读、分页 | C2（隔离）：clean tree `e2dded70f8f0` 已复核 Grid 390；#359=`08a33879` 已入 main | 旧 `Number(null)`/折叠分组证据已拒绝；管理员和公开页均显示当前可见行，token scope、分页、过期链接和生产 readback仍待完成 |
| 87 | `/h5/index.html`（H5 index artifact） | PUB-SUR | build carrier | 构建入口／分流载体 | N/A | N/A | 未挂载为独立用户路由；不伪造页面验收 |
| 88 | `/h5/auth.html`（H5 auth artifact） | PUB-SUR | `h5AuthAdapter → surveyPublicHost` | 授权停止态 | 375/390/430 | 授权失败、受控回跳 | `fc8f7239` 三宽证据；#361=`09f6d724` 已入 main |
| 89 | `/h5/all.html`（H5 all artifact） | PUB-SUR | `surveyPublicHost` | 全量答题 | 375/390/430 | 已挂载答题／提交 | `fc8f7239` 候选三宽证据；业务回执另验 |
| 90 | `/h5/one.html`（H5 one artifact） | PUB-SUR | `surveyPublicHost` | 单题答题 | 375/390/430 | 已挂载答题／校验 | `fc8f7239` 候选三宽证据；业务回执另验 |
| 91 | `/h5/loading.html`（H5 loading artifact） | PUB-SUR | build carrier | 加载载体 | N/A | N/A | 未挂载为独立用户路由；不伪造页面验收 |
| 92 | `/h5/error.html`（H5 error artifact） | PUB-SUR | `surveyPublicHost` | 错误停止态 | 375/390/430 | Owner 受控错误、无重试写入 | `fc8f7239` 三宽证据；#361=`09f6d724` 已入 main |
| 93 | `/h5/result.html`（H5 result artifact） | PUB-SUR | `surveyPublicHost` | 结果页 | 375/390/430 | 已挂载只读结果 | `fc8f7239` 候选三宽证据；结果 readback 另验 |
| 94 | `/h5/done.html?slug={slug}`（H5 done artifact） | PUB-SUR | `surveyPublicHost` + Survey session | 提交完成：默认仅“收到你的问卷”；有效渠道二维码时展示二维码 | 375/390/430 | 无提交、会话失败、二维码失效、安全跳转 | 单次提交完成链路；页面只消费 Owner 的 completion action，不展示结果凭据 |
| 95 | `/h5/signup.html`（H5 signup artifact） | PUB-SUR | build carrier | 注册载体 | N/A | N/A | 未挂载为独立用户路由；不伪造页面验收 |
| 96 | `/h5/active.html`（H5 active artifact） | PUB-SUR | build carrier | 活动载体 | N/A | N/A | 未挂载为独立用户路由；不伪造页面验收 |
| 97 | `/h5/expired.html`（H5 expired artifact） | PUB-SUR | build carrier | 过期载体 | N/A | N/A | 未挂载为独立用户路由；不伪造页面验收 |
| 98 | `/h5/pay.html`（H5 pay artifact） | PUB-SUR | build carrier | 支付载体 | N/A | N/A | 未挂载为独立用户路由；不伪造页面验收 |
| 99 | `/h5/qr.html`（H5 QR artifact） | PUB-SUR | build carrier | QR／分享载体 | N/A | N/A | 未挂载为独立用户路由；不伪造页面验收 |

## 逐项最高证据级别（2026-09-15）

级别是当前已确认子范围的最高证据，不是整页完成标记。为避免把共享模块、CI 或截图自动升级到页面验收，矩阵 1–99 按以下索引判定；未列入下表的条目均为 C0。

| 条目 | 最高级别 | 证据边界 |
| --- | --- | --- |
| 2 | C3（子旅程） | #293 overview API 与 #299 首页／导航 merge、run `34935805795` 全绿；不含生产发布或线上认证 readback。 |
| 9 | C1 | GroupOps 列表只记录共享合同／Host 证据；不含真实保存读回。 |
| 10、13 | C3（子旅程） | #300 GroupOps 详情的 owner scope／`chat_reference`／保存读回，以及 #287 渠道真实 PostgreSQL／Chromium composition 与 Enter Journey；不含发布或整页所有状态。 |
| 25 | C3（子旅程） | #316 canonical customer／OneID／phone／profile／tab 证据；不含所有分区和生产认证 readback。 |
| 35、36 | C2（子范围） | #324 Radar 分页/筛选与 #296 picker 布局／单项 callback；不含真实素材 save/GET。 |
| 22–23 | C2（隔离） | clean tree `e2dded70f8f0` 的 archive entry/detail 1280/1440 已通过并由根审复核；#356 旧栈候选保留历史，#358=`03a06d0a` 已入 main，生产 readback未验。 |
| 42–43 | C3（子旅程） | #301 订单级冻结分销快照／稳定 Read Port 与 E-ORD 视口证据；#347 head `66143cd`、CI `35033284745`、merge `040eb675` 已进入 main；部署和最新订单认证 readback仍待。 |
| 45–46 | C3（子旅程） | #291 商品售卖信息的分销开关／比例／等待天数唯一配置；不含生产页面 readback。 |
| 59–61 | C0/C1（子范围） | fixed_script #344 main `dca8065f`／CI `34967296577` 已有授权 PUT→GET；Prompt 与其它 lifecycle 仍未闭合，当前只以 #344 主线事实为准。 |
| 76 | C0/C2（子范围） | #330 merge `65d69b54a2336c98da9bff2ab218562683205445`，review `754cbce2`，run `34944435507` PASS，deploy SKIPPED；仅表示企微 sidebar scoped presentation 子范围；未部署，真实企微宿主授权、绑定读回与 Provider receipt 仍待完成。 |
| 50、86 | C2（隔离） | `74743a12` 的后台 1280/1440 与 clean tree `e2dded70f8f0` 的 Grid 375/390/430 证据已通过，根审复核当前显示 1 行；#359=`08a33879` 已入 main，不能写成生产已加载。 |
| 77–78 | C0/C2（子范围） | #309 基础分销管理已在 main；公共生命周期 #351=`d2eda00`、管理确认 #352=`b4bb355`、settlement #343=`d8f4a73`、paid records #347=`040eb67`、compact #353=`ada1bdc9` 已进入 main；CI `35034837242` 全适用 lane、check、quality 通过，clean source/tree `dff54688`/`d17da85` 与 review 一致。仍待部署、公共中心和管理页生产 readback；分销状态不代替结算确认。 |
| 79 | C2（隔离） | clean tree `e2dded70f8f0` 的 Radar 375/390/430 证据已通过，根审复核 Radar 390，早期 1px fixture 已消除；#355 已入 main，可见 fixture 与 Owner receipt 已验，公开授权和生产认证 readback仍待。 |
| 84 | C2（隔离） | clean tree `e2dded70f8f0` 的 Coupon 375/390/430 证据已通过；active-window fixture 仍只是局部合同，领取事实、失效、重复和失败态服务端证据仍待。 |
| 80–81 | C0/C2（子范围） | #325 auth/manifest/nested-UoW 与有效视口证据已入 main；未部署、未做真实支付/provider 回执。 |
| 85 | C3（子范围） | #346=`aec4c260` 与 #361=`09f6d724` 已入 main；`fc8f7239` 在 UTF8 PG Chromium 下覆盖 H5 auth/all/one/error/result 三宽。部署、真实移动端 readback，以及问卷提交结果和业务写入回执仍待单独验收。 |
| 1、3–8、11–12、14–21、24、26–34、37–41、44、47–49、51–58、62–75、80–83 | C0 | 仅路由、Host、assets 或未完成页面边界核对；逐页真实数据、失败态、权限与 readback 尚待验收。 |

矩阵当前覆盖的是 clean main 能确认的 route/view 与既有 artifact；`C0`、共享组件测试、注入式 Host 或单一页面截图都不会自动升级为 `C3`。四类曾识别的真实缺口已有 remaining-pages clean tree `e2dded70f8f0` 隔离 Chromium 证据，#355/#358/#359/#360/#361 已入 main；生产认证 readback、部署和 Provider receipt 仍待。`/admin/coupons`、couponForm、couponData、`/admin/service-period-products`、`/admin/external-effects`、`/admin/channels/new`、`/admin/api-docs`／runtime releases、`/admin/owner-migration` 已有后台 1280/1440 证据。H5 auth/all/one/error/result 有三宽证据；其余八个 H5 build carrier 与无独立公共 UI 的渠道按代码可达性记 N/A。当前未发现应新增页面的遗漏，也不扩大业务验收范围。GroupOps 标准群运营、运营闭环、自动化话术、AI 助手四类页面也必须保持独立记录。

## 基础多维表与指标看板（2026-09-17，待完整 CI）

`web/v3/shared/ui/dataWorkspace.ts` 由 `/admin/spProductData.html` 的 `productWorkspace`、`/admin/hxc-dashboard` 的 `funnelGrid.ts` 和 `/shared/data-dashboard` 的 `dashboardShare` 调用。Tabulator 仅处理展示、列及折叠，Go Owner 在分页前计算范围、指标和组人数；共享组件不访问领域存储。后台复用既有 transport、单页头，匿名入口只加载独立 manifest 闭包。`dashboardShareDialog.ts` 复用分享交互，Product/HXC 各自授权、持久化、审计。公开字段只读白名单与不可扩大的基础范围在服务端执行。真实 Chromium 覆盖范围/撤销及移动端，证据状态见 PRD 验证记录；不代表生产部署。

页面层级修订：同一 DataWorkspace 提供顶部总览／明细导航、按需挂载 Tabulator、分别配置指标与列的右侧面板、可撤回范围条件、未保存切换提示。样式集中在 `dataWorkspaceStyle.ts`，三个调用方共享；HXC Host 明确允许 `tab=overview|details`。当前改造验证独立记录，不沿用旧布局 CI。

### 素材分组管理

`web/v3/materialGroupManagement.ts`：三个素材工作台共用分组目录、分组表单、单个/当前页批量移动与所属分组选择；组合 `MaterialGroupSidebar`、`pageHeaderActions` 和 `selectionDialog`。Media API 独占领域写入。不得把元数据分组操作当作 Provider 素材刷新。

图片素材页通过 `GroupManagement` 可选 toolbar 与 selectionSlot 复用同一批量选择状态：勾选在缩略图前、所属分组纯文本、搜索栏内全选/转移；附件与小程序默认布局不变。

### 管理端搜索单选
`internal/webshell/static/admin_console/admin_search_select.js`：核心产品销售商品关联使用原生搜索输入、单选与分页加载。调用方注入受权目录 loader，组件不写领域数据。保留选择、清空、失败重试、键盘/IME 和选中项回显；Segment `/core/product-options` 通过 ProductOptionReader 读取普通/周期商品。

### 裂变活动完整设置页（2026-09-19 修复）
`/admin/referral/settings` 经 `RenderReferral` 继续使用 admin_base；`referralAdmin.ts` 复用 `AICRMSearchSelect` 搜索普通/周期商品，目录由 Referral 管理权限下的 ProductOptionReader 提供。共享选择器增加可选 emptyLabel/initialLabel/initialQuery/onChange，既有核心产品调用默认行为保持。真实 PostgreSQL 配置读回与个人邀请测试、JSDOM 设置页及同壳装配测试独立于生产验收；视觉 QA 的未完成项见 design-qa.md。
