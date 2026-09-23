# Content Radar 列表服务端筛选、可见分页和刷新 PRD

日期：2026-09-15
状态：已获实施授权；功能与 OpenAPI authority 元数据仍分别审阅、提交和验证。
整合功能基线：`84c34e5ad784d3f4cf20082b6a83d39919bff7e5`（#317 已合入的共享选择与内容组件）；最终提交前仍按实际 main 重新绑定 source authority。

## 业务判断与证据

内容雷达列表当前只读取默认第一页。`web/src/admin/sections/radar.ts` 随后用该内存数组执行名称、链接、文件名、类型和“启用/停用”筛选，刷新按钮只调用本地 `paint()`。因此第 21 条及以后的记录不可见，后页匹配项会被误显示为无结果，刷新也不是服务端回读。这是源码结论，不是线上网络时序复现，也不把先前约 8.1 秒的观察归因于本 PR。

现有领域已经具备一条有界读取路径：`internal/radar/port/projections.go` 的 `ListQuery` 有 `Search`、`ContentType`、`Status`、`Limit`、`Offset`，`internal/radar/store/postgres.go` 以 `updated_at DESC, id DESC` 排序，先 count、再按 offset/limit 读取一页，并以单个页级统计聚合补足摘要。`internal/radar/http/handler.go` 当前仅把 `status/limit/offset` 接入该 Port；`api/openapi.yaml` 也尚未声明 `search/content_type`，并错误复用默认 50 的通用 Limit 文档。

`internal/webshell/static/admin_console/radar_oneid_bridge.js` 已从同一列表响应读取 #244 页级摘要，但目前任何迟到列表响应都会清空并重写摘要 map。即使页面丢弃旧响应，旧统计仍可能 hydration 到新行，必须与 Host 的当前读取代际一起失效。

参考：本仓的 `web/v3/imageLibraryFilterHost.ts` 已采用 AbortController 加 generation、精确失败页 retry 和 offset 边界；外部仅借鉴 [Ant Design Pagination 的受控 disabled/total 输入形式](https://github.com/ant-design/ant-design/blob/master/components/pagination/Pagination.tsx)，不引入其依赖或组件。

## 分类与边界

| 项目 | 结论 |
| --- | --- |
| OneID/外部身份 | 不涉及。列表继续读取 Radar 本地投影，不解析、匹配、创建或关联客户身份。 |
| 持久化 | 列表读取不新增写入；状态启停仍复用既有 Radar lifecycle 本地写入及其既有事务、CAS 与幂等合同。Host 只在该既有写入返回后协调权威列表 GET 回读，不新增表、事务、任务或收据。 |
| 持久任务/Provider | 不涉及。不新增队列、Worker、Provider 读写、幂等收据或外部效果。 |
| 终端与复用 | 管理端单壳 `admin_base`。冻结 `web/src` 与 donor 不改；用 `web/v3/radarAdapter.ts` 的 `api.loadDb`/Host 接缝、现有 `.card/.toolbar/.tbl/.btn` 样式和现有分享弹窗。#317 的共享 `installMaterialPickerAdapter`、分页素材读取和冻结回调 relay 原样保留；列表控制器只在同一 adapter 装配其读取/ownership 接缝，不新建页面壳或第二次挂载。Product Design index 路由判定本项为既有界面集成，不触发视觉审计或重设计。 |

## 目标行为

1. `/api/admin/radar-links` 增加公开、可选的 `search` 和 `content_type` query；保留 `status/limit/offset`、固定排序和既有响应字段。雷达列表的 `limit` 明确默认 20、范围 1–100；不修改全局通用 Limit 的默认 50。
2. `search` 经过现有 trim 和 UTF-8 **字节**上限 200 校验；不是“200 个中文字符”。Store 在既有已转义 LIKE 谓词中同时匹配 `name`、`title` 和 `destination_url`，不访问 Media 表，也不再承诺文件名搜索。
3. 管理端显示明确的“全部／草稿／启用／停用”状态筛选。现有冻结“停用”本质是 `!enabled`，会合并草稿与停用；本 Host 以服务端权威 `status` 区分三种状态。既有启停动作与详情、编辑、分享路径保留，不扩写其业务合同。
4. 当前页固定最多 20 条，显示服务器 `total` 与范围；上一页/下一页只按 `offset`、`limit` 与 `has_more` 启用。筛选提交重置到 offset 0；刷新使用已提交 query 和同一 offset；不会全量预取或根据返回行数猜测下一页。
5. 每个请求捕获 query/offset 快照与单调 generation；新请求 abort 旧请求，但仅 generation 决定是否发布页面、total、按钮、busy、错误、retry 和桥摘要。失败 retry 精确重放失败快照；401/403 清除旧授权页、总数和摘要，显示可见权限错误。普通网络/5xx 失败保留最近一次成功页，但不以该页冒充新筛选结果。
6. Host 在每个列表 GET 前公布 generation；#244 bridge 仅当捕获的 generation 仍为当前值时才更新 summary map。每页仍是一条列表 GET 加既有页级摘要，不发逐行详情/统计 GET；`statistics_status=unavailable` 继续显示“不可用”，绝不填零。
7. 列表行的详情和编辑只传 ID，仍由精确 ID PR 的单条 GET 守住“同 ID、无 fallback、失败不新建”合同。
8. `RadarLinkListItem` 是 `additionalProperties: false` 的闭合 DTO。分页 Host 只读取其已声明的 lifecycle 和统计字段；不把未声明的 `file_name_snapshot`、PDF 处理状态或页数塞进响应。PDF 行仍由冻结 renderer 的既有默认展示渲染，不借此声称列表 API 返回 PDF 处理进度。

## 后端、契约与兼容

- Handler 仅把 `search/content_type` 映射到既有 `radarport.ListQuery`，让 App 保持统一的 status/type/byte-length/offset 校验与 400 语义。
- Store 复用同一个 `where` 作为 count 与 page 读取条件；由于 PostgreSQL read-committed 下 count 与行读取本就可能跨快照，`total/has_more` 是该响应的服务端元数据，页面不得再推断或补造全局值。
- OpenAPI、handler 与 HTTP contract tests 同步。列表 Host 通过既有 `authenticatedRequest` 读取这条路径；冻结 P4 generated client 不改。根审功能 diff 后，再按仓库 source-governance 流程独立登记 `source-index`、`source-lock` 和 P5 authority approval，实际 base/SHA 以当时提交为准。
- 整合 #317 时，保持其唯一的启动 IIFE：先等待标准组件、安装共享素材选择器，再唯一一次 import 冻结 `admin/main`。列表 `installRadarListRead`、ownership/share-generation guard 与 controller 在同一 adapter 的模块初始化阶段装配；它们不重新 import 或 mount 冻结页面。
- 不改变 detail、分享、访客明细、统计聚合、身份、素材上传、排序，或既有 lifecycle 写入的协议、事务、CAS/幂等合同。Host 只把页面成功状态延后到权威列表回读完成；旧客户端不带新参数仍得到默认 `all`、`limit=20`、`offset=0`。

## 验收与证据

1. 合成 21 条：初始 GET 带 `limit=20&offset=0`，只渲染 20 条；下一页只读一次 `offset=20` 并只显示第 21 条；返回、边界按钮、total/range 均由 metadata 控制，零逐行 GET。
2. `search`、`content_type`、`status` 通过真实 Host→现有认证 HTTP 接缝发送；筛选 reset offset；空态与 total 是服务端结果。后端隔离 PostgreSQL 16 测试覆盖 name/title/destination URL 和 `%`、`_`、反斜杠的既有 escape 合同，及 21 条 limit/offset。测试只使用显式 localhost 新库/随机 schema，记录实际 PASS 或 skip，绝不继承环境 DSN。
3. 同筛选刷新重放已提交 snapshot；失败后的 retry 不读当前未提交输入；乱序成功、拒绝和 abort 后晚响应均不能覆盖新页或释放新 busy；401/403 清空先前结果。
4. 迟到列表响应不能污染 #244 bridge 统计；当前页 `unavailable` 显示“不可用”。详情/编辑仍经过 exact-ID 接缝。
5. JSDOM fixture 通过仓库已有 Swagger Parser 解析 canonical OpenAPI，并使用其现有 AJV dependency 验证正向 `RadarLinkPage`；缺少必填 `authorized_views` 或携带未声明 PDF 字段的负例必须被闭合 schema 拒绝。重放 #317 后还回归共享 `radarAdapter.test` 素材选择、精确 ID、列表 Host、typecheck、build、source gates 与完整 consumer。新 Radar 列表 suite 保持注册在 `scripts/run-donor-view-consumers.sh` 的必跑段。通过的 fixture 只证明源码和合成 HTTP/数据库合同；不宣称线上时延、部署或 Provider 回执。
