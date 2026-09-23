# 商品页面素材：当前维度上传、选择与排序

## 目标与已核验根因

普通商品 `productForm` 与周期商品 `spProductForm` 的“页面素材”必须在当前编辑维度内完成上传、素材库选择和排序。成功上传或确认选择后，用户仍停留在当前 URL 和页面素材维度，未保存的其它字段不丢失；素材顺序只在点击该维度现有“保存当前维度”后由 Product Owner 持久化并重读。

冻结 donor `web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/admin/controller.ts:1558-1600` 已确认：

- `setCommerceImageUrls` 调用整页 `setState`，而冻结的页面维度初始化会回到售卖信息；
- `uploadCommerceImage` 只取第一个文件，调用 `saveImageItem` 后提示“已上传到素材库”并执行 `init()`，没有将新素材写进当前商品草稿；
- 既有素材行没有排序交互。

donor 只作冻结行为证据，不修改。V3 在 `web/v3/productAdapter.ts` 的已存在 Product Host/Adapter 接缝修复，且以已重放到 `0ffa5096d8eb5edc99945ac1b3eca978cf2f2fd1` 的 `#327` 候选 `6c06e338` 为依赖基线。该候选已经负责受限素材选择器 `onCommit` 与调用方草稿，Radar 相关变更不是本能力的新增内容。

## 开发前分类

```text
OneID: not involved；页面素材只读/写 Product Owner 与 Media Owner 的受控 product、typed Media 记录，不解析、创建或关联客户身份。
Persistence: local transaction；每个上传仍由 Media Owner 创建一条图片记录；本次仅把其返回的 typed Media 记录追加到当前 Product 浏览器草稿。商品持久化继续由既有 Product Owner 的当前维度保存命令、CAS、幂等键与读回完成。
External Effects: not involved；不触发 Provider、企微写、支付或后台任务。上传请求仍走现有 Media Owner HTTP 边界。
```

## 参考

- 用户指定旧仓 [AI-CRM@dd8d60d](https://github.com/qianlan33333-png/AI-CRM/blob/dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/aicrm_next/extensions/commerce/commerce/templates/wechat_products.html)：`readProductBody` 将当前素材顺序序列化为 `image_library_id` 与 `sort_order=index+1`；`renderSlices` 使用原生 `dragstart/dragover/drop` 对草稿数组 `splice` 后仅重绘素材区；上传返回 item 后追加草稿，不切面板。旧实现会在多文件中途失败时延后可视化已成功项，本次不沿用该缺陷。
- 本仓 Media Owner `POST /api/admin/image-library/upload` 返回受控 `item`（id、original_url、thumb_320_url、enabled 等）；只接受这个返回的 typed Media record，不能按文件名二次检索、不能猜测 URL 或将未授权 URL 当素材。
- 前端一致性：复用 `web/v3/shared/ui/materialPickerAdapter.ts` 和 #327 Product `onCommit` 调用方草稿，不复制 picker、产品保存或上传 Owner。Product Design route 在当前 Skills catalog 不可调用，记录为未完成，未假称使用。

## 行为合同

1. 仅 `productForm` / `spProductForm` 的页面素材维度接管上传与素材选择。捕获 `{page, productId, kind, current URL, active dimension, draft generation}`；页面、URL、产品或维度变化后，旧上传/选择响应不得改新编辑器。
2. 上传逐个使用 Media Owner 的现有 multipart endpoint 和 CSRF/同源授权。每个成功响应经过 typed Media record 校验后立刻追加到当前草稿并局部回显；后续文件失败时保留已成功项、失败文件的原草稿和当前维度，不能重传已经成功的文件。每次上传完成都清空 file input。总数仍最多 10。
3. 素材库 `onCommit` 仅写当前 Product 草稿并局部回显，保持活动维度、URL、其他未保存字段和临时选择。取消/目录失败/权限失效不改草稿。不得调用 `init()`、浏览器导航或全局商品重置。
4. 素材列表以 stable typed-Media ID / 受控 original URL 为键，支持拖放重新排序；同时提供可聚焦的“上移/下移”按钮，首尾禁用。拖放或键盘排序仅更新当前草稿和素材区，不自动保存。保留原移除动作。
5. 点击现有“保存当前维度”后，Product Owner 仍以其现有输入、CAS、幂等键写入；保存成功重读当前 product 并显示相同顺序。保存失败保留浏览器草稿并停在当前维度。

## 验收

- 普通和周期商品：在页面素材维度上传/从素材库确认后，URL、活动维度和其它未保存字段不变，成功项立即出现；后续上传失败不清掉或重复成功项。
- 两个商品表单均可拖放和键盘上/下移动；保存请求与读回的 `images` 顺序一致。
- 并发/迟到上传、选库回调、页面/URL/商品切换、401/403/5xx、数量上限、取消、保存失败各自不污染新上下文或伪称成功。
- 运行受影响 Node/TypeScript、真实 PostgreSQL Chromium Product Journey（1280/1440；若新增商品表单窄屏行为则独立记录）并保留持久日志与截图。无生产写、Provider 调用或发布。

## 非目标

不改变 Media 或 Product 表结构、素材库权限、OneID、分销、支付、公开商品页、Radar、上传 Provider、产品保存协议或冻结 donor。
