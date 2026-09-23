# PRD：渠道码中心 OneID 重构与一次性历史导入

## 1. 产品目标

在 AI-CRM-v3 提供可生产使用的渠道码中心：运营人员可管理渠道、配置客服与欢迎内容、发布和维护企微联系二维码/获客链接，并在扫码回调后获得可靠的 OneID 归因、负责人分配、欢迎内容及入渠标签执行结果。同时，将正式快照时点前的旧系统渠道历史一次性导入为不可执行的审计事实。

本项目不复制 AI-CRM-v2 后端，不切换或改造旧系统，不迁订单、问卷、会员等领域，不双写、不持续同步，也不处理签字快照时点之后产生的旧系统数据。

## 2. 用户与权限

| 用户 | 目标 | 权限 |
|---|---|---|
| Viewer | 查看渠道、资产状态、近期用户和历史 | 只读 |
| Admin | 创建、编辑、启停和归档本地渠道配置 | 本地配置写，不执行 Provider 写 |
| SuperAdmin | 发布/更新/删除企微资产及人工对账 | Provider 效果和对账 |
| 迁移操作员 | 执行受控历史快照、导入和核验 | 命令行、显式摘要确认 |

所有管理端写请求要求登录、CSRF、严格 JSON、请求体上限和幂等键；敏感读取返回 `Cache-Control: no-store`。

## 3. 核心场景

### 3.1 渠道目录

- 按关键字、状态和游标查看渠道列表。
- 创建唯一且创建后不可修改的 `channel_code`。
- 通过版本/ETag CAS 编辑渠道，避免覆盖并发修改。
- 启用、停用和归档渠道；有历史、资产或归因引用时禁止硬删除。
- 每次编辑形成不可变配置版本，保留名称、载体、场景值、欢迎内容、素材引用、标签和客服策略。

### 3.2 客服分配

- 单负责人，或 1–5 名多客服。
- 支持比例分配和 24 小时满额切换。
- 候选人必须同时满足本地 active staff 和企微 follow-user；企微读取失败时失败关闭。
- 比例、顺序、容量和溢出规则由服务端验证。

### 3.3 欢迎内容与标签

- 欢迎文本可与图片、小程序、附件、群邀请组合。
- Channel 只保存 Media/Tag 的稳定本地引用；回调处理时在同一事务中锁定来源并冻结 Provider-ready 素材快照。
- WelcomeCode 在企微回调事务中加密为 10 分钟 opaque grant；明文不进入日志、Channel、Outbound 或 External Effects。
- Outbound 兑换 grant 并按企微 `text/image/file/miniprogram/link` 协议发送。
- 入渠标签执行时通过 canonical customer 和当前 relationship 解析企微联系人，不在 Channel/EER 保存 `external_userid`。

### 3.4 二维码与获客链接

- 渠道资产发布在本地业务记录、审计、Outbox 和 External Effect 同一 PostgreSQL UoW 中受理。
- 二维码支持创建、读取、基于当前完整渠道配置更新、删除、结果回读和显式对账。
- 获客链接兼容入口支持列表、详情、创建、完整更新、删除和显式对账。
- 状态区分 `accepted → queued → attempted → executed/outcome_unknown/final_failed → reconciled`。
- 非终态不可复制或下载；二维码只经 same-origin 受控下载，限制域名、重定向、MIME、大小和超时。
- 更新或删除成功后，旧资产及其 State binding 在同一事务中退役；历史扫码仍可按事件时间核验，新的回调只匹配当前有效绑定。
- `outcome_unknown` 禁止换幂等键重试，只允许原 effect 范围的 Provider readback 或人工证据对账。

### 3.5 扫码归因

- raw State 仅在企微回调边界计算 HMAC，Channel 只接收摘要。
- State 的 0、1、N 个有效匹配分别产生 unmatched、attributed、ambiguous，不按最新资产猜测。
- 只有 Provider-verified 且带 corp scope 的 `external_userid` 可进入 Identity Resolve；仅 verified not-found 才显式建客。
- identity conflict 保留冲突收据，不允许渠道页面强制绑定或合并。
- 归因、relationship、负责人、欢迎/标签效果受理、回调收据、审计和 Outbox 按命令要求原子提交。

### 3.6 历史读取

- 近期用户与历史联系人只展示 canonical `customer_id`、安全名称、时间和统计。
- 历史客服保留名称快照、顺序、比例/容量和旧状态。
- 归因收据可查看并追加式修正，旧证据不可改写。
- API 不返回 raw UnionID、OpenID、`external_userid`、手机号或 State。

## 4. 一次性历史导入

`cmd/migrate-channel-history` 提供：

1. `inspect`：生产只读 schema discovery、一致性快照、逐表摘要和异常分布报告。
2. `validate`：校验 AES-256-GCM 快照、manifest 和逐行摘要。
3. `dry-run`：确认范围及零 Provider 调用/零 Provider effect。
4. `import`：断点续跑导入，每行归入 `imported/already_imported/unresolved/quarantined/invalid`。
5. `reconcile`：验证行数、重复映射、静默丢失和 OneID 绑定。
6. `replay-check`：同快照再次执行并证明事实计数不增长。
7. `rollback`：无新运行时引用时受控回滚；否则失败关闭。

历史身份只依赖 `identityport.Resolver`。scope 完整且唯一命中才关联 `customer_id`；未命中、缺 scope 或冲突保留 unresolved。旧二维码、链接和效果日志仅作为不可执行历史事实，不创建 River Job、External Effect 或 Provider 请求。

## 5. 非功能要求

- PostgreSQL 16、模块化单体、单企业、单数据库。
- Provider 网络调用不持有数据库事务。
- Provider 默认 disabled，并按二维码、欢迎语、标签分别灰度。
- 所有外部写只由 Outbound 拥有；Channel 不自建队列、Worker、lease/fence 或重试状态机。
- token、secret、OAuth code、openid、UnionID、`external_userid` 和手机号不得进入结构化日志。
- 供体固定为 `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`；两张活跃渠道模板通过逐字节 hash gate。

## 6. 验收标准

- `/admin/channels`、新建/编辑页使用真实 PostgreSQL API，无 Mock 回退。
- CAS、权限、CSRF、幂等、游标和稳定错误码全部通过自动化测试。
- 使用专用渠道、员工和测试用户完成二维码发布、扫码归因、欢迎文本/素材和标签灰度。
- 获客链接和二维码更新/删除可读取真实终态，未知结果可在原 effect 上对账。
- 正式历史导入满足：

```text
wrong_oneid_bindings = 0
duplicate_source_maps = 0
provider_effects_created_by_import = 0
provider_calls_during_import = 0
silent_loss = 0
```

- 同一正式快照 `replay-check` 不新增任何历史事实。

## 7. 发布与回退

1. 合并并部署 Provider 全关闭版本，验证 release SHA、migration、`/readyz`、页面和 PostgreSQL API。
2. 对源库执行只读 discovery，签字确认 snapshot timestamp 和表清单。
3. 在隔离 PG16 完成导入、对账、重放、回滚、再次导入演练。
4. 目标生产库建立备份/恢复点，再执行 `validate → dry-run → import → reconcile → replay-check`。
5. 历史导入完成后按能力逐项灰度 Provider；任一未知结果保持关闭并走原 effect 对账。

本项目不改变旧系统运行状态。应用发布可通过上一 release 回退；历史导入只能使用导入器的受控 rollback，并在存在新运行时引用时拒绝回滚。
