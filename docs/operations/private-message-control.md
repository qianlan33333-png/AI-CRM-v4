# 原企微任务停止、查询和对账

已有管理员登录的 CLI 可由外部进程直接调用。停止是接受原任务的独立异步操作，202/queued 不表示已经停止；查询 control.state 的 stopped、delivered 或 outcome_unknown。不会创建新群发或重发内容。停不了的未知结果保留原操作，不换键盲重试。

先读原接收人版本，写入请求文件 `{"expected_version":1,"reason":"废弃已被新稿替代的任务"}`，再调用：

```sh
aicrm-cli invoke admin.stopAIAssistantRecipient --param plan_id=5 --param recipient_id=17 --idempotency-key original-task-stop-5-17 --input stop-request.json
aicrm-cli invoke admin.getAIAssistantRecipientControl --param plan_id=5 --param recipient_id=17 --query action=stop
aicrm-cli invoke admin.reconcileAIAssistantRecipientDelivery --param plan_id=6 --param recipient_id=24 --idempotency-key delivery-observation-6-24-1 --input reconcile-request.json
aicrm-cli invoke admin.getAIAssistantRecipientControl --param plan_id=6 --param recipient_id=24 --query action=reconcile
```

同一逻辑请求复用同键，修改请求不能复用旧键。停止同一原任务即使换键也返回原停止操作；对账的新观察可用新逻辑键再次读取迟到回执。输入只允许计划/接收人、版本及原因，不能指定替代 msgid、发送员工或客户外部 ID。非唯一客户任务拒绝，防止停止同一 msgid 内无关成员。

使用既有人类账号权限和 CSRF；机器 token 不冒充指定员工。用户指定的 wecom-staff 账号在生产是 ID 10、有效 admin，后台登录已启用。此能力属于管理员 action aiassistant.task.stop，viewer 不具写权限，不转移唯一超级管理员。

工作台交接与实际发送独立：Provider 接受后交接完成；待员工发送不形成客户通用锁。原包幂等、同节点去重、原周期和服务预算仍保留。停止与已发送事实按原成员同步；已发送保留原 sent_at，不伪造成取消。生产维护已停止四条原未发送任务，并回填一条已发送时间；工作台 17 条新稿的占位预检为 17 ready。该结果不代替最终审核、手机发送或送达验收。

本次规则定位于工作台 operator_workflow.request_handoff → newcomer_node_ledger.reserve，在新 CRM 计划创建前生效。普通客户群 Group Ops 和直接 CRM AI Assistant 调用不经过该检查，不能以其测试覆盖工作台新人 SOP。工作台当前生产目录没有 Git 历史；2026-10-14 13:14 前备份中的原文件已含两条阻断规则（旧 SHA db651541e7f0504eabc8588da26767fe99d0823c63dde02540863a25cda54d9b，文件修改时间 07:00:40 +08:00）。这只能证明当时已经存在，不能证明首次引入日期或原始需求。首次引入提交、原 PRD 和首次测试记录尚未确认。

安装合同的 Host 复用校验修复只调整源码工具和夹具。361be23 的实际包、631034f 的 Host SHA、manifest 及资源字节保持；仅排除构建器已明确排除的 source-index.json/source-lock.json，其他 Git type/mode/blob/path 漂移、web 根形状变化及资源摘要不一致均拒绝。工作台在原受保护环境、原锁和原 in_flight 中，从已检查修复提交加载准确 stage_check，使用 --test-source-sha 绑定修复源码执行原注册合同，再由原 poll 验证收据；不改写通过、不重装应用或工具、不新增恢复状态。随后工具源码按现有维护入口纳入，原安装收据和新测试源码分别记录。
