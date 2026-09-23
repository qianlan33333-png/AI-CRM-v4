# 商品与 Radar 素材选择：真实多选调用方

## 目标

将商品编辑器的页面素材选择和内容 Radar 的图片／PDF 素材选择，从冻结选择器的 `selectedIds + onConfirm` 单项桥接迁移到已发布 V3 Material Adapter 的 `selectedRecords + onCommit` 完整临时选择合同。

确认只更新调用方已有的浏览器草稿；原商品“保存当前维度”和 Radar“保存内容”仍是唯一持久化入口，并在各自的真实页面重新读取结果。取消、关闭、目录失败和调用方拒绝都不能改变原有草稿。

## 开发前分类

| 判断 | 结论 |
| --- | --- |
| OneID／外部身份 | 不涉及。素材选择使用 Media Owner 的本地 `library_id` 和原有受控资源 URL，不解析、关联或创建客户／企微身份。 |
| 持久化 | 选择器本身无持久化。商品仅更新原 `pfImageUrls/spfImageUrls` 本地草稿，随后由 Product Owner 原保存命令写入 `images`；Radar 仅更新其编辑表单 `form.media`，随后由 Radar Owner 原保存命令写入 `media_item_id`／快照字段。 |
| External Effects | 不涉及。目录是调用方已授权的 Media HTTP 读取；本 PR 不上传、不发送、不排队、不调用 Provider，也不改变商品／Radar 保存后的既有外部效果边界。 |

## 已核实的实际调用路径

| 页面 | 真实入口与原草稿 | 支持的素材及限额 | V3 确认后的调用方应用 |
| --- | --- | --- | --- |
| 普通商品 `productForm` | 冻结 `AdminController` 持有 `pfImageUrls` 本地页面草稿；原保存将 `images` 一次送入 Product Owner | 图片，最多 10 项（Owner 保存上限仍独立校验） | V3 在该控制器的公开原型接缝一次更新同一 `pfImageUrls` 草稿，再由原保存持久化；成功后重新打开从该原草稿回显。 |
| 周期商品 `spProductForm` | 同一冻结控制器持有 `spfImageUrls`；原保存将 `images` 一次送入 Service Product Owner | 图片，最多 10 项 | 与普通商品共用同一 V3 caller adapter 和一次性草稿替换，但保留自己的控制器字段和保存路径。 |
| 内容 Radar `radarForm` | `web/src/admin/sections/radar.ts` 的单个 `form.media`；原保存写入 `media_item_id`、名称快照与目标 URL | 图片或 PDF 附件，单选 1 项 | `onCommit({ selected })` 接受恰好一条，临时把这条已验证的 V3 记录放进本次原 picker callback 的目录输入，让原回调写入 `form.media`；页面保存后重读该 Radar 内容。 |

商品允许多选；Radar 的领域表单只有单个 `media_item_id`，所以它也采用完整 `onCommit` 调用形状和取消／回显语义，但不会凭 UI 扩张为多素材领域模型。

## 共享与调用方合同

- 复用 `web/v3/shared/ui/materialPickerAdapter.ts`、`SelectionSession` 与 `selectionDialog`；不新建平行选择器，不改冻结 donor。
- 每个调用方传入可信的 `selectedRecords` 和 `selectedIds`。商品只接受当前同源 `/api/admin/image-library/{id}/variants/original` URL；任何外域、上传或不可解析的既有 URL 不会被伪造成 `library_id`，而是保留在原产品草稿的相对位置，直到用户用原产品行的移除操作处理它。
- V3 dialog 返回完整 `{ selected, added, removed }`。商品先验证**全部**选中记录都带有上述可信原图 URL，再原子地写回同一控制器草稿，因此后续分页的合法素材也能应用，而不受冻结 picker 首批目录限制。Radar 只把一条已验证记录注入本次原 callback 的输入；任一校验或 callback 失败都会抛出明确错误，V3 dialog 保持打开和草稿不变。商品的初始素材直读与 Radar 的 `loadDb` 编辑态读取均受 2.5 秒上限保护：每次商品按钮点击对同一页面／kind／原草稿单飞，路由、kind 或原草稿变化会废弃旧结果；Radar 对表单 key、内容类型和实际按钮代次做同样校验。异步旧读取超时、失败或迟到后不得打开或应用任何旧选择器。
- 商品以整组 URL 一次替换页面草稿，原保存仍以该组值一次写入。Radar 的原 callback 只应用一项。调用方接受成功后 V3 session 才 commit/close；回调抛错、目录失权、取消或刷新失败均不自动重放和不假称回滚。
- 重新打开应读取调用方当前草稿而非上次 dialog 的缓存；显式移除是临时草稿变化，取消仍恢复此前页面草稿。商品确认后可以移除、重开再加；Radar 继续沿用其页面原有“移除”操作。
- 所有目录读取继续由页面显式授权：Radar 使用其 `image-library`／`attachment-library` scoped loader；商品保留当前 Media adapter 读取与原 callback 范围。冻结 Radar callback 的旧泛目录读若返回 404，只在当前表单仍有效、且 V3 已精确授权该单条素材时注入该条记录；401/403/5xx 从不回退。没有全局目录兜底。

## 前端一致性和 Product Design 路由

本次 Skills catalog 没有可调用的 Product Design 路由，因此该步骤未完成；未安装、替代或伪称已使用该插件。前端一致性组件索引要求扩展已验收的 Material Adapter，而不是复制冻结 `material_picker`。本 PR 只在实际 Product/Radar Host 接缝替换可见选择流程，沿用已审核的管理端灰白蓝视觉基线、单壳、共享 dialog CSS、IME／focus／keyboard 合同和调用方既有表单布局。

构建链仍为 `scripts/build-v3-host-adapters.mjs` → stable Host／领域 Host → manifest → `scripts/stage-new-shell-ui.mjs`；release staged artifact 必须继续装载 Material Adapter 及其 chunk，冻结 donor 不得被写入。

## GitHub 与仓内参考

- [PR #296](https://github.com/qianlan33333-png/AI-CRM-v3/pull/296) 首次引入 Radar scoped Material session，并在正文明确 Radar 仍是 `selectedIds=[] + onConfirm` 单项桥接；本 PR 正式收敛这个保留缺口。
- [PR #317](https://github.com/qianlan33333-png/AI-CRM-v3/pull/317) 已合并至 `main` 的 #317（`84c34e5ad784d3f4cf20082b6a83d39919bff7e5`）已验证 `onCommit` 成功后才 commit/close 的合同，且 stable Host 的 manifest/chunk 相对路径可在 staged release 中读取。
- 冻结 V2 行为参考仅来自 `web/donor-sources/.../controller.ts`：`pickCommerceImages` 对整个 URL 列表做单次草稿替换，`saveCommerceProduct` 统一持久化；`web/src/admin/sections/radar.ts` 明确 Radar 只持有单个 `form.media`。两者皆不修改。

## 验收

1. 商品普通／周期页：选两项、移除、取消、重新打开回显，保存后请求体和编辑页重读都保留相同原 URL 顺序；当前作用域目录未确认、失权或缺失的素材保持为不可选回显，确认时 dialog 留在当前草稿、页面不改；来自后续已授权分页的素材可以正常确认。
2. Radar 图片／PDF：确认只更新单个表单草稿，取消不改，未确认/缺行/缺确认按钮失败保留；真实 PostgreSQL Chromium Journey 从已保存图片的编辑页直接切 PDF，先确认同号图片已清除，再显式选择同号 PDF，最后通过原 Owner 保存和 GET 的 `attachment_id`（映射为表单 `target_type=pdf`／`media_item_id`）回读；旧 ID 不能被自动重解释为 PDF。
3. 目录分页、搜索、IME composition Enter／Escape、焦点回归、当前 401/403、旧请求迟到、缩略图失败继续复用共享 session 已有回归；商品额外覆盖双击单飞、预读后路由切换和原移除草稿变化，Radar 覆盖编辑态 `loadDb` 超时与迟到、表单路由切换，以及只对 scoped 404 的单条授权 fallback（401/403/5xx 不回退）。新调用方覆盖 `onCommit` 的整组原子回调边界。
4. 运行 TypeScript、Product/Radar DOM adapter tests、真实 PostgreSQL + Chromium Product/Radar journeys、串行 build/stage release closure。若 Darwin 环境跳过某真实 Chromium journey，报告为跳过并由 Linux CI gate 负责，不把 composition 结果描述为浏览器通过。

不包含素材上传／持久化模型改造、Provider 效果、素材库呈现重构、状态示例页和其他页面的素材接入。
