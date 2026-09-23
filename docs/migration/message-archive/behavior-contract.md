# 会话存档行为与 SDK 证据

本模块只消费 `msgaudit_notify` 的已验签、已解密回调，将安全通知先写入既有 `webhook_inbox`，成功提交后才 ACK。`cmd/aicrm` 的既有 `worker` one-shot 在每次启动时依次领取外部联系人与会话存档 Inbox；因此进程重启、lease 到期和失败均复用现有 Inbox 恢复，不新增 ticker、cron、River job 或 archive worker。

一次通知拉取只在收到通知后发生。SDK 网络与解密在数据库事务外；每个已解密页在一个 UoW 中完成 OneID `Resolve`、消息、参与者、问题记录和 seq cursor。页预算耗尽与任何解密或格式错误都是 retryable，cursor 不前进。页预算耗尽在既有 Inbox 达到通用 attempt 上限时使用其 CAS continuation 增加下一次合法领取机会；它不是失败重试耗尽，持续有进展的通知会继续到读空后才完成。未知但格式完整的消息保留受保护 payload；格式无效的原始字节先保留到受保护 issue，再从同一 cursor 重试。

同一页内相同可信参与者和员工目录读取会被 UoW 内存缓存，避免 N+1；缓存不会跨页或持久化。

参与者只有由 WeCom SDK Adapter 从该条已解密记录附带的可信外部联系人事实才进入 OneID `Resolve`。使用既有 `wecom-corp:<corp_id>` scope，且只 Resolve，不 Provision 或合并。供体 `archive_sdk.py` 的既有分类识别 `wm` 和 `wo` 外部联系人形式；任何其余未能从可信事实确认的值保持 unresolved。

SDK 不提交仓库。Linux CI 从企业微信官方 [91774 文档所列下载地址](https://wwcdn.weixin.qq.com/node/wwcomm/sdk_x86_v3_20250205.tgz)下载 x86 v3 `20250205` 包，校验包 SHA-256 `afa8c017da2994ad2215933f2fcc6042d40d935663ad42d6e1e9d7716652f0d8`，再校验 `C_sdk/libWeWorkFinanceSdk_C.so` SHA-256 `79ced4de6b18d5e96a21cd06f325794dc8957f8925120538d56d4ce827d3dfd0`。隔离 runner 使用 `dlopen`、符号校验、`NewSdk`/`DestroySdk` 进行无凭据 ABI 健康验证；不调用 `Init`、不读取真实会话、不会携带生产 secret。

## 本地读取、筛选与私有图片

`/admin/message-archive/customers/{customer_id}` 是 archive-owned 的最小读取 Host，不接入 Customer 360、精简 sidebar 或共享导航。`/api/admin/message-archive/...` 要求已认证的 Admin/SuperAdmin，并为每次文本、员工筛选列表或媒体读取写入审计。已入库记录的读取独立于 Provider/SDK 开关；关闭 SDK 后本地文本、关键词、单聊/群聊、开始/结束时间、员工、消息类型和方向筛选仍可用，私有媒体读取明确不可用。员工筛选由同一客户归档中已解析的 Access 员工显示名列表提供，HTTP 使用稳定 ID；普通读取者不需要、也不能输入任意内部员工 ID。

图片先按 Customer 当前 OneID 谱系检查 archive-owned 媒体归属，再在事务外以受限分片读取 SDK 媒体。服务端核对可用的大小和 MD5，且只放行 JPEG、PNG、GIF、WebP 的二进制签名；HTML、SVG 和未知字节被拒绝。页面通过受保护 Blob 显示图片，并在离开时释放 object URL。

## 历史导入

`cmd/migrate-message-archive` 的 `extract` 只能由操作员显式执行：它从非符号链接、权限 `0600` 的 `--source-database-url-file` 打开旧 PostgreSQL，在 `REPEATABLE READ READ ONLY` 事务内读取 `archived_messages(id, seq, msgid, unionid, raw_payload)`。它将 `unionid` 原样映射为 `historical_unionid`，先取可选行字段 `group_name`，缺失时取 `raw_payload.group_name`；对冻结供体的实际 SDK 包装 `{seq, encrypted_record, decrypted_message}`，只把 `decrypted_message` 交给 Archive 正规化器，并严格核验包装层与行的 `seq`、有效 `msgid` 一致。完整受保护包装不复制到 V3；快照保存其 SHA-256，连同解密消息、投影字段进入逐行 receipt/reconcile digest，因此包装层变更也可核验。未知但格式完整的消息类型仍由 Archive 保留受保护 payload。提取不会启动 SDK、推进正常通知 cursor 或调用 Provider。提取要求冻结的 `--source-revision` 与 `--wecom-corp-id`，并以独占创建的 `0600` 文件写出显式离线快照（`aicrm-message-archive-history-v1`）。

其余模式只读取该离线快照：`inspect`、`dry-run`、`apply`、`reconcile` 与受操作员显式调用、有限额的 `re-resolve`；`apply` 要求快照 SHA-256 和 `--confirm-apply`。`dry-run` 会在隔离 PostgreSQL 事务执行与 `apply` 相同的目标冲突、OneID/Access 只读查询、消息与 receipt 写入逻辑，再整体回滚并返回逐源行预计结果。

历史行从不构造 `VerifiedFact`，不会 Provision Customer、写 OneID 身份或合并根。导入及后续 `re-resolve` 仅以参与者已归档的原始值查询 Identity 的既有 `VerifiedWeComCustomer` 和 Access 的既有员工读取 Port；唯一已验证事实可关联至已有 Customer，未找到保持 unresolved，既有 conflict 不被此工具猜测或覆盖。每次后续尝试记为 archive-owned `message_archive_resolution_attempts`，没有后台轮询。每个源行都有独立 receipt；`reconcile` 逐项校验源行 digest、msgid、seq、receipt 结果桶、目标消息、参与者和媒体事实，缺 receipt、内容篡改或冲突都会失败。
