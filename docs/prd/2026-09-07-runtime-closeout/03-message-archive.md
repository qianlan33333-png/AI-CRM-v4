# 会话存档：通知驱动增量生产接通

基础 PRD：docs/prd/2026-09-05-v3-parity/09-message-archive.md 与 docs/migration/message-archive/behavior-contract.md；旧 extensions/archive/message_archive；用户原 PRD 只作业务依据，停旧/切回调要求不采用。
分类：带作用域 Identity Resolve，未知身份保留待解析；内部 Inbox/jobqueue，同批消息/游标同 PostgreSQL UoW；SDK 是 Provider read，禁止循环轮询和隐式建客。
已有：Go SDK 隔离runner、消息去重/游标/读取/搜索。待接通：生产 archive disabled、SDK/private key/secret 未配置；需核 runner 制品、共享通知分发和私有媒体目录。
流程：msgaudit_notify→持久化收据并快速回复→现有 Inbox 驱动分页 SDK 拉取/解密→msgid 幂等→消息与游标原子提交→OneID 解析→客户读取文本/图片/搜索。未知消息类型保留受保护原事实；分页失败不可错误推进游标；身份待解析不能丢消息。
从旧生产受保护文件定位 Secret/SDK/版本化私钥，复制到 V3 对应受保护路径，保证只读拉取配置不改变旧系统。先给根明确配置/库摘要/ABI和回调路径证据再生产应用。未取得官方通知时要明确通知接通限制，不用定时任务冒充。新消息真实拉取可在已授权生产接通范围验证但只输出聚合元信息。无历史导入。

## 固定约束与验收
旧供体 AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f，只读检出 /private/tmp/aicrm-release-fallback-donors/sidebar；V3 起点 cc4bb8c7e32d4d7d89f6a9a0b0a928bb562584bc，实施前刷新 origin/main。先读 skills/aicrm-v3-development/SKILL.md。实现者在本 PRD 附录逐字段/命令列出原样复用、Go 等价、V3 已有和待补齐的来源证据，之后再编码。前端照搬供体结构、样式、操作顺序，允许 JS 转 TS，仅在 Host/Adapter 做接口适配；遵循已上线单源供体清单机制，不整目录复制。
每板块完整 PR：实际后端/页面/运行装配、必要迁移、架构/编译/专项、真实 PostgreSQL 事务并发恢复、Chromium 操作、协议测试及完整 CI。先修具体失败再跑全量，不削弱冻结或业务断言。历史数据迁移不在本轮。部署只发布审核通过的准确 HEAD，核 API/Worker 版本、dist 和配置实际生效，保留回滚路径。生产网络配置及凭据只经受保护文件；日志、代码、文档不含秘密或 PII。真实发送/支付需有明确业务目标与内容，不为验收擅自发送。
不恢复旧 56 条机器接口，不新建 OneID 匹配、队列、Worker/重试内核，不顺带开发 Campaign、客服权限或客户个人免打扰。发现本板块旧真实流程依赖未覆盖项，报告具体证据，不伪造成功。

## 2026-09-08 生产接通进展

- 已部署版本 `3f5ea38c702b91e37746c2ad1b7bf4f5174e5a2a` 的隔离 SDK 实际 GetChatData 成功，单条测试解密成功，消息标识一致、负载合法。该只读验证没有写入存档业务表，也没有输出消息内容。此前 301042 阻断本次已不再出现。
- 08:21 CST 已通过受保护配置启用存档，API 重启后 ready 正常，实际进程回读启用值和配置匹配；既有 Inbox timer 正常，08:26 Worker 成功退出。未增加存档轮询或专用队列。
- 08:26 核查仍为官方通知收据 0、同步运行 0、游标 0、消息 0；当前完成的是 Provider 读取和生产启用，通知驱动入库尚待证明。需核对企微后台事件接收 URL 与接收应用，未伪造回调或手工写 Inbox 来代替真实通知。
- 受保护环境文件已备份，可恢复原配置后受控重启；本次没有业务数据迁移或数据库结构变更。
