# Content Radar 精确 ID 读取 PRD

日期：2026-09-15

状态：已获实现授权；实现、合并、部署和线上回读分别记录。

源码基线：`b4e7874186b633b54729638d2de85ef0934e8715`。

## 业务判断与边界

运营人员从指定 Radar 链接进入详情或编辑页时，页面必须展示该 ID 的权威记录，或呈现可见的失败状态。用列表第一条替代不存在的 ID 会展示错对象；编辑页把显式不存在的 ID 变成新建会把一次编辑误变为创建。第二页的合法记录尤其不能再依赖默认列表页是否包含它。

OneID：不涉及。本次不解析、关联或创建客户身份；仅读取 Radar 自有链接。

Persistence：无新增持久化、任务、Provider 读取或 Provider 写入；复用既有同源 `GET /api/admin/radar-links/{id}`。

External Effects：不涉及；该 GET 的既有响应要求 `local_projection=true`、`real_external_call_executed=false`。

范围只修 V3 owned `radarAdapter` 的 `AdminApi.loadDb` 读取接缝和它的真实挂载测试。冻结 `web/src`、冻结 donor、Radar HTTP handler、OpenAPI、统计投影、CAS 与保存路径保持不变。列表筛选和可见分页属于后续独立 PR。

## 根因和现有链路

- `internal/radar/module/ui.go:50-71` 拒绝大多数非法或 non-canonical URL；不过它只检查 query map 的键数，重复 `id` 仍可能通过，且 Go `int64` 上限超过 JS 安全整数。这不能替代静态 Host/fixture 的 raw-URL 保护。
- 冻结 `web/src/admin/sections/radar.ts:30-38` 对详情与编辑调用 `api.loadDb({page,id})`。冻结 `web/src/api/admin.ts:2049` 对三个 Radar 页面仍读取列表。
- 冻结详情渲染的 `links.find(...id) || links[0]` 会在目标不在默认列表页时显示第一条；编辑渲染找不到 ID 时把 `editing` 设为 `null`，保存输入因而没有 `id`。
- 既有 OpenAPI `getRadarLink(linkId)` 映射到 `GET /api/admin/radar-links/{id}`；响应中 `link.link_id` 是可校验的目标 ID。它是本次唯一新增使用的既有读取 operation。
- `radar_oneid_bridge.js` 是既有统计和 auth-policy 补充层，继续同挂载；本次不修改它。它只可在单条 GET 成功并挂载后继续对相同 ID 工作。

[WHATWG URL 标准仓库](https://github.com/whatwg/url) 和 [其重复查询参数讨论](https://github.com/whatwg/url/issues/851) 确认 query 是成对序列，不能以 `get()` 的首个值掩盖重复值。本 PR 因此检查完整参数对而非只读取第一个 `id`。

## 方案

1. V3 Host 在冻结入口动态导入前保存原 `api.loadDb`。只对 `radarDetail` 与带 query 的 `radarForm` 处理 raw URL；其它页面继续原读取合同。
2. 新建只接受 `radarForm.html` 的空 query。任何额外参数、缺失/空/重复 `id`、非 canonical decimal、零、或超过 `Number.MAX_SAFE_INTEGER` 都在请求前拒绝并通过现有入口错误区域显示；不请求列表、不创建新建态、不读取 events/share/stats。
3. 合法精确 ID 以现有生成客户端请求单条 GET，统一解包 401/403/404/网络失败。只接受同 ID 的 `link.link_id` 和本地读取边界；将该记录映射成仅含这一条的 `AdminDb`，再交给未改动的冻结 renderer。
4. 单条读取失败、响应不是同一 ID 或响应边界无效时，拒绝 `loadDb`。冻结入口显示可见错误，不挂载详情/编辑，因而不会出现其他 ID 标题、后续 event/share/stats 读取或保存入口。

## 前端复用与 Product Design

终端：管理端既有 `admin_base` 单壳。

入口：`internal/radar/module/ui.go` → `radarHost` manifest asset → `web/v3/radarAdapter.ts` → 冻结 `mountRadar`。

复用：冻结 Radar renderer、既有加载错误区域、`AdminApi.loadDb` seam 与 `radar_oneid_bridge`；不新增页面、壳、样式、选择器或交互模式。

Product Design：按 `product-design:index` 路由并读取 `audit`；当前是已有页面的源码级错误修复，浏览器验收由专职 UI 审计执行。本 PR 不声称新的截图或线上体验复现。

## 验收

新增的 canonical Host/JSDOM 用例必须共挂载真实 `HttpApi`、冻结 Radar renderer 与现有 bridge，使用合成数据：

1. `id=21` 成功只能请求并渲染 21，不发列表 GET，也不显示 ID 1。
2. 21 的 404、401、403、网络错误和响应 ID 不匹配均显示错误；没有列表替代、另一个 ID 的 events/share/stats、保存或创建请求。
3. 空 query 的 `radarForm` 仍是既有新建路径；`?id=`、重复 id、非法/零/不安全整数 id，及含其它 query 的表单不会变成新建。
4. 合法详情保留 bridge 对同一 ID 的统计读取和不可用显示；编辑保留同 ID 的 auth-policy 读取。
5. 新测试注册到 `scripts/run-donor-view-consumers.sh` 的 Radar 段，并运行其定向命令、V3 typecheck 和完整 canonical consumer。上述是源码/夹具证据，不等于线上回读。

## 非目标与风险

- 不改 service route 对 URL 的拒绝规则，也不把静态保护描述成线上该路径已复现。
- 不添加列表分页、统计缓存、身份映射、数据库迁移、Provider 行为或重试机制。
- 编辑保存继续复用既有 `saveRadarLinkDto`：提交前读取当前版本，再以既有 `PATCH /api/admin/radar-links/{id}` 写入；本 PR 不改变其并发或 CAS 语义。
- 旧 deep link 不存在时从错误展示取代错误的第一条展示；这是预期兼容变化。
