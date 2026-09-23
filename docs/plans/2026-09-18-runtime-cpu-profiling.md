# 受控 API CPU 采样补充计划

本项补齐已批准运行治理计划的性能排障要求。入口集成中；完整 CI、部署和生产验收分别留证。

## 业务判断与边界

- OneID：不涉及。只记录管理员内部编号，不查询、匹配或改变客户身份。
- Persistence：短事务接受和完成本地诊断收据；固定五秒同步瞬时采样，不是需要恢复的异步业务任务，不增加队列、Worker、lease 或重试状态机。
- External Effects：不涉及，没有 Provider 读取或写入。
- 采集当前 API 进程本身，界面必须显示 `api`、发布 SHA、固定 5 秒；不代表 effects-worker、一次性 Worker 或整台主机的 CPU。Worker 采样另列未覆盖。
- 复用超级管理员、CSRF、幂等和同一 Owner 数据边界。不暴露 `net/http/pprof`，不接受 profile 类型、采样时长、路径、URL、PID、命令或请求正文。

## 明确规则

POST `/api/admin/ops-diagnostics/cpu-profiles` 接受空对象和 Idempotency-Key。成功同步返回已完成记录；原键在途、失败、结果未知或已过期均回放原收据，不重新采样。GET 同一路径列出 720 小时内记录；GET `/{opaque_id}` 查询 720 小时内结果；GET `/{opaque_id}/download` 再次校验管理员、状态、截止时间、文件及摘要后下载。

固定每次 CPU 采样 5 秒，原始及净化后压缩产物均不超过 2 MiB，解压处理上限 8 MiB。全局一个数据库 session advisory lock 与进程内互斥共同限制并发；锁使用独占借用连接，采样期间不持有事务。每个滚动小时全局最多 6 次、每管理员 3 次。每次接受预留 2 MiB，最近 720 小时全局最多 64 MiB；已知无产物失败可释放预留，完成按真实字节计，结果未知按预留计直到 TTL。采样前另核对固定注册目录实际文件字节，过期但尚未物理删除的文件也计入 64 MiB 上限；清理故障不能导致无限增长。数据库连接丢失后，最近 30 秒的接受记录仍阻止另一进程启动采样。

只将解析后清空所有标签、数值标签、注释、额外链接并剔除本机目录的标准 pprof 写入已登记的 `/var/lib/aicrm/process-diagnostics`。原始采样只在内存，永不落盘。文件名由服务端随机 128 位 opaque ID 构造，文件 0600，拒绝符号链接、路径穿越、已有文件、硬链接及权限不符。不在日志、HTTP错误或飞书报告输出原始 profile、路径、标签和异常文本。

在线下载在接受时间加 720 小时后拒绝；未解决问题不延期。现有宿主小时清理负责物理删除，不新增调度。最小幂等/管理员操作收据、版本、脱敏结论永久保留，原 profile 不进入数据库。失败、请求取消、进程崩溃或完成记录失败不能显示成功；未知收据不能触发重采。

## 官方参考

- Go runtime/pprof：https://pkg.go.dev/runtime/pprof#StartCPUProfile — 当前进程采样和已有采样冲突。
- Google pprof profile parser：https://github.com/google/pprof/tree/6331bc6350fe/profile — 解析、去除标签并重新序列化，避免只删除标签引用却残留字符串表秘密。

## 根集成与验收

仅当 API 角色、运行巡查及过程清理均已启用才接受采样，复用现有开关，不增加定时任务。集成 API 路由、治理界面、OpenAPI 来源声明、0190 readiness/installer 和数据分类登记后，才可声称入口交付。不得复用旧 DiagnosticSnapshot 的 key/status 投影冒充 profile。必须执行真实五秒 CPU profile 的标准解析、标签 Secret 注入、取消/并发/大小/文件路径保护测试；真实 PostgreSQL 接受与限额/容量/幂等/720h/权限及完成事务失败测试；最后干净 HEAD 的完整 CI 和管理员浏览器旅程。工作区专项不能代替生产验收。
