# 完整员工名单刷新相关修复

原任务 `01a12dfe-e7e1-7891-9e14-840db2e536c9`，复用父 PRD 的晚间完整名单修复范围。新 scope 不增加登录授权或删除账号，不涉及客户 OneID。

当前生产安装应用 83b5e8cb0e858b4f20f9237b10b19e8c9018314e，用户给定新增员工的真实 user/get 成功、已激活、姓名匹配，并在应用可见部门完整 user/simplelist 的46名内。生产只读DB中它恰有一条记录，Active=true、login_enabled=false、access_granted_at=NULL、姓名正确。实际遗漏来自 ListGovernance 过滤未开通员工；原刷新仅更新已有姓名，缺少缺失员工的发现投影。安全聚合诊断不输出 token/Secret 或其他 Provider PII。

实现：canonical POST /api/admin/access/users/refresh（旧 /refresh-names 保留兼容）→ 已核验同企业的完整只读 Port → 共用 StaffProjector → Access 事务内保留所有已存在授权/角色/有效状态，仅创建未授权职工与更新姓名/审计 → 重新读取含未开通员工的治理列表。未开通员工只能通过原 Provider 验证的首次开通命令获得登录；旧角色/密码/绑定/开关命令继续拒绝未授权投影。周期目录与手动目录共用 Access 事务锁以避免重复发现；不新增队列、Worker或身份表，不新增迁移。

测试：真实 PostgreSQL + 合成 HTTP Provider 证明新增入库、已有未开通可见、姓名更新、重复幂等、手动/周期并发只创建一次、跨企业 Reader 拒绝、部分 Provider 失败无副作用、审计失败将新增/姓名/两类审计一起回滚、空目录不删/停既有员工、首次开通复用 ID。旧 0238 升级故障/原子性和未开通投影不能调用治理写的合同保留。Source Chromium 旅程实际点击按钮，显示未开通，复用三步授权弹窗，核对重复刷新与原治理旅程；截图见仓外 staff-directory-refresh-20261012/screenshots，视觉报告见根 design-qa.md。DOM测试验证失败保留、读回成功后才显示成功、普通管理员不能选管理员权限、403清除。

安装验收具名入口：`scripts/run-installed-staff-directory.sh --binary /absolute/bin/aicrm --root /absolute/package --sha APP_SHA --binary-sha256 SHA256`，执行 `TestPostgreSQLStaffDirectoryInstalledChromiumJourney`。标准 AICRM_INSTALLED_PRODUCT_* 输入由脚本设置；Linux、真实安装包 SHA/二进制摘要/Host manifest 和隔离 loopback acceptance DB 被核验。所有浏览器/API流量经TLS前端代理进入该安装二进制进程，源码只播种 disposable 夹具。复用原进程私有CA/CONNECT边界，qyapi五个目录 GET 转至 loopback；禁用外部写/worker且不继承生产凭据。安装包最终读回不能由源 Host PASS代替。

共享 test Provider 夹具扩展另有 `TestInstalledDirectoryProviderReadBoundary`：真实TLS代理读成功，拒绝POST和异域，旧 Survey 两个OAuth读取仍有效。该测试不需要真实 Provider，不能代替前述真实新增员工可见事实。

交付边界：仅相关检查；Linux源旅程、最终安装二进制/Host、预发与生产实际新增员工显示及权限字段读回由唯一工作台保存独立收据。开发不安装共享预发/生产。本批与问卷修复串行累计后统一预发/同包生产，不移动已接受问卷 refs。

最终阶段收敛：工作台已接受新 base 6fcd3f18b554ca0acdfdc6cf35338230902dc39c。按其当前阶段指令，Linux不重复执行源 Host 员工浏览器旅程；本机已真实通过的界面结果保留为独立证据，最终只在安装包执行该界面合同。source 短清单没有依赖 web/dist 的入口；问卷源码检查不重复运行，其已接受代码与五条安装旅程保留。上述早期“Linux源旅程”准备项由这条阶段选择替代。
