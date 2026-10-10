# 侧栏技术发布验证

复用已批准的 `docs/prd/2026-10-09-sidebar-ai-operations.md`；当前业务候选为 `2e07a7c89cb31e91072bca43c942f2d017ad6fe4`，本补充只修改验证材料和 AI 清单，无 UI 或生产业务逻辑变更。

分类：安装包测试只在隔离 PostgreSQL 中建立现有 OneID 的可信员工/客户关系和合法会话，不建新身份机制；无生产持久数据修改、Provider 调用或 worker。复用当前 `InstalledProduct`、Access 登录、API 子进程及 TLS loopback 代理，参考仓内 `public_survey_installed_fixture_test.go` 与 `audience_installed_fixture_test.go`，不引入第二发布路径。

```mermaid
flowchart LR
 A[准确候选 相关合同] --> B[最终构建一次]
 B --> C[安装包 API与资源]
 C --> D[隔离本人读取和拒绝路径]
 D --> E[已有授权 同包生产读回]
```

清单从23项缩为15项：保留3个Context Center Python合同、Sidebar小包race、默认关闭配置、当前员工认证、Reader/Generator装配、侧栏模板与CSP、OpenAPI、两个侧栏UI合同、Host adapter以及一个真实Sidebar Chromium。去掉未改动的HTTP配置包、问卷Bootstrap、订单和全站DOM、三个Automation Chromium及独立测试配置helper包；其编译由已保留的直接消费者完成。允许这些无关旅程漏测风险，不称完整回归。

`TestPostgreSQLSidebarAIOperationsInstalled` 使用安装二进制，Source仅种入隔离fixture及读取最终数据库事实。验收本人当前员工会话得到 `unconfigured`，没有摘要/快照/生成可用；匿名、无当前Cookie、跨员工、撤销员工、查询注入、原始对话注入、未确认及伪造快照均拒绝；零effects/发送intent、仅API进程。所需安装输入缺失不能计入发布通过。

实际共享预发和生产另读标准Host/overlay字节、ready版本、开关关闭和匿名401；该证据不证明55接通或模型生成成功。远端exporter、55部署与认证/真实模型保持原handoff的未完成边界，不重启远端Agent服务。
