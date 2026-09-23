# AI-CRM v3

AI-CRM v3 是单企业私有化 CRM 的 Go 主运行仓库。当前已交付 PostgreSQL 平台底座、OneID 身份内核、员工认证与权限、企微 OAuth/回调/JSSDK/侧边栏身份，以及企微客户激活、客户目录和一次性 declared 手机号绑定能力。

固定供体基线：

- `AI-CRM-production@4af15e64fb7ebb311b52b17eaf5fc5ea5e8154c8`：生产行为与 OneID 参考；
- `AI-CRM@69c5282fb38058f2cc9872b6feb3f0f54bfad64b`：管理后台和企微侧边栏视觉壳。
- `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`：客户列表、游标分页和企微目录同步 Behavior Contract。

供体仓不是运行时依赖，也未复制其 Git 历史。

## 本期能力

- 渠道中立 `customers.id`、外部身份唯一约束、冲突、关联意图、合并候选、管理员确认归并和可审计撤销；
- 本地 Argon2id 密码、数据库 Session、CSRF、登录限流、固定三角色和 `session_version` 即时失效；
- 企微员工登录、侧边栏 OAuth、JSSDK 签名、客户上下文令牌、外部联系人加密回调和幂等 Inbox worker；
- 独立客户联系 Secret、可恢复企微全量同步、逐项收据、Outbox 目录投影和 02:30 外部 oneshot 对账 timer；
- `/admin/customers` 列表/详情、固定 watermark 游标、精确手机号筛选、脱敏与审计揭示；
- `cmd/migrate-phone-identities` 只接受已校验快照，支持 `inspect/dry-run/apply/reconcile/rollback`，不长期连接源生产环境；
- 后台完整 CRM 菜单、登录/首页/员工权限/OneID 查询页和侧边栏壳；尚未开发的业务统一显示“功能待接入”，不会调用旧 API；
- 支付宝仅实现通用身份 Provider 契约和 Fake Adapter，不包含支付宝网络调用、订单或支付；
- `main` 必过 `make check`。GitHub Actions 的部署默认关闭，只有仓库变量 `AICRM_ENABLE_ACTIONS_DEPLOY` 精确为 `true` 才会通过固定 SSH 主机密钥发布版本化 release；常规合并后按本地完整发布流程执行。

公开 HTTP 契约见 [OpenAPI](api/openapi.yaml)，数据迁移见 [migrations](migrations)，部署约束见 [部署说明](deploy/README.md)。

## 本地运行

需要 Go 1.26 和 PostgreSQL 16。先创建数据库并执行迁移：

```bash
export AICRM_DATABASE_URL='postgres://aicrm:password@127.0.0.1:5432/aicrm?sslmode=disable'
go run ./cmd/migrate-platform
```

首次启动可用环境变量幂等创建超级管理员：

```bash
export AICRM_BOOTSTRAP_USERNAME='admin'
export AICRM_BOOTSTRAP_PASSWORD='replace-with-a-strong-password'
export AICRM_BOOTSTRAP_DISPLAY_NAME='系统管理员'
make run
```

默认只监听 `127.0.0.1:8080`。公网流量由 Caddy 在 80/443 终止 HTTPS 后反向代理：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

`AICRM_WECOM_ENABLED=false` 和 `AICRM_WECOM_CUSTOMER_SYNC_ENABLED=false` 是安全默认值。客户同步还必须单独提供 `AICRM_WECOM_CONTACT_SECRET`；它不复用 OAuth 应用 Secret。

企微标签目录读取另有最窄的独立开关：`AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED=false`。`id-dev` 保持关闭；只有同时明确配置 `catalog-read-authorized` 权限时才允许启用，只读取企业标签目录，不包含客户打标或去标。目录创建、改名和归档另需 `AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true`、`AICRM_OUTBOUND_PROVIDER_ENABLED=true`、可用企微通讯录凭据以及独立的 `catalog-write-authorized` 确认；读取授权永远不会开启写入。

## 验证

```bash
make check
go test -race ./cmd/aicrm ./internal/access/... ./internal/customer/... ./internal/identity/... ./internal/wecom/...
govulncheck ./...
```
