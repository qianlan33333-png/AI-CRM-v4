# 群邀请改造本地验证记录

- HEAD: `bbd0b9ef54215cedb8c3e092048721657e7f4d5e`
- HEAD tree: `98aa4d6c2334d757c17305fb6c0db27ad15ae579`
- 工作区：未提交，混有本任务及既存并行任务修改；没有创建提交、推送、PR 或部署。以下结果针对本任务执行时的工作区，不是该 HEAD 的已提交发布证据。
- 环境：macOS arm64、PostgreSQL 16.13、Node 24.21.0。项目冻结 Node 为 24.18.0；本地可用运行时不同，因此不能称为完整工具链验收。

## 已通过

命令使用 `PATH=/opt/homebrew/opt/node@24/bin:$PATH`。数据库测试另设 `AICRM_DATABASE_URL='postgres:///postgres?host=/tmp'`，fixture 创建独立 schema 并清理，不接触生产数据。

1. `python3 scripts/dev_preflight.py fast`：格式、边界、冻结供体、差异空白及 preflight 测试。
   证据：`/private/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/aicrm-preflight-6tetog7w`。
2. `python3 scripts/dev_preflight.py compile`：全仓 Go 及测试编译，未执行全部测试。
   证据：`/private/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/aicrm-preflight-drv_d41q`。
3. `go test ./internal/media/store ./internal/groupops/store ./internal/wecom/adapter -run 'Test(PostgreSQL(Invitation|Catalog)|InvitationCode)' -count=1 -v`：真实 PostgreSQL 原子回滚、旧 ID 升级、幂等、固定日程、人工合并补跑、全分页、部分失败、补拉、完成重放、旧观察不覆盖新观察；测试 HTTP Provider 的单群码参数与未知结果保留 config_id。
4. `go test ./internal/media/domain ./internal/media/app ./internal/media/http ./internal/groupops/app ./internal/outbound ./internal/wecom/adapter ./internal/webshell -count=1`，另跑 `go test ./internal/groupops/app ./internal/groupops/http -count=1`。未配置数据库的专项命令不代表相关包所有数据库用例已执行。
5. `AICRM_REQUIRE_CHROMIUM_JOURNEY=1 AICRM_INVITATION_SCREENSHOTS=/tmp/aicrm-invitation-screens go test ./cmd/aicrm -run '^TestPostgreSQLInvitationHostChromiumJourney$' -count=1 -v`：真实 Host 登录、目录搜索、同名群、空阈值无默认、创建/回读；接口拒绝 null/0/201/小数，拒绝未登录；本地数据库模拟人数与群码后验证切换、不回流和公共入口。
   截图：`/tmp/aicrm-invitation-screens/`。模拟 code receipt 不是实际 Provider 回执。
6. `npm run typecheck`、`node web/v3/shared/ui/groupPickerAdapter.test.mjs`。

## 不包含在本轮通过结论中

- 完整 Linux CI 与本地 full（当前工作区非干净已提交树、工具版本和平台不符合 full 条件）。
- 实际企业权限、官方入群码回执、微信扫码入群、生产 24 小时调度观察。
- 多 Worker 进程之间的 Provider 读取合并压力验证。目前同进程 singleflight 和新鲜共享观察合并重复读取；跨进程同时发起的读取仍可能重叠。
- 移动端独立布局全流程验收。

## 入口与兼容

后台 `/admin/group-invitations`；公开 `/gi/{token}`；旧素材 ID 保持。群运营原刷新接口在生产 Composition 中委托同一 Catalog，返回 202 与任务状态，前端引导到统一目录。

本地设计预览 `http://127.0.0.1:4186/admin/group-invitations?tab=directory` 使用真实模板/样式与隔离示例数据，不连接企微。桌面视觉核验见根目录 `design-qa.md`。
