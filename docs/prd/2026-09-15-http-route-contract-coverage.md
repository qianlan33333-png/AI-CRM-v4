# 管理端与 Runner HTTP 路由合同补测（窄 PRD）

**状态：已批准实施；基线：`2218a90abae864c9f25d437f65dd930b9fa3a013`。**

## 业务判断与范围

这是一项测试覆盖缺口修复：五组已上线的 HTTP handler 路由已有业务实现，却缺少成功转发、认证和错误映射的直接合同用例。它不修改生产路由、领域服务、数据库 schema、OpenAPI 或 CI runner，也不恢复旧的 `scripts/testing` harness。

- **OneID：不涉及。** 测试对象不读取或写入客户、渠道外部身份或客户归属。
- **持久化与执行：无新增持久化。** 测试仅以进程内 Store、UoW、事件和队列边界 fake 验证 handler 到既有 App/Port 的调用；不连接 PostgreSQL、Provider、内部 jobqueue 或外部效果。标签同步路由的 `queued` 仅为既有 SyncService 收据形状，不是 Provider 执行或业务回执。

旧提交 `59c283334c9f1c0452795694178ec9be20f1b826` 只用于比对已存在路由的行为目标；本 PR 按当前基线的接口和 DTO 重写最小 fake，不复制旧候选提交或其测试运行入口。

## 源码证据与实施

1. `internal/channel/acquisition_links_http.go` 的 `mutate` 已处理 `PATCH /{link_id}` 和 `DELETE /{link_id}`，但现有 `acquisition_links_http_test.go` 只覆盖创建、未知 JSON 与 reconcile。补 update/delete 成功命令的 actor、link ID、幂等键及用户/部门输入转发，并锁定空 DELETE body、重复幂等键和 CSRF 失败的拒绝；被拒绝请求不得调用 mutation fake。
2. `internal/operationcycle/http/handler.go` 已将管理员的 run、action result、run versions 和 proposal decision 交给 `operationapp.Service`。补最小 Store fake，锁定 key/page、admin actor、非法 decision、直接 Authenticate 失败的管理读取、CSRF 失败，以及 `ErrNotFound`、`ErrUnavailable`、`ErrConflict` 到 HTTP 状态的映射；每个被拒绝写入的 fake 调用计数必须保持为零。
3. 同一 handler 的 runner 分支已经支持 context index、strategy context 和 strategy-change proposal。补 service token、`limit/offset/mode`、proposal payload/幂等键和 malformed JSON 的合同；不启动 runner 或接真实 token。
4. `internal/tag/http/handler.go` 已有 live gate 和 sync-due 路由。复用该 package 已有 CatalogStore/UoW/Security 测试形状，记录 SyncCommand，锁定 gate 成功、401、403、503 以及 sync-due 的 actor、trace、幂等键、202、CSRF、未知 JSON。

文件范围只限：

- `internal/channel/acquisition_links_http_test.go`
- `internal/operationcycle/http/route_contract_test.go`
- `internal/tag/http/route_contract_test.go`
- 本 PRD

## 参考、验证与边界

仓内现有 `httptest.NewRequest` / `httptest.NewRecorder` 模式见 `internal/operationcycle/http/handler_test.go`、`internal/tag/http/handler_test.go` 和 `internal/channel/acquisition_links_http_test.go`。Go 官方的 [httptest example](https://github.com/golang/go/blob/master/src/net/http/httptest/example_test.go) 提供相同的标准请求—handler—response 形状。当前仓库的 [tag handler test](https://github.com/qianlan33333-png/AI-CRM-v3/blob/2218a90abae864c9f25d437f65dd930b9fa3a013/internal/tag/http/handler_test.go) 是领域 fake 的参考；这些链接不是线上行为证明。

定向 `go test` 只证明内存 HTTP 合同。提交前运行仓库要求的 `python3 scripts/dev_preflight.py fast`、`python3 scripts/dev_preflight.py compile` 及受影响 Go package 测试；完整 backend CI、合并、部署和真实业务/Provider 收据另行记录。任何发现需要生产实现变更、新依赖、数据库或 Provider 才能测试的情形，停止并报 root。
