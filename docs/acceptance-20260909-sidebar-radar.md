# 0909 Sidebar 图片准备与后台验收修复

## 边界分类

- Sidebar：读取现有 canonical customer，经原有 Context/Identity Port 授权；不创建或合并身份。图片来自 Media 的 enabled mobile_1080 投影，冻结本地字节后提交 Outbound。
- 图片上传：Provider 写入，由 Outbound 唯一拥有；复用 EER KindOutboundMedia、River、lease/fence、重试和未知结果规则。准备意图、effect 接受、审计与 Outbox 同 PostgreSQL UoW；Provider 调用不占数据库事务；完成结果与 EER 完成同事务。
- 雷达：不涉及 OneID。配置启停为本地事务，沿用 CAS、幂等收据、审计和 Outbox；无 Provider 写入。
- 标签通知与看板按钮：无身份、无持久化变更，只改变显示位置和持续通知。

## 可观察行为

- 图片发送首次点击缺少临时素材时，POST /api/sidebar/v2/send-intents 返回 HTTP 202 与 state=material_preparing，尚无发送 intent/grant。Host 以原幂等键轮询，期间每次确认当前客户；获得真实 media_id 后沿用原 JSSDK 单次 grant 与结果回写。准备操作只上传，不发消息。上传结果未知不盲目重传。
- 暂存素材按企业/应用和内容隔离；有效期预留安全余量。禁用图片不进入准备链路。
- 雷达首次启用保存及后续启停使用 enable/disable 命令名称；旧版 enabled/disabled 不符合收据 operation 约束，会令配置保存的启用步骤 409。
- 标签页不再插入顶部常驻状态通知；真实标签同步和操作反馈保留。
- 看板“立即刷新”移至标题最上方同一行右侧，保留原处理事件。

## 本地证据与线上待验

- PostgreSQL：并发去重、事务回滚、官方回执缓存、到期续备、未知结果锁定；真实 EER RunAttempt 完成与进程退出恢复；未创建聊天发送意图。
- Host：准备完成前零 SDK 调用，相同请求键只执行一次；等待中切换客户不发出后续发送请求。
- Transport：应用凭据与素材作用域一致，只有 token 与图片上传调用，无消息调用；缺少媒体回执不报成功。
- 雷达：首次保存、连续保存、启停、原键重放、旧版本冲突及收据/审计/Outbox 原子性。
- 线上验收分别记录发布状态、Provider 上传回执和当前企微会话实际发送，不把排队或上传成功写成消息送达。
