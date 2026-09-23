# 图片素材库与共享缩略图状态呈现

## 业务判断

图片素材库的列表、素材选择和内容详情此前各自维护缩略图加载或失败表现，造成无 URL、加载中和加载失败的反馈不一致。本能力将这些视觉状态收敛为一个 V3 共享呈现 helper，统一图片库卡片、素材选择器和内容预览／只读详情的表现。

OneID：不涉及。素材不会读取、解析或归属客户身份。

持久化：不涉及。helper 不写入素材、选择结果或内容包。

外部效果：不涉及。helper 不上传、发送、调用 Provider 或新增任务；浏览器对调用方已经授权的既有 `<img>` 显示 URL 的加载仍由调用方原有边界决定。

## 参考与复用

- GitHub #253：`/admin/image-library` 已通过 Media Owner UI、`RenderMedia`、`MaterialSaveHost` 装配，上传、编辑、删除及其读回写合同保持不动。
- GitHub #317：共享素材选择器已经提供 caller-scoped 读取、临时选择、确认／取消和 Enter/IME 搜索合同。
- GitHub #329：内容预览在无受控缩略图时使用全宽布局；真实 URL 加载失败时仍保留视觉列并给出明确反馈。

前端组件索引命中：素材库 `RenderMedia`，共享素材选择 `web/v3/shared/ui/materialPickerAdapter.ts`，内容预览 `web/v3/shared/ui/contentPresentation.ts`，搜索复用 `committedTextSearch.ts`。Product Design catalog 在本次会话不可发现，因此该路由未完成；实现沿用已批准的小鹅通式灰白蓝 token，不进行新的视觉发散。

## 方案与边界

新增 `materialThumbnailPresentation.ts`，只将调用方给出的授权 URL 呈现为 `loading`、`loaded`、`error` 或 `no_url`。helper 不自行 fetch、拼接、转换或放宽 URL 信任，也不撤销 private blob URL；调用方继续拥有授权 URL、private loader、abort 与清理。

接入范围：

1. 图片素材库卡片：统一加载、错误和无预览占位；保持既有上传、编辑、删除、MaterialSaveHost 回读、权限和分页行为。
2. 共享素材选择器：统一列表卡片缩略图反馈；保持 typed `kind + id + source`、临时确认／取消、`onCommit`、调用方 scope 与错误语义。
3. 内容预览与只读详情：复用同一状态 renderer；无 URL 继续不渲染视觉列，保持 #329 的全宽详情；有 URL 失败后继续保留视觉列并显示 fallback。

图片库搜索继续只在 Enter 后调用读取，IME 组合态和普通输入不触发搜索；刷新与分页只使用已经提交的查询，读取失败保留已有内容。

不包括小程序／附件重设计、新 API、上传／编辑／删除行为、素材领域数据结构、身份、持久化或 Provider 调用。

## 验收

- helper 覆盖 `loading`、`loaded`、`error`、`no_url`，重复渲染时旧图片事件不会覆盖新状态，并证明没有自行发起 fetch。
- 素材选择器保留选中值、取消、`onCommit`、权限失败与 IME Enter 行为。
- 内容详情验证无 URL 不预留缩略图列；加载失败保留真实缩略图视觉列。
- 图片库 Host 验证 Enter/IME、分页、失败保留和四种缩略图状态；真实 Host 出错时只显示 fallback、恢复后只显示图片。
- Chromium 对图片库、素材选择器及内容呈现验证 loading／loaded／error 的计算样式互斥；内容预览和只读详情的有图／错误视觉列固定为 48px，使用 `object-fit: cover`。
- 在本地 PG + Chromium 的 `/admin/image-library` 1280／1440 视口回读；仅使用独立测试数据库和本地 fake Provider，不执行生产上传或删除。
