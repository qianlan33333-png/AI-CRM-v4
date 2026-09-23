# 后台列表可读性修复：周期商品操作与问卷空态

## 背景与已核验事实

在 `79d8f15cb2629ecf6f5597da48ab2dca5c5293e8` 的隔离 PostgreSQL 与 Chromium 组合页面中复测：

- `/admin/service-period-products` 的周期商品操作列仍会分为两行。`#334` 已将可见文案改为“删除”，当前截图中“删除”位于第二行；没有裁切。创建周期商品仍位于列表内容卡而非该页唯一顶部标题栏，状态仍显示裸 `enabled`，更新时间仍显示原始 ISO 值。它们是普通后台一致性问题，不是资金、订单或归档规则故障。
- `/admin/questionnaires` 的成功 `0` 条结果只留下表头，表体没有说明空态。

证据为 `TestPostgreSQLAdminShellLayoutChromiumJourney` 的 PASS 及同次截图，保存在 `aicrm-artifacts/remaining-admin-visual-audit-20260915/main79d8/`。旧 `58abb` / main933 截图不作为本次结论。

## 前置分类

```text
OneID: not involved；两处均只呈现已通过后台授权读取的列表，不解析、关联或创建客户身份。
Persistence: stateless；仅调整浏览器呈现，不改变产品或问卷的命令、版本、审计或数据表。
External Effects: not involved；不触发 Provider、支付、归档写入或后台任务。
```

周期商品“删除”继续调用既有 Product owner 的 archive/delete 命令；不改变 #334 的确认文案、版本比较、幂等键或回读规则。

## 参考与视觉基线

- GitHub 参考：[Ant Design Table](https://github.com/ant-design/ant-design/blob/master/components/table/index.en-US.md) 对 `emptyText`、加载和表格列展示的契约；[Ant Design DESIGN](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) 对表格操作和菜单的语义原则。
- 仅借鉴“明确空结果与读取失败”“在空间不足时收纳次要动作”的原则。本仓不引入 React、Ant Design 或第三方 UI 依赖。
- 前端链路：`/admin/service-period-products` → Product UI adapter → `RenderProducts(page=spProducts)` → V3 shell `admin-topbar` + 已冻结 `spProducts` 片段 + V3 `productAdapter`；`/admin/questionnaires` → Survey UI adapter → `RenderSurvey(page=questionnaires)` → 已冻结问卷片段 + V3 `surveyAdapter`。
- Product Design：本会话 Skills catalog 中没有可调用的 `product-design:*` 资源，不能伪称已使用；本 PRD 以已验证主线截图、共享组件索引和现有视觉 token 为基线。

## 方案与范围

### 周期商品操作列

冻结 donor 的 300px 列和 `flex-wrap` 不修改。优先核实现有 V3 共享表格操作入口；若主线没有可复用 action menu，则新增一个 V3-owned、无业务写入的键盘可用 action-menu helper，并登记组件索引。

- “编辑”“数据”保持直接可见；“分享”“复制”“停用/启用”“删除”保持原节点、原授权显示和原回调，只在动作数量或可用宽度不足时进入“更多操作”。
- 菜单必须可以键盘打开、关闭和恢复焦点；原动作节点不重建，避免丢失 frozen controller 已绑定的处理器。
- 1280 与 1440 下不裁切、不强制所有按钮同一行；内容超过可用宽度时保持可访问菜单或表格横向滚动。
- `#334` 删除命令和其确认/幂等/readback 合同不改。
- 仅 `products` 与 `spProducts` 由 `RenderProducts` 启用既有 shell `.admin-topbar`，分别以“商品管理”“周期商品管理”作为唯一标题；`productForm`、`spProductForm` 与 `spProductData` 继续保持原嵌入式布局和顶部保存操作。
- `mountPageHeaderActionElements` 将各列表已有的“创建”按钮节点（不克隆）移入 `.admin-topbar > .admin-topbar-meta`；原监听、href、禁用状态和权限可见性保持。Adapter 使用该共享组件公开的 `pageHeaderActionElementsHaveConnectedOrigins` 判断 donor 重绘期间是否仍有真实源位置，避免同文档重绘误清空或复活脱离 document 的旧按钮；无授权创建节点时才清除旧页头动作。随后只删除同一列表 donor 的直接标题子树，避免双标题或空白占位。
- frozen runtime 在 `web/src/shared/ui/runtime.ts:134-137` 将 `onClick` 直接绑定到各按钮；行操作菜单仅搬移该原节点。每次 frozen controller 重绘后，Adapter 清理脱离 document 的旧浮层与监听，再挂载新行；不得依赖 stage/document 事件委托。
- 普通商品与周期商品均通过既有 `formatShanghaiDateTime` 以北京时间显示已读取的更新时间，避免原始 ISO 折行；周期商品仍只将已读取的生命周期显示值 `enabled` / `disabled` / `draft` 转为现有中文状态文案。未知值和非法时间不编造新值。
- 共享溢出菜单根据触发器上下可用空间择位，并限制可用高度后滚动；以计算后的可见状态管理打开、关闭和焦点。Tab 离开菜单关闭浮层，Escape 回收至触发器。

### 问卷列表空态

`channel-list-read-state-implement` 已在另一候选中定义 V3 shared `tableReadState`（`empty`、`no-match`、`error`、`colSpan`、保留已授权行和 retry），但尚未进入主线。

- 不复制或并入该候选；待共享 helper 合入后，在 Survey adapter 以显式已完成的列表响应状态调用它。
- 只有已成功读取且 `items=[]` 时显示“暂无问卷”；查询条件已生效但无匹配时显示“未找到匹配问卷”。
- 加载、网络/5xx、401 和 403 沿既有 adapter/transport 的语义呈现；不以空 `tbody` 或 MutationObserver 推断成功，也不把授权失败伪装成空数据。
- 该接入独立提交，避免将共享 helper 的实现夹带进本修复。

## 验收

1. 在已登录、真实 Postgres + Chromium 页面复测 1280 / 1440：仅普通与周期商品列表各有一个 shell 标题和位于顶栏右侧的创建操作；三个表单/数据入口没有新 shell 标题。周期商品的所有可见和已授权动作可达、无裁切；长名称、减少权限动作和窄可用区域仍可操作。
2. 通过真实“更多操作”菜单点击删除：Chromium 夹具使用独立的 `admin-layout-delete-menu` 商品，保证后续商品表单仍验证原商品。取消为零写；确认仅发送一次原 Product owner DELETE，保持原目标、版本、幂等键并经既有读回移除行。重绘后旧浮层不可见、旧动作不可再次触发。
3. 问卷在成功 0、搜索无匹配、加载、5xx、401、403 时各自呈现正确表格状态；合法 0 不再空白，失败/无权不泄漏或伪装旧数据。
4. 覆盖受影响 Node/typecheck、实际页面 Chromium 截图、构建/舞台/P5（以最终 main 和 head 绑定）。

## 非目标

- 不修改冻结 donor、问卷/产品业务规则、身份模型、Provider、支付、归档保存、后台导航或其它列表空态。

## 当前本地验证状态

共享菜单的 Node 键盘、计算可见性和源节点回调合同，以及商品归档 DOM 合同已通过。真实 PostgreSQL/Chromium 的长商品列表下缘菜单旅程也已通过：1280／1440 均验证菜单向上展开、取消零写、确认仅一次原 Product owner DELETE，并通过既有 readback 移除行；保留真实截图和原始日志于 `aicrm-artifacts/admin-list-visual-fixes-20260915/25082480-menu-focus-keyboard-forward-tab-final/`。测试点击在滚动后等待两帧并重取命中点，且共享菜单在内部焦点切换时不会提前关闭浮层；实际普通 Tab 离开浮层与 Escape 均关闭菜单，Escape 回收至触发按钮。
