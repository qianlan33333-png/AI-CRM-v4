# 小程序素材：禁用未接通的企微缩略图控件

**状态：** 已实施，待 root diff 审核
**开发基线：** `f13f65b79659309555a0653321f7c52a02893270`（已包含小程序表单校验 #290 的 fresh `origin/main` worktree）
**用户入口：** `/admin/miniprogram-library` → “新建小程序卡片”或“编辑小程序卡片”弹窗

## 业务判断与源码证据

冻结小程序素材模板目前展示两个会造成错误预期的控件：create 弹窗的“上传缩略图（将缓存到企微）”，以及 edit 弹窗的“刷新缩略图缓存”。冻结 controller 实际把两者接到 `blocked()`，分别提示没有独立上传 operation 和没有缓存刷新 operation；它们没有真实上传或刷新链路。

仓库确有 `POST /api/admin/miniprogram-library/{id}/test-resolve`，但它不能成为这些按钮的接线目标。HTTP 结果明确包含 `changed=false`、`local_only=true`、`provider_call_executed=false` 和 `real_external_call_executed=false`。OpenAPI 同样将其描述为无 Provider 调用的本地卡片投影刷新；Media resolver 只可查询本地缓存，禁止调用 Provider 或跟随 URL。因此把它称为“刷新企微缩略图缓存”会把本地缓存事实误报成企微动作。

当前 OpenAPI 没有独立缩略图上传 operation，且 create/edit 请求不应从页面伪造 `thumb_media_id`。这两个控件继续可点会产生“后端能力未就绪”的短暂阻断提示，而非准确、持久的产品状态。

`web/src/admin/templates/mpLib.html` 和 controller 均是冻结 donor source view，不能修改。本 PR 复用 V3-owned `web/v3/materialSaveAdapter.ts` 的实际挂载接缝：在 donor modal 渲染后只改变这两个控件的浏览器可用性与说明，不改变 frozen 模板、controller 或其余素材保存生命周期。Product Design `audit` 路由和前端组件索引均要求沿用当前 modal/feedback 呈现，不重设计。GitHub 代码搜索未发现仓库内可复用的企微缩略图上传/刷新 UI 接线；以 Media/OpenAPI 当前合同为准。

## 架构分类

| 判断 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不涉及。小程序素材的缩略图 UI 不解析或关联客户、OneID 或外部身份。 |
| 持久化 | 不新增或改变持久化。禁用动作不会调用 `test-resolve`、create、update 或任何写入。 |
| 内部持久任务 / Provider | 不新增任务、队列、Provider read/write。现有 `test-resolve` 的本地缓存 read 与 receipt 合同保持不调用。 |
| 权限 / 外部效果 | 不改变现有 Admin 授权；更正 UI，避免把 Provider 外部效果显示为已接通。 |

## 最小范围

1. 在 V3 `materialSaveAdapter` 观察并识别冻结 `mpLib` create/edit modal 中的两个缩略图按钮。
2. 将 create 控件改为禁用态“上传缩略图（暂不支持）”，并在 modal 内保留说明：“当前不支持企微缩略图上传；不会上传至企微。”
3. 将 edit 控件改为禁用态“刷新缩略图缓存（暂不支持）”，并在 modal 内保留说明：“当前不支持企微缩略图刷新；不会发起企微调用。”
4. 为每个禁用按钮设置 `disabled`、`aria-disabled`、`aria-describedby` 和合适的禁用视觉状态。Host capture 阻断合成点击，使冻结 `blocked()` 不再产生错误的能力未就绪 toast。
5. 不修改 create/edit 的有效表单保存、已有重复点击锁定、Idempotency-Key、失败/结果未知反馈或列表回读。

## 明确排除

- 不修改 `web/donor-sources`、`web/src/admin/templates/mpLib.html`、冻结 controller、OpenAPI、Media domain/app/http/store 或数据库。
- 不将 `test-resolve` 接为企微刷新，不添加本地缓存检查、Provider 刷新、缩略图上传、媒体写入、Outbox、队列或 worker。
- 不在页面上声明企微缓存已刷新、缩略图已上传、外部效果已执行或 Provider 已回执。
- 不隐藏按钮来假装能力存在；禁用态与原因必须同时可见、可读取。

## 验收

扩展现有 `web/v3/materialSaveAdapter.test.mjs`，让真实冻结 `mpLib.html` 与 V3 Host 同时挂载：

| 场景 | 预期 |
| --- | --- |
| 新建弹窗 | 原“上传缩略图（将缓存到企微）”不会作为可用动作出现；替代禁用按钮带上传暂不支持文字、辅助说明和 aria 关联。点击/合成点击不触发 upload、create、update、`test-resolve` 或能力未就绪 toast。 |
| 编辑弹窗 | 原“刷新缩略图缓存”不会作为可用动作出现；替代禁用按钮带刷新暂不支持文字、辅助说明和 aria 关联。点击/合成点击不触发 `test-resolve`、Provider 调用或能力未就绪 toast。 |
| 正常保存 | 既有 create/edit 保存及其 Host 写入、幂等与回读覆盖继续通过。 |

定向验证：`node web/v3/materialSaveAdapter.test.mjs`、TypeScript/build/Host mount 合同和相关 Media local-cache 合同测试。JSDOM 证明 UI 阻断与请求形状，不替代线上浏览器验证或 Provider 回执。
