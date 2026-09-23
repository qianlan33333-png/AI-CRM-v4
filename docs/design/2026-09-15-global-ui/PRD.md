# 新CRM 全局 UI、标准组件与分销概览优化

日期：2026-09-15
状态：已批准，持续实施；治理账本继续由 PR #297 交付
当前实施基线：origin/main=`09f6d7243228f6415cf550dc8711e28b4003889f`（2026-09-16 账本更新时点）
主线动态：#345、#348、#349、#350、#352、#351、#343、#347 与 #353 已进入此前主线；本次账本时点 #340=`a01d6f2e`、#346=`aec4c260`、#354=`71acf1e8`、#357=`d9eb7147`、#355=`23a31742`、#358=`03a06d0a`、#359=`08a33879`、#360=`7552ebd3`、#361=`09f6d724` 也已合入。固定审计基线仍保留 a4，合入不等于部署或页面验收。tests/governance 仍按候选登记；#356 旧栈候选仅作历史记录。
实施方式：按可观察能力拆分独立工作树与 PR。本文是产品与验收基线，不把未合入分支、CI、截图、fixture 或构建包当作已发布能力。

## 2026-09-16 浏览器证据补记

当前 main 已有 #340、#346、#354、#357、#355、#358、#359、#360 与 #361（`09f6d7243228f6415cf550dc8711e28b4003889f`）；本 tests/governance 候选仍须按最终合入顺序重新绑定。本段取代本文较早的“仍待合入”快照，不把候选或本地验收写成 production。

- 四类既有缺口的隔离验收基线是 `e2dded70f8f08c20f2d9080b3bbcd0f716cd65fb`：UTF8 PostgreSQL、required Chromium 19.335s、13 张图，覆盖消息存档、Radar、公开优惠券和共享会员表格。
- 桌面补验收提交 `74743a12a2e21686e67881061c88eccecc7feb17` 在 UTF8 PostgreSQL、required Chromium 下通过 admin 1280/1440 路由证据；CouponData 语义修复后，`2800105c549f68143f36d248bc77e829b35a7042` 的 83.147s 复验确认“已领取 / 发行量”=`1 / 100`、累计领取说明“发行 100”、范围“指定商品（1项）”，且编辑表单实际字段已读回；#360 已入 main。
- 公开问卷真实可达状态限于 auth/all/one/error/result：`fc8f72393c5633ff1327b705c8cd073ec8c61cb5` 的 UTF8 PostgreSQL、required Chromium 44.853s，15 张 375/390/430 图在 `/Users/qianlan/aicrm-artifacts/remaining-pages-chromium-20260916/public-survey-final-fc8f7239f54d`；auth/error 为停止态且没有冻结演示壳，#361 已入 main。`/h5/index/loading/done/signup/active/expired/pay/qr` 是未挂载 build carrier，不伪造业务流；渠道没有独立公共移动 UI。
- 以上均为独立 PostgreSQL fixture、Provider disabled 的页面级证据；不替代部署后认证读回、支付、退款、结算或 Provider receipt。

本次使用干净测试／治理工作树；原目录 `/Users/qianlan/Downloads/新CRM` 的现有修改只读保留，冻结 donor 不修改。本候选更新 Chromium/PG 验收测试、测试截图配置 getter、治理文档、组件索引、逐页账本和剩余清单，不改业务 runtime、不部署。

## 开发前判断

```text
OneID: reads canonical customer；客户相关页面只读取 customers.id 及既有 Identity/Customer Read Port。本次不新增客户主键、身份匹配、隐式建客、自动合并或外部身份写入。
Persistence: governance/overview presentation is stateless/read-only；分销概览新增的待处理异常订单数是 Distribution-owned 的只读 COUNT(DISTINCT commission.order_id) 投影，经既有稳定 Overview Read Port 提供；不新增汇总表、内部任务或缓存。业务保存仍由所属领域在既有 PostgreSQL Unit of Work 内完成。
External Effects: not involved in this governance/UI pass；申请二维码、分享链接和页面按钮只复用调用方已有 URL 与页面命令，不新增 Provider write。支付、退款、分账、企微发送、群发和调度继续由所属模块及 outbound/External Effects 合同负责，UI 不把 accepted/queued 当作 Provider 成功。
```

业务判断：全局治理的目标是让管理员在统一后台壳中找到真实入口、看懂数据状态并完成已有业务操作；视觉收敛不能改变权限、身份归属、订单快照、佣金生命周期、支付确认时间或外部效果语义。旧链接可作为兼容载体，但不重复计数为新页面；静态 carrier、reserved placeholder、登录／退出和构建 artifact 单独登记。

## GitHub 参考与 Product Design 状态

GitHub 参考采用 [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md)、[Ant Design Pro](https://github.com/ant-design/ant-design-pro) 和 [Vant](https://github.com/youzan/vant) 的 token、信息层级、状态语义及移动端适配思路；不引入依赖、不复制代码，也不替代 v3 共享组件和领域合同。

本轮会话 Skills catalog 未提供 `product-design:index` 或 focused Product Design skill，因此该路由未完成，未伪造调用、未重新创建模板，也未把个人模板或截图当作验收。此前已核实的个人模板「新CRM · 小鹅通式统一工作台」保留作视觉参考；Product Design 路由恢复后补 focused audit 或 image-to-code 记录。后台、企微 sidebar 与公共手机页继续使用各自壳和认证边界。

## 目标与标准组件

统一后台一级／二级页面、导航、弹窗、抽屉、企微侧边栏、商品与支付页、问卷页和分销页。管理端只使用 v3 `admin_base` 单壳；企微 sidebar、H5 和公共页使用各自入口，不因视觉相似互挂壳或数据契约。

标准组件优先复用并扩展现有 V3-owned 入口：`SelectionSession`／选择弹窗、标签选择器、客服／员工选择器、群聊选择器、素材选择器、话术与素材编辑器、只读内容呈现、详情抽屉、页头操作、提交式搜索、表格／分页／状态反馈。组件只负责呈现、选择、校验和交互状态；目录读取、权限、业务保存、上传、发送、调度、支付和 Provider 调用由调用页及所属领域负责。

所有选择器必须保留调用方的数据源、权限范围、实体 ID、单／多选、数量限制和只读状态。弹窗用临时选择，确认后才回传，取消不改原值，重复打开正确回显；乱序响应不得覆盖新查询，翻页不得丢选择，刷新或失败不得静默清空，失效或失权项要明确提示。远程空词 Enter 重新请求调用方授权的无关键词分页目录，渠道本地筛选才可使用已加载全量结果；IME composition 期间不搜索，候选确认 Enter 不触发查询。

## 经营首页与分销管理要求

`/admin` 继续通过 `GET /api/admin/overview` 读取统计范围、指标、趋势、更新时间和各分区状态。overview 只读各领域稳定 Read Port，不跨领域查表、不触发 Provider、不新增持久化汇总。统计使用北京时间；金额按最小货币单位并保留币种；客户按 canonical customer 去重；未知、无权限、不可用和真实零值保持可区分。

分销管理的本轮用户调整如下：

1. 顶栏右侧只保留“申请二维码”。“打开申请页”和“复制申请链接”与二维码属于同一申请入口，移除这两个并列动作。二维码展示复用商品管理二级页的分享 dialog／页头操作入口，URL 仍由分销领域持有，不能复制领域命令、生成第二标题栏或扩大权限。
2. 分销概览的指标压成一行，只展示“成交额、待结算佣金、已结算佣金、待处理异常订单”，四个数字适当放大。移除旧的总说明“期内指标按支付确认时间计算；未结算、系统分账成功确认和待处理异常为当前状态。”
3. “分销员、订单、异常”三个 tab 与“当前页筛选”同一行，移除“仅筛选当前已加载页，不扫描后续页，也不代表总数。”这句说明；筛选行为和授权范围仍由当前列表保留。
4. 删除指标卡的观察时间、逐卡口径说明和附带脚注；这些内容全部不在页面展示。指标字段缺失或为负数时显示“待确认”，真实零值仍展示，不能把缺失或异常值静默成 0。

分销口径固定为：成交额与期间初始佣金使用同一支付确认时间；退款和调整不混入初始佣金；待结算、已结算和待处理异常是当前状态，不随期间筛选；已结算只表示系统成功确认，不代称银行到账。待处理异常订单必须是 Distribution-owned COUNT(DISTINCT commission.order_id) 的附加只读字段，通过现有 ReadPort 返回；既有异常记录数仍保留给其原消费者，不能直接改 label 或把记录数当订单数。指标字段缺失或为负数时显示“待确认”，真实零值保留。以上是内部数据合同，不能渲染为指标卡脚注、观察时间或逐卡说明。

交易订单只经 distribution 稳定 Read Port 展示成交时冻结的分销比例、归因分销员、佣金金额和币种、退款复核等待、预计结算、实际结算／分账时间、状态和异常证据；预计时间不能冒充已分账，商品当前比例不能替代订单快照。

## 视觉、路由与验收基线

组件索引固定追踪：`canonical route → handler／领域 UI adapter → Render* 或 mount → manifest assets → page caller`。`component-map.md` 的连续条目混合 canonical route/view、旧 alias/carrier、reserved placeholder、登录／退出和 H5/build artifact，条目数量不等于页面数量。

视口矩阵按终端执行：后台桌面 `1280/1440`；企微 sidebar `360/420`；公共页与 H5 `375/390/430`。C0 只证明源码路由、Host 和 assets 静态闭包；C1 证明共享组件合同；C2 证明实际挂载壳、视口和交互；C3 证明认证业务数据、关键交互及保存后的服务端 readback。发布、线上页面回读和真实业务／Provider 回执是另外的门禁。

验收必须逐项覆盖 loading、空、无匹配、失败、403、只读、真实零值、未知、重复点击、取消、刷新、分页、IME、权限变化和窄屏。Mock、Node、jsdom、fixture、静态 carrier、截图或 CI 不能代替真实 PostgreSQL、认证浏览器、发布后 readback 或 Provider receipt。

## 99 路由矩阵收口判断

本轮逐项复核 1–99 台账后，确认过四类真实的页面／状态证据缺口：消息存档列表与详情（22–23）、Radar 公共查看器（79）、优惠券公共领取（84）以及会员数据表格（50、86）。早期 `eaa84fc5` 截图被根审拒绝，原因分别是 archive 重复标题、member-grid 把 `null` 算成 0 行、Radar fixture 只有 1px。随后 remaining-pages clean tree `e2dded70f8f0` 在 UTF8 PostgreSQL 上完成 Chromium 19.335s，日志为 `/Users/qianlan/aicrm-artifacts/remaining-pages-chromium-20260916/remaining-pages-e2dded70f8f0.log`，13 张截图覆盖 archive entry/detail 1280/1440、Radar/Coupon/Grid 375/390/430；根审已复核 archive entry 1280、detail 1440、Radar 390、Grid 390，重复标题、1px fixture 和共 0 行问题已消除，Grid 当前显示 1 行。该结果是隔离浏览器／合同证据，不是生产 readback；相关 #355/#358/#359/#360/#361 均已合入。#356 的旧栈候选保留为历史，不再作为当前交付。

除上述四类外，本次矩阵审计没有发现需要新增页面或扩大业务验收范围的遗漏。`/admin/coupons`、couponForm、couponData、`/admin/service-period-products`、`/admin/external-effects`、`/admin/channels/new`、`/admin/api-docs`／runtime releases、`/admin/owner-migration` 与 member-grid 已有实际 PostgreSQL Chromium 1280/1440 证据。问卷的实际公共移动路由是 auth/all/one/error/result，已有 375/390/430 证据；`index/loading/done/signup/active/expired/pay/qr` 是未挂载 build carrier，渠道没有独立公共移动 UI，均为 N/A 而不伪造流程。shared member-grid 的 e2dded 375/390/430 隔离证据不代替后台周期商品列表或完整 projection readback。runtime 已入当前 main，仍须补发布后认证页面和服务端 readback；不把测试稳定性工作、共享组件存在或截图本身升级为页面完成。

## 当前主线与交付顺序

当前 main `09f6d7243228f6415cf550dc8711e28b4003889f` 已包含 #340、#346、#354、#357、#355、#358、#359、#360、#361 以及此前共享组件、经营汇总、订单分销事实、客户档案、公共商品／支付、搜索／确认框、企微 sidebar、分销管理基础、群运营反馈、fixed_script 等范围。#340 CI `35036235000`、#346 CI `35037494318`、#354 CI `35039557156` 与 #355 CI `35042545968` 均已记录为全适用通过。四漏项的 e2dded 隔离 Chromium 证据已具备，但不代表当前 main 或生产完成。

交付顺序：先盘点 route／Host／assets 与共享组件；再按可观察能力完成后台壳、首页、分销、运营、档案、素材、自动化及公共页；每个 PR 只交付一个可观察能力。root 负责分发审核，Terra high/xhigh 负责标准开发，Luna max 负责基础执行和审计。

## 完成边界

本候选文档不声称全局 UI 完成。当前没有本任务 production deployment、部署后版本核对或认证页面 readback；生产公开 `readyz` 仍报告 `f594e33b010db8bef62a9a890c3d80350f2008a0`，浏览器登录状态已过期，不能伪造生产回读。支付、退款、分销归因、系统分账、发送和 Provider 业务验收继续单独记录，本次不触发新交易或外部写入。b869 首次包预检因 458 个 AppleDouble `._*` 隐藏成员被拒；随后 `COPYFILE_DISABLE=1` attempt2 的 tar 严格校验通过（458 members = 441 regular + 17 directories，440 payload hashes 全匹配，archive SHA256 `1504a8ea17a4b150c914bc43c9e86e3264d36df50beb05da45354c0beb10eb46`），但它仍来自 detached b869、只是预检且未部署，最终发布必须从 clean merged main 重建。所有未达到相应门禁的页面继续在逐页账本中保持待验收；冻结 donor 仅作行为和视觉证据，不修改、不计为能力来源。
