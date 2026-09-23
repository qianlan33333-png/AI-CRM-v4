# 2026-09-10 媒资与 Excel 前端验证证据

## 边界判断

- OneID：不涉及。素材刷新和 Excel 封面不识别、创建或归属客户。
- 持久化与外部效果：前端只读取 Media preparation 状态，并提交后端已定义的 CSRF、幂等异步命令；刷新任务、凭据写入和企微效果由后端 Owner 负责。

## 已验证契约

- `GET /api/admin/media-preparations` 使用顶层 `today_refresh_round`、`next_refresh_at` 和 `items` 精确字段。
- 全量刷新使用 `POST /api/admin/media-preparations/refresh-rounds`，单素材刷新使用 `POST /api/admin/media-preparations/{source_ref}/prepare`，均发送 `force: true`、CSRF 和 `Idempotency-Key`。
- Excel 封面继续使用 `POST /api/admin/operation-batches/{id}/cover?expected_version=...`；上传仍为 raw PNG/JPEG，已有启用图片使用 JSON `{"cover_image_id": <id>}`。

## 测试证据

- `node web/v3/materialSaveAdapter.test.mjs`：PASS。覆盖真实嵌入 Media 页面、读取 loading/error/retry、缺失计数不显示默认 0、明确 `succeeded: 98` 与 `unknown: 2` 原样显示、刷新失败但旧凭据仍可用、已过期和缺失凭据、全量 POST 双击只提交一次、响应丢失后复用同一个 Idempotency-Key。
- `node scripts/excel-batches-dom-test.mjs`：PASS。覆盖已有启用图片分页、下一页、取消不改批次、JSON 封面选择、冻结封面与历史展示，以及提交后隐藏封面编辑入口。
- `npx esbuild web/v3/materialSaveAdapter.ts --bundle --format=esm --outfile=/tmp/media-ui-material.js`：PASS。
- `npx esbuild web/v3/excelBatches.ts --bundle --format=esm --outfile=/tmp/media-ui-excel.js`：PASS。
- `node scripts/prepare-donor-source-views.mjs` 后执行 `npm run build --silent`：PASS（56 pages + 9 hashed entries）。
- `git diff --check`：PASS；全仓 `dev_preflight.py fast` 按协调约定暂缓，等待 core 完成。

此前素材测试失败的原因是 harness 用 Node realm 的 `TypeError` 模拟浏览器网络异常，页面 realm 的 `error instanceof Error` 判断不成立；测试现已改用 JSDOM 页面 realm 异常，未删减幂等、双击或 POST 次数断言。
