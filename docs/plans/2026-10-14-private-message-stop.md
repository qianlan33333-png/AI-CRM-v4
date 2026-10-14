# Private message stop Implementation Plan

**Goal:** 外部 CLI 停止准确旧企微任务并读取可供工作台释放占位的真实结果。

**Architecture:** AI Owner 在现有 UOW 读取原接收人并提交 Outbound Port；Outbound 通过原 EER/River 执行 Provider 读取与停止，完成事务保存事实。Access 沿用管理员权限，HTTP/OpenAPI/CLI 目录统一登记。

**Tech Stack:** Go、PostgreSQL 16、River、现有 CLI 和企业微信适配器。

1. 新增 Outbound 停止 Port、前向兼容迁移及 EER kind；冻结原 payload_reference，意图与审计同事务。
2. 增加 Provider 适配器和完成 sink；分页核对唯一原客户，已发送只对账，未知不盲重试。
3. AI app/HTTP 暴露接受和结果查询，复用原幂等收据；管理员 Access 授权并核对指定账号。
4. OpenAPI 和生成 CLI 目录同步；执行相关拒绝、并发、回滚、事实保留及 CLI/PG 合同。
5. 本次四条原任务按授权停止、已发送一条对账，工作台按成员保存确认事实，重新做占位预检。
6. dev_preflight prepare、准确提交/verify、国内推送和实时路由实际交接；唯一路由执行安装，随后独立读取业务结果。
