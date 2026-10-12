# 员工姓名刷新修复证据

原任务：`01a12dfe-e7e1-7891-9e14-840db2e536c9`。父 PRD：`docs/prd/2026-10-12-staff-name-refresh.md`。base：工作台 open batch 累计源码 `0b2d5967975f40ae51b92a39dc198a14cdbb3af9`，应用生产仍为 `8245526b89d04111acb2b03f66416b9c10d4064e`。

生产复现：已登录超级管理员页面点击刷新，POST `/api/admin/access/users/refresh-names` 返回 500、internal_error；生产日志记录同一 route_template/status。只读查询生产约束，未包含 refresh_staff_name。用当前服务进程环境在进程内调用安装包 check-enterprise-directory，只输出聚合预检，complete=true，employee_count=46，scope_user_count=2，scope_department_count=1。Provider 读取不是本次故障原因。

真实 PostgreSQL 16 用例 `TestPostgreSQLStaffNameRefreshAuditUpgradeAndAtomicity`：旧 schema 下复现 23514 / ck_admin_access_audit_action，完整回滚；新增迁移不改已有账号和审计；真实 Management + HTTP refresh 路由更新两名员工并写两条审计；账号数量、角色、停用登录状态、员工绑定、授权时间、会话版本与其他字段不变；相同姓名重复刷新不重复审计；只读员工、缺 CSRF、无会话/无效会话均拒绝；第二名员工审计触发失败时前一名更新与审计一起回滚。

直接调用与清单：RefreshStaffNames → completeEnterpriseDirectory → WeCom read Port；之后 Access UOW → StaffNameWriter → AppendAccessAudit。修复只扩展数据库 CHECK，不修改上述生产代码或前端。清单覆盖数据库升级/真实 HTTP/数据保留/原子性，以及现有 app 的 Provider 故障保留姓名测试。局部检查，不称全仓回归。

Product Design 有界流程审核：

1. 已登录员工列表可读，显示 46 名员工，角色与登录状态分列；刷新返回错误并保留列表。错误提示是通用服务不可用，无法向用户解释具体原因。
2. 管理面板可打开，停用登录与角色保存是独立控件；只读查看，未保存/停用/绑定/重置任何生产账号。语义快照识别了 dialog、button、radio 和字段标签；截图不证明完整无障碍合规。

本轮审核截图在仓外 `/Users/qianlan/Downloads/新CRM/release-control/staff-refresh-20261012/`，已检查错误页面与管理面板画面。拒绝了一张用户切页期间的商品页面截图，重新捕获独立员工页。无 UI 代码变更。正式恢复后的页面截图与认证读回待预发/生产安装后验证。

发布：只投递国内精确候选给唯一路由工作台。本批先全部累计、统一预发，生产待人类最终集中授权。新增 Access 前向迁移需按工作台生产迁移流程备份，不绕过工作台直接修改生产约束。

预发具名验收 `staff-name-refresh`：核对安装包内 0238_staff_name_refresh_audit.sql 与候选内容，迁移在已安装 schema 的记录和 CHECK 定义；用已有认证管理员点击刷新，确认响应成功、姓名/审计读回及账号/角色/登录开关/会话版本不变，重复刷新不重复写审计。Provider 若在预发不可读，记录该独立未验证项，不能把 503 称业务成功；可在本批最终授权后生产真实 Provider 页面补验。真实 Provider 已有本次生产只读完整预检证据。
