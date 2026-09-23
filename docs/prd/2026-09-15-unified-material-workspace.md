# 统一素材工作台

## 1. 目标和业务判断

将后台中的图片素材库、附件素材库和小程序素材库收敛为一个导航入口和一个工作台，使用二级 Tab 切换**图片／附件／小程序**。这次只调整入口、信息层级和已有素材记录的呈现；上传、编辑、启停、下载、删除、缩略图状态、幂等键、CSRF、版本冲突和权限判断继续由当前 Media Owner 及各自 HTTP 合约处理。

当前页面把标题、状态区和列表区分散在三套页面中，且图片页自建顶部卡片。线上只读核查还确认图片、附件页的“素材刷新状态”先完整逐素材表展示一次，随后主列表又展示一次同类记录。目标是一个可识别的“素材库”页面：唯一壳标题、紧凑的类型 Tab／搜索／状态区域、紧随其后的当前类型列表；详情、表单和反馈仍保留原有动作语义。参考用户提供的素材库图的灰白蓝色工作台层级，但不添加图中未被当前数据支持的容量、审核、裁剪或统计能力。

```text
OneID: 不涉及。素材列表、文件和小程序卡片不解析、关联或创建客户身份。
Persistence: 不新增。列表／Tab 仅选择已有 Media 读路径；既有写入仍由 Media Owner 事务处理。
External Effects: 不新增。缩略图可用性、上传和刷新继续沿既有 Owner 行为；统一工作台不触发 Provider 调用、任务、队列或重试。
```

## 2. 当前真实链路和约束

| 类型 | 当前管理页与读取 API | 已有可展示事实 | 保留的 Owner 边界 |
| --- | --- | --- | --- |
| 图片 | `/admin/image-library` → `media.UIBinding` → `RenderMedia(images)` → `imageLibraryFilterHost`；`GET /api/admin/image-library` | `name`、`file_name`、`mime_type`、`file_size`、宽高、分类、标签、启停、创建／更新时间和受保护 variants URL | V3 Image Host 的上传、编辑、启停、删除和精确读回；缩略图 URL 仍是受权 Media 路径 |
| 附件 | `/admin/attachment-library` → `RenderMedia(attach)` → 冻结模板 + `MaterialSaveHost`；`GET /api/admin/attachment-library` | `name`、`file_name`、`mime_type`、`file_size`、说明、标签、启停、版本、创建／更新时间、受权下载 URL | 既有附件上传、版本 CAS、下载、删除引用检查和结果反馈 |
| 小程序 | `/admin/miniprogram-library` → `RenderMedia(mpLib)` → 冻结模板 + `MaterialSaveHost`；`GET /api/admin/miniprogram-library` | `name`、`appid`、`pagepath`、`title`、thumbnail 状态／受权图片 URL、启停、版本、数值创建／更新者、时间 | 既有卡片创建、编辑、启停、删除和缩略图状态；不会把本地缩略图误说成 Provider 已接受 |

三个列表都维持各自的 `q`、`offset`、`limit` 和 `enabled_only` HTTP 语义；图片额外保留 `category`、`tags`、`tag_group` 和 facets。不能以一个跨类型搜索请求代替三类 Owner API，也不能把不同实体的 ID 或缩略图标识互换。

## 3. 路由、导航和呈现方案

### 唯一入口和兼容地址

1. 新 canonical 页面为 `GET /admin/materials?tab=images|attachments|miniprograms`，缺省为 `images`；只接受该受控 tab 参数，非法值回到缺省 Tab。页面 query 不新增搜索持久化或跨页面状态。
2. `/admin/image-library`、`/admin/attachment-library`、`/admin/miniprogram-library` 保持可访问，使用兼容重定向分别进入对应 Tab。构建和 release stage 中既有 `admin/images.html`、`admin/attach.html`、`admin/mpLib.html` 私有 donor aliases 也保留为各自类型的内部兼容载体；它们不是第二个公开页面或新 API。原 API URL 完全不改。
3. 导航 JSON 把三个独立项目替换为单个“素材库”项目，`href` 指向默认图片 Tab；三个旧路径与 canonical 路径都保持 active 状态和既有管理员权限。
4. `media.UIBinding` 只负责 canonical tab 到现有 `images`、`attach`、`mpLib` 页面配置的映射。它不汇总数据、不新建 API，也不绕开 `requireAdminSession` 或 Media HTTP 权限。

### 单一壳和当前类型内容

1. `RenderMedia` 为 unified route 使用既有 `admin_base` 的一个标题栏，标题为“素材库”。页面不再出现图片 Host 或 donor 的重复一级标题。
2. V3-owned `materialLibraryPresentation` 承担 Tab、当前类型说明、紧凑状态／筛选容器和页面级布局；它只装配和呈现，不管理素材写入或 Provider 行为。既有 `MaterialSaveHost` 刷新动作保留，但其默认展示收敛为一行当前刷新状态、上次结果和“查看刷新明细”展开控件；初始不渲染第二张逐素材刷新表。展开区仅复用已有单项状态／失败原因／重试反馈，关闭后不丢失表单或已读列表，也不新增刷新统计。
3. 页面右侧动作复用 `mountPageHeaderActions` 或经生命周期验证后的 `mountPageHeaderActionElements`：图片上传继续走 Image Host 已有回调；附件／小程序必须移动原已绑定节点并在 donor 重绘、disabled/busy、取消和离开页面时恢复／清理。若某个 donor 无法满足该身份和生命周期合同，保留其原 action 但不复制或伪造回调。
4. 图片继续使用 `imageLibraryFilterHost` 的请求 generation、Abort、失败保留、分页和精确删除读回；抽取其已完成的 `materialThumbnailPresentation`，作为受控 thumbnail 的 loading／loaded／error／无 URL 呈现规则。无 URL 内容保持全宽；真实 URL 或错误仍占稳定视觉列。
5. 附件和小程序保留各自 donor 列表／表单行为，但 presentation layer 必须实际生成／映射当前类型的主列表列，不得只替换标题文字：图片为缩略图＋名称、可读大小（KB／MB）、宽×高、创建时间、启用状态、现有操作；附件为文件名／显示名、MIME、可读大小、标签、启用状态、版本、更新时间、现有下载／操作；小程序为缩略图状态、名称／标题、AppID、页面路径、启用状态、版本、更新时间、现有操作。显示字段按当前 DTO 而定：附件不伪造尺寸；小程序不伪造文件大小、图片宽高或创建者姓名。
6. 搜索复用 `committedTextSearch`：只在非 composition 的 Enter 后提交；输入法候选和普通输入不发读请求。切换 Tab 只读取新 Tab 对应的既有类型 API，且不会把 draft 输入当作 query；刷新／分页使用已提交的 query；新请求不会被旧结果覆盖，失败不清空最近成功内容。

## 4. 行为和状态

| 情况 | 可见行为 |
| --- | --- |
| 切换 Tab | 进入对应 canonical `tab`，只读取该类型既有 API；当前 Tab、类型标签和动作一致。 |
| 刷新状态 | 默认只显示紧凑状态与现有刷新入口；展开后才显示已有逐素材状态／失败明细，绝不把它伪装成第二份主列表。 |
| 空结果 | 说明当前类型暂无记录，保留允许的原有创建入口。 |
| 加载／网络或服务失败 | 显示当前类型的受控错误；已有成功列表、搜索 draft、页码和未完成表单不被静默清除。 |
| 401／403 | 明确登录失效或无权读取／操作；不把结果显示成零，不自动切换类型或权限。 |
| 图片缩略图 | loading、成功、加载失败和无 URL 具有稳定盒模型；不主动 fetch 任意 URL，不改变私有 blob／variant 授权。 |
| 附件下载／小程序缩略图 | 继续使用 Media Owner 的受权 URL 和已有不可用说明；不增加上传缩略图或外部刷新入口。 |
| 写入与读回 | 保持既有同键恢复、CAS、删除引用检查和写后读回。Tab／呈现层不二次提交或点击真实 Provider 操作。 |

## 5. 复用、参考和不做事项

### 已确认参考

- GitHub #253：图片 Host 已经证明 source-owned 读取、写后读回、稳定分页、错误反馈和 MaterialSaveHost 边界，作为图片 Tab 的底座。
- GitHub #317：`committedTextSearch`、标准素材选择器、统一反馈和组件不拥有领域写入的约束，作为 Tab 搜索与选择器兼容边界。
- GitHub #329：内容呈现对无 thumbnail、加载失败 thumbnail 和稳定视觉列的规则，作为共享缩略图呈现的回归要求。

### 前端一致性复用表

| 参考页面／组件 | 本次复用 | 受影响调用 |
| --- | --- | --- |
| `admin_base`、`pageHeaderActions` | 唯一标题栏、右侧页面级动作 | unified 素材页及三个旧路径重定向 |
| `committedTextSearch` | Enter／IME 防抖边界、焦点恢复 | 三个类型的搜索控件 |
| `imageLibraryFilterHost`、`materialThumbnailPresentation` | 图片列表、受控缩略图和读取竞态保护 | 图片 Tab、现有素材选择／内容只读呈现 |
| `MaterialSaveHost` | 写入 busy、同键恢复、读回反馈 | 图片、附件、小程序原写入路径 |

Product Design catalog 本轮不可用，未将其视为已执行；视觉基线使用用户已批准的浅灰背景、白色内容区、蓝色主操作和现有 V3 tokens。

不新增跨类型 API、字段、统计、容量、审核、裁剪、上传渠道、Provider 调用、身份读取或持久化模型；不修改冻结 donor。

## 6. 验收

1. 路由：canonical 三 Tab、三条旧页面 URL、三个私有 donor html aliases、导航 active 状态和无权限响应均通过；API 请求仍分别命中原三类路径和正确 query。
2. 搜索：图片／附件／小程序分别验证普通 Enter、中文 IME composition、取消、刷新、分页、乱序返回和失败保留。
3. 呈现：图片的 loading／loaded／error／no-URL；附件下载可见性；小程序 thumbnail unavailable／available 按真实字段呈现。图片、附件、小程序分别核验其定义的实际列和可读字节格式；无 URL 不留空列。默认刷新区不出现第二张逐素材表，展开／收起保留已有状态和失败反馈。
4. 写入回归：图片上传／编辑／删除结果核验，附件上传／CAS／下载，和小程序创建／编辑／缩略图不可用提示均走既有 owner 契约；测试不执行真实 Provider 调用。
5. 视口：真实隔离 PostgreSQL + Chromium 在 1280／1440 验证唯一标题、Tab、动作 containment、空态／失败态和旧 URL 兼容；受影响 Node、Go、typecheck、build、stage 与 source manifest/P5 闭环通过。
