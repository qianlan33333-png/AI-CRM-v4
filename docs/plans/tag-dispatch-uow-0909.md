# 标签写入读取快照事务修复

OneID: 不涉及新的客户身份；仅标签配置和企微标签绑定。
Persistence: Tag 所有者短 PostgreSQL UoW 读取已冻结 dispatch；事务结束后 Outbound 才调用 Provider。接受、重试、回执继续使用现有 EER 与 Tag 同一 UoW。
External Effects: 不新增队列、Provider writer、重试状态机或身份匹配。只修正 composition 的稳定读取 Port 注入。

线上配置已启用，但直接注入要求事务上下文的 Store，导致 dispatch_changed、call_attempted=false。将 provider 的依赖收窄为 CatalogMutationDispatchReader，在 composition 通过短事务读取后返回。

恢复仅增加固定 dispatch_changed + 当前 EER envelope fingerprint 的匹配证据，且要求历史 attempt 已完成、未调用、未执行；其它未知证据仍阻止。当前 Tag 快照一致性校验和原 effect 重用保持不变。

真实 PostgreSQL 测试重现旧接线失败，按现有恢复 Port 重试同 effect，成功投影真实 Provider 标签 ID；断言 Provider 调用时上下文没有事务且读取事务已关闭。并验证没有重复 Tag、Group 或 effect。
