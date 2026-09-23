# 小程序素材：modal 内前置校验与持久反馈

**状态：** 已实现，待 root diff 审核
**开发基线：** `7346fdd9a4b4a1f141b4955cb3db1250c8a81d7d`（fresh `origin/main` worktree）
**用户入口：** `/admin/miniprogram-library` → “新建小程序卡片”或“编辑”

## 业务判断与证据边界

线上空表单点击未观察到创建请求，符合冻结 controller 的客户端 return 分支；线上未捕捉到提示只能归类为 2.4 秒 toast 未采到或持久反馈不足，不能据此声称线上资产损坏、后端未就绪或写入失败。这个判断由当前源码链自包含支撑：`RenderMedia` 挂载 `mpLib` 和 `materialSaveHost`，冻结 `saveMp()` 在调用 `saveMpItem` 前 return。

源码事实是冻结 donor 的 `saveMp()` 目前硬性校验素材名称与 AppID，然后将 PagePath 和卡片标题原样交给现有 `saveMpItem`；冻结 API adapter 在其 POST 载荷中把空卡片标题补为 Name。Media 领域的 `ValidMiniProgram` 则要求 AppID、PagePath 和持久化 Title 非空；`NewMiniProgram` 仍在**创建**时将 Name/Title 互相回填，更新故意不同，空 Title 会被拒绝。这样，缺 PagePath 的 create 目前可能发出服务端必然拒绝的请求；短 toast 也不是可聚焦、可持续读取的表单错误。

已按 Product Design `audit` 路由使用现有审计证据；其 user-context preflight 未找到保存的设计上下文。前端组件索引命中管理端单壳、冻结素材模板和通用反馈层。复用路径是 `RenderMedia` → manifest `materialSaveHost` → `web/v3/materialSaveAdapter.ts`；冻结 donor 和 Admin Shell 不改，也不重设计 modal。

GitHub 代码搜索没有发现可复用的 V3 小程序 preflight 实现。相同冻结 donor 的历史 `saveMp` 空值 toast 是行为来源，不是可直接修改的生产所有者；本 PR 在 V3 Host 接缝补足可访问错误呈现。

## 架构分类

| 判断 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不涉及。小程序素材表单不读取、解析或关联客户或外部用户身份。 |
| 持久化 | 无效输入为纯前端、零 HTTP 写入。有效提交沿用 Media 领域既有 PostgreSQL 创建/更新、幂等键和服务端回读；本 PR 不改表、UoW 或 CAS。 |
| 内部持久任务 / Provider | 不新增任务、队列、Provider read/write 或缩略图缓存调用。缩略图既有独立合同不因表单校验改变。 |
| 权限 | 保持现有 Admin 路由、CSRF、Media handler 和服务端授权。 |

## 最小范围

1. 在 V3-owned `materialSaveAdapter` 的 capture 接缝、冻结 controller 调用前识别 `mpLib` 中 create/edit 的“创建”/“保存”。
2. 将错误写入当前 modal 内的持久 `role="alert"` 区，标记无效字段、把焦点移到第一个无效输入，并保留全部输入与打开的 modal。校验拦截时不创建 `activeSave`、不触发 donor `saveMp`、不发 POST。
3. 创建：保持冻结页面现有 Name、AppID、PagePath 必填；Title 空而 Name 有值时不阻断，显示“卡片标题为空，将使用素材名称”。Media API 的 Name/Title 双向回填仍是后台合同；冻结 API adapter 既有载荷回填不变，当前 V3 capture 层不改写输入或请求来绕过 donor 的 Name 必填。
4. 更新：Name、AppID、PagePath、Title 都必须非空；尤其禁止把 create 的 Title 回填规则用于 update。
5. 有效写入继续由既有 `materialSaveAdapter` 提供重复点击锁定、Idempotency-Key、失败/结果未知处理和 collection readback；不伪造“已创建”。

## 明确排除

- 不修改 `web/donor-sources`、`web/dist`、Admin Shell、Media domain/handler、OpenAPI、数据库迁移或缩略图缓存 API。
- 不因一次 live 采样遗漏 toast 而改发布资产、弱化全局 feedback，或把提示改成后端能力错误。
- 不将 create Title 强制必填，或把更新的必填限制放宽为 create 回填。

## 验收

真实冻结 `mpLib.html` 与 V3 Host 共挂载的 JSDOM 测试覆盖：

| 场景 | 预期 |
| --- | --- |
| create 空表单 | modal 留在原处，`role=alert` 可读，首个无效字段获焦点，零 POST。 |
| create 缺 Name / AppID / Path | 显示对应错误，字段 `aria-invalid`，零 POST。 |
| create 仅 Name + AppID + Path | 显示 Title 将由 Name 回填的说明，发一次既有 POST；冻结 API adapter 的既有 Title 载荷回填保持不变，服务端回读后才完成。 |
| create 仅 Title + AppID + Path | 保持当前页面 Name 必填，显示 modal 内错误并零 POST；不以 Host 绕过冻结 controller。 |
| edit 缺 Name/AppID/Path/Title | modal 内错误、聚焦、零 PUT；Title 空不会回填。 |
| 全字段有效、快速双击、写失败/回读失败 | 保持既有单写、幂等键、输入保留与真实回读合同。 |

定向验证：`node web/v3/materialSaveAdapter.test.mjs`，并运行受影响的 TypeScript/build/挂载合同检查。PostgreSQL 或线上浏览器回读只有在实际环境执行后单独记录，不能由 JSDOM 代替。
