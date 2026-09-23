# PRD：黄小璨 UUID 日课导入小程序素材库

状态：已确认冻结  
负责人：Codex  
板块：Media / 小程序素材库  
分支/worktree：`codex/hxc-daily-lessons-media-import` / `hxc-daily-lessons-media-import`  
预计上线窗口：CI、合并和生产部署门禁通过后的首个串行发布窗口

## 1. 业务判断

- 用户与场景：CRM 管理员需要把黄小璨小程序后台当前已发布日课作为可复用的小程序素材。
- 要解决的问题：源系统已有 1,162 条日课；本次只导入其中 ID 为 UUID 的 1,062 条，100 条非 UUID 明确排除。
- 核心规则与边界：
  - 只接受源快照中 `published`、未删除、UUID ID、标题非空、封面 PNG 可验证的记录。
  - 固定映射：AppID `wx0ca836834b18e989`；路径 `pages/article/article?lesson_id={uuid}&from=learn`；名称和卡片标题使用日课标题；小程序分组为“日课”。
  - 每条日课同时落一个封面图片素材和一个小程序素材；小程序引用该图片。
  - `hxc-daily-lessons + UUID` 是稳定源键；重复执行只读回原映射，不重复创建。
  - 非 UUID、源条数漂移、重复 ID、摘要不一致、无效 PNG、已有映射漂移均停止，不降级导入。
  - 首次导入后增加每日 10:00（Asia/Shanghai）增量同步：源接口没有 cursor/updated_at，因此每次只做轻量目录扫描，并只下载、写入尚未存在完整 UUID 映射的新记录。
  - 已有 UUID 映射保持不可变，不在定时任务中覆盖；源记录漂移留给显式对账/修复流程。非 UUID 继续排除。
- 成功标准：冻结快照精确包含 1,062 条；生产 apply 后新增或重放合计 1,062 条；verify 证明 1,062 个小程序映射、封面引用、分组、AppID、路径、标题和启用状态一致；素材库认证读回总数和抽样内容一致。

## 2. 市场与 GitHub 调研

| 来源 | 类型 | 可借鉴能力 | 采用/舍弃 | 原因 |
| --- | --- | --- | --- | --- |
| Contentful environments and migration scripts | 成熟产品 | 先冻结迁移文件，在隔离环境验证，再提升到生产 | 采用冻结快照与逐环境验证；舍弃其内容模型环境机制 | 本次是数据导入，不是内容模型发布 |
| Strapi data transfer / export-import 思路 | 成熟产品 | 明确源快照、目标导入和完整性校验 | 采用快照摘要、inspect/apply/verify 分离；不引入其 CLI | 仓库已有更严格的本地迁移命令模式 |
| `golang-migrate/migrate` | GitHub | 顺序、事务、失败即停、可重复执行 | 采用事务/失败即停原则；不新增依赖 | 现有 PostgreSQL UoW 和迁移命令足够 |
| 本仓 `cmd/migrate-media-legacy-materials` | 仓库复用 | 冻结快照、SHA 确认、显式 actor、inspect/dry-run/apply/verify、不可变映射 | 直接扩展同一交付模式 | 最符合 v3 既有治理和发布合同 |

## 3. 复用与架构分类

- 已有领域/模块：`internal/media`、`media_images`、`media_miniprograms`、`media_material_groups`、`media_legacy_material_mappings`、Media 审计/Outbox/操作收据。
- 共享组件与标准组件：复用现有小程序素材库页面和“日课”分组，不增加页面或并行素材模型。
- OneID：不涉及；日课是内容素材，不读取、解析、创建或关联客户身份。
- Persistence：Provider 读取只发生在离线快照提取；生产导入是本地 PostgreSQL 事务。无 Provider 写入或 External Effect。
- 复用、扩展和舍弃方案：新增独立迁移命令和 Media-owned 原子导入方法；复用稳定映射、收据、审计、Outbox、图片检查和引用账本；定时任务使用 systemd oneshot + Persistent timer，不使用进程内 cron/ticker、后台 HTTP 循环或源 MySQL。

## 4. 产品与技术边界

- 用户流程：离线 `extract` 生成 0600 冻结快照 → `inspect` → 测试库 `dry-run` → 生产 `apply`（精确 SHA + actor + 显式确认）→ `verify` → 管理端认证读回；随后每天 10:00 由 `migrate-hxc-daily-lessons --mode sync` 扫描新增 UUID 并幂等写入。
- 页面：不改前端；导入结果出现在现有“小程序”素材板块和“日课”分组。
- API/Port/事件：不新增线上业务 API；离线只读源接口为 `/api/lesson/list` 与 `/api/share/lesson-card/{id}.png`。
- 数据 Owner：所有写入由 Media 拥有；每条日课的 blob、图片、小程序、两条源映射、引用、收据、审计和 Outbox 在一个 PostgreSQL UoW 中提交。
- 权限与审计：apply 必须提供管理员 ID、精确快照 SHA 和 `--confirm-apply`；日志只输出计数、源 UUID 和稳定错误码，不输出密钥或正文。
- 并行依赖：不改 Composition Root、HTTP 路由或页面；需要串行修改发布包二进制清单。

## 5. 测试与验收

| 测试层 | 是否适用 | 命令/旅程 | 结果要求 |
| --- | --- | --- | --- |
| 静态/架构/敏感信息 | 是 | `python3 scripts/dev_preflight.py fast` | 通过 |
| 编译/单元 | 是 | `python3 scripts/dev_preflight.py compile`、命令专项测试 | 通过 |
| PostgreSQL/迁移/事务 | 是 | 临时 schema：apply、replay、verify、漂移/回滚 | 通过 |
| 集成/真实读回 | 是 | 源快照 1,062；生产映射/素材/封面引用/分组聚合读回 | 精确一致 |
| 前端/Chromium | 否 | 无页面改动；部署后用既有管理页读回 | 页面能看到分组和素材 |
| OneID / External Effects | 否 | 架构检查 | 不引入依赖 |

## 6. 并行与发布快照

```text
开始 main：c7588a6bafa0039a0ccdf7d95d3d3ed9a55f5391
活跃 PR：未发现与 HXC 日课导入、Media 导入命令或新迁移重叠的活跃 PR
重叠文件：Media store、新迁移命令、发布包二进制清单（后两者串行）
其他已合并但未部署版本：合并/部署前重新核对
当前排队上线版本：合并/部署前重新核对
本次重新运行：fast、compile、专项 PostgreSQL、required CI、发布包合同
```

## 7. 上线、监控与回滚

- 先合并 GitHub PR，再从准确 merge SHA 的干净 checkout 构建并 SSH 部署。
- 导入前记录目标表和映射基线；先 dry-run，再对精确快照执行一次 apply。
- 观察：源 1,062、new+replayed 1,062、verify 1,062、失败 0；抽样首/中/末素材并认证读取封面。
- apply 任一行失败时该行完整回滚并停止后续处理；已提交行可通过同快照安全重放续跑。
- 不自动删除已导入素材。若验收失败，停止使用“日课”分组并根据本次精确映射清单制定单独、经确认的可恢复清理方案。

## 8. 确认与变更

- 用户确认：2026-09-20 明确要求“不考虑非UUID。先把已有的1,062 条是 UUID全部写入”。
- 冻结解释：采用上一轮已说明的标题/AppID/路径/封面/“日课”分组映射；非 UUID 不进入候选、失败或完成计数。
- 重大变更需重新确认：改变 1,062 条范围、导入草稿/删除项、去掉封面、启用持续同步或处理非 UUID。
