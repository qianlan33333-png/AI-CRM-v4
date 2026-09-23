# V3 标准选择组件覆盖账本
当前 main：`09f6d7243228f6415cf550dc8711e28b4003889f`；本账本只记录共享组件和调用方边界，不把 main 合入、Mock、Node、截图或静态 donor 当作生产页面完成。

分类结论：OneID 不参与选择控件；读取目录属于受权限约束的 Provider/本地目录读取。商品配置在商品 Owner 的单一事务保存；客户打标签仍经既有 Customer TagCommand/External Effects 边界，组件不新增 Provider 写入、身份匹配或队列。

2026-09-16 补记：CouponData 继续复用唯一 `couponAdapter`，只在其展示副本把既有 `issue` 显示为“已领取 / 发行量”，以 `targetRefs` 的已知项数显示“指定商品（N项）”；不跨 Product Owner 读取名称。public Survey 继续复用 #346 的 `h5AuthAdapter → surveyPublicHost` 链路，#361 只替换 Owner 已给出的 OAuth/error 停止态展示；不新增 OAuth、OneID、持久化或 Provider 调用。两者已入 main，仍等待 production 门禁。

| 入口 | 实体与标准原件 | 外层 Host / 权限与保存映射 | 验证状态 |
| --- | --- | --- | --- |
| 客户目录与档案（`customers.html`、`customerDetail.html`） | 客服 `OperationMemberPicker`；标签 `AICRMWeComTagPicker` | `customerAdapter` 在发布页面隐藏旧 ID 输入。负责人仅以 `scope=owner_migration` 返回的 `staff_id` 保存；预检 403 时隐藏控件，不扩大仅超级管理员的目录权限。标签为可信正整数 `tag_id`。 | 原件 DOM 选择、查询参数和 `staff_id`/`user_id` 分离已测。 |
| 客户静态目录批量标签 | `AICRMWeComTagPicker` | `admin_customers.js` 保持 Customer TagCommand 预览/确认流程；标准 picker 只填原有选择框。 | 浏览器旅程已测。 |
| 普通商品、周期商品 | 页面图片 `AICRMMaterialPicker`；标签 `AICRMWeComTagPicker` | `productAdapter` 将普通/周期表单各自的“从素材库选择”接入 V3 material loader；#341 `a4a89ce5`、#348 `fbf79b6`、#350 `4ed751f1` 与 #345 `1e91d8f` 均已进入当前 main `23a31742`；仍回填冻结草稿/保存通道；标签保存为 `enabled:boolean, tag_ids:number[]`。购买后动作由 Host 控制 `purchase_action_enabled` 与 `qr|redirect`，未启用或非当前模式字段会在写入前清空。 | 普通商品覆盖标签启用/关闭、跨页素材、保存 payload、重复保存恢复；周期商品冻结表单覆盖跨页素材、取消不改草稿和保存 payload。真实页面发布后 readback 仍待验。 |
| 周期商品会员表 | `OperationMemberPicker` | `member_grid_host` 仅把原件读取映射到产品已有 Access-scoped staff 目录，保留 `disabledUserIds`、上限与无全局刷新。 | Go + jsdom 原件旅程已测。 |
| 内容雷达 | `AICRMMaterialPicker` | `radarAdapter` 仅适配真实图片/附件目录的分页、搜索与原始素材 ID；冻结 `radar.ts` 未修改。 | 原件素材 DOM relay（真实目录项回填冻结保存状态）已测。 |
| 图片素材库、素材选择与内容详情 | `materialThumbnailPresentation` | 图片库 Host、共享素材 picker 与 `contentPresentation` 仅复用调用方已经授权的展示 URL；统一 loading／loaded／error／no_url，helper 不发起 API、上传或 Provider 调用。无 URL 的内容详情继续全宽；真实 URL 错误继续保留视觉列。 | helper 与三个实际调用点的状态、Enter/IME、分页及无缩略图布局回归。 |
| 问卷编辑（选项、评分、评估标签） | `AICRMWeComTagPicker` | 所有可达标签按钮均由 `mountTagPicker` 汇入唯一 `openTagModal`，调用原件 `AICRMWeComTagPicker.open`；标准 Host 在原标签全局加载后锁定该全局，冻结编辑器无法覆盖它。编辑器没有素材、群聊或客服选择动作。 | 路径与加载顺序已复核；逐入口选择和保存旅程仍由问卷验收覆盖。 |
| 群运营、AI 助手 | 客服、群聊、素材、话术 composer 原件 | 现有 GroupOps / AI Host 的限定目录和保存契约不变。main #344（`dca8065f`，CI `34967296577`）已提供 fixed_script composer 的授权 PUT→GET 保存回读；Prompt 及其它生命周期边界仍由各自 Owner 维护，不能用 fixed_script 证据扩展覆盖。 | fixed_script 具备主线保存回读证据；Prompt、其它生命周期、Provider 效果和真实页面仍逐项验收。 |
| 渠道 | 客服、标签、素材、composer 原件及渠道原表单 | `channelCenterAdapter` 由渠道 Owner 维护；共享发布清单提供原件和被动资产。 | 渠道 Host 旅程已测。 |
| 优惠券 | 优惠券原表单及抽取的同字节 runtime | CSP 使用 `/assets/standard-components/coupon_form_runtime.js`，由页面 Host 在挂载原 DOM 后加载。Coupon active-window fixture 仅证明局部窗口条件，不能升级公共领取页状态。 | Coupon CSP/失败重试、失效、重复领取和真实 readback 旅程由 Coupon Owner 维护；#360=`7552ebd3` 的 CouponData 语义修复已入 main。#355 是独立的 Radar CSP 合入，不归属优惠券。生产 readback 仍待。 |
| 组件状态示例（`/admin/component-states`） | Group、Material、Tag、Staff、Content Composer | `componentStatesHost` 只注入明确本地 fixture；确认只更新同页摘要。Tag/Staff 保持各自 source 与 ID 合同，Composer 仅用 `media-library + kind + id` 渲染本地预览与只读。 | 认证路由、零 API 请求、状态/IME/焦点和 360–1440 组件旅程覆盖。 |
| Audience 管理（归档人群包、删除空群组、归档策略、解绑自动化） | `window.AICRMConfirmation.confirm` / `openConfirmationDialog` | `admin_audience_detail.js` 在请求前冻结既有目标与可见名称；组件只持有临时原因、校验、焦点、Escape、取消/确认结果与 busy 状态。既有 Audience Owner 命令仍由 caller 以原路径、正文、鉴权和幂等键发出。 | 浏览器覆盖取消零写入、确认单次归档与 Owner 读回；失败反馈后可按既有命令合同重试。 |

没有选择动作的标签管理列表、其它只读展示和历史数据不替换控件。旧文档把 `agentEdit.html` 的所有固定素材都写成“API `maxItems=0`、全部只读”，该结论已过时：main #344 的 fixed_script 已可经 composer 授权 PUT→GET 保存并回读；Prompt 与其它生命周期仍保持各自边界，不能把 fixed_script 证据扩展成全编辑器或 Provider 完成。旧 `web/src/shared/ui/picker.ts` 中仍有冻结 controller 的历史方法；每次发布须按实际服务模板和路由确认可达性，不能凭全局组件加载把它们计为覆盖。

## 2026-09-16 根审补记

本账本的标准组件合同没有新增业务范围。99 路由矩阵曾确认四类真实证据缺口：archive list/detail、Radar public、coupon public claim、member-grid；旧 `eaa84fc5` 截图因 archive 重复标题、`Number(null)` 造成 grid 总数 0、Radar 1px fixture 被拒。随后 remaining-pages clean tree `e2dded70f8f0` 在 UTF8 PostgreSQL 上完成 Chromium 19.335s，13 张截图覆盖四类页面并由根审复核关键截图，重复标题、1px fixture、共 0 行问题已消除，Grid 当前显示 1 行。该证据是隔离浏览器/合同通过；#355/#358/#359/#360/#361 均已入 main，生产 readback 仍待，不能把它写成生产页面完成。`/admin/coupons`、couponForm、couponData、`/admin/service-period-products`、`/admin/external-effects`、`/admin/channels/new`、`/admin/api-docs`／runtime releases、`/admin/owner-migration` 已有桌面页面级证据；问卷仅 auth/all/one/error/result 是实际移动路由，其余八个 H5 carrier 与无独立公共移动 UI 的渠道为 N/A。shared member-grid 的移动隔离证据不替代后台周期商品列表。

当前本地中文 PostgreSQL Journey 统一使用 `postgres://qianlan@127.0.0.1:55441/aicrm_ui_utf8_20260916?sslmode=disable`；SQL_ASCII 数据库不作为中文测试依据。生产、Provider 写入和新交易不属于本账本验收范围。b869 首次包预检因 AppleDouble 失败；`COPYFILE_DISABLE=1` attempt2 严格校验通过，458 members = 441 regular + 17 directories，440 payload hashes 全匹配，archive SHA256 为 `1504a8ea17a4b150c914bc43c9e86e3264d36df50beb05da45354c0beb10eb46`。它仍是 detached b869 预检、未部署，最终包必须从 clean merged main 重建。
