# 统一客户档案入口并下线旧页面

用户授权：2026-10-05「按照你说的完成修复……不用兼容旧的，直接用新的。旧的页面直接下线」。复用上一轮生产诊断；本候选只修复档案入口和历史订单回链，不改变客户归属。原候选 base：`94d4b845f9d9ddcc30e7e05432032ba1677fac5a`；2026-10-08 沿国内准确 main `26f8546e147afc6506023df8c5d03ee9939b8274` 重提同一缺陷，不扩能力。

```mermaid
flowchart TD
 A[订单或历史记录中的客户链接] --> B[现有 canonical Customer ID]
 C[用户列表查询] --> B
 B --> D[/admin/customers/ID 正式档案]
 D --> E[历史订单 查看详情 带 external_history]
 E --> A
 F[直接访问旧 customerDetail.html] --> G[410 Gone 不跳转 不提供旧档案]
```

业务规则：新入口保留已有权限、身份解析和无数据状态；所有客户链接保留原 canonical ID。旧档案页面不构建、不挂载，不保留兼容跳转；即使残留旧 dist 文件也不能被服务。其他后台页面仍保留既有行为。

参考与复用：上一轮 HubSpot 官方记录身份/合并追溯调研不支持按姓名合并；本次下线语义参照 [RFC 9110 §15.5.11](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.5.11) 的永久不可用 410。复用 V4 `admin_base`、`admin_customers.html`、现有表格的 `admin-button--ghost` 与 Order Host，不引入新框架。冻结 donor 证据不修改；只修改 V4 自有页面/section、发布装配和服务路由。

OneID：只读取 canonical Customer，并生成对应路由；不新增匹配、建客或合并。Persistence：无数据库写入或迁移。External Effects：不涉及 Provider 读写、内部持久任务或队列。不涉及新增限制。

Product Design：index/user-context preflight、get-context、audit/image-to-code；视觉目标是用户已选择的正式档案与现有订单按钮。生产浏览器会话失效，当前代码和隔离样例用于同布局本地截图/交互核查；不把样例称为真实业务验收。

五项影响：对外合同为旧页 410、新链接与订单回链；业务机制不变；关联消费者为 Order Host、自有历史 section、客户列表、webshell 和前端构建；页面保留正式档案布局；证据选取 webshell 小包测试、正式客户脚本、订单 adapter、直接历史消费者及构建产物合同。

验收：旧 URL 的 GET/HEAD（含 query）不返回档案或 Location；新档案可正常渲染，同一 ID 从订单和列表进入一致；external_history 的订单详情携带正确 Provider；已知/未知 Provider 不互相误路由；发布包不含旧档案 HTML。预发在最终安装包完成“客户列表→正式档案→历史订单→正式档案”旅程并读取旧入口状态。身份/鉴权沿用现有逻辑；无迁移、无真实支付或 Provider 写。

回退：原发布流程恢复前一应用包；无数据回滚。国内候选推送后向唯一路由工作台投递准确 base/head/tree/ref、测试和未验证项。用户原消息授权修复和下线旧页。2026-10-08 工作台传达其用户最新命令「所有本地完成最终上线部署」覆盖本项生产晋级；工作台绑定原人类授权及准确构件，开发任务仅交付候选，不安装共享预发或生产。

|参考页面|复用组件|公共扩展/新增|受影响调用|Product Design 结果|证据|
|---|---|---|---|---|---|
|当前正式客户档案与历史交易详情|admin_base、正式档案订单表、admin-button--ghost、Order Host|无|客户列表、交易详情、Campaign/HXC/周期历史记录|audit 确认入口分裂及历史订单操作缺失；image-to-code 保留布局完成入口修复|仓外 design/02、03 同视口截图；04 订单回链截图；本地样例点击档案→历史订单→同一档案|

验证边界：本地浏览器为隔离合成数据；所用通用预览壳有未提供的图标和日期资源，不能据此宣称生产视觉/Provider 验收。正式客户脚本更新既有资源版本号，避免一小时缓存继续使用旧按钮逻辑。安装后的认证往返和旧入口 410 已加入现有 external-order-history 旅程，由工作台在最终同包预发执行。

发布清单使用 `customer-profile-entry-checks.mjs` 复用上述六个现有合同。原因：干净候选没有dist，四个页面合同需要构建HTML和标签组件字节；入口先用既有build/Host装配命令准备一次，再执行合同。不修改发布器、缓存、队列或门禁。

本次重提只有检查清单的累计基线冲突；采用当前候选相关清单，已上线发票工具清单不重复带入。业务/前端源代码自动合并后按准确diff复核，保留最新 Order Host 与 admin_base 的其他改动。复用原 Product Design 证据 `/Users/qianlan/Downloads/新CRM/release-control/customer-profile-new-only/design/`，没有新增布局或视觉方案。

重提交验证选择：复用e2e既有客户列表、周期历史、Campaign历史/目标人员的具名页面块（--customer-profile-links），不运行无关导出/H5/侧边栏。最新main的订单导出按钮已更换，旧全页面断言不再匹配；完整V3类型检查在未修改的automationContentHost/sidebar出现基线错误。两项如实交接，不在此缺陷中扩大修复；本候选受影响的TS单独校验。

安装检查接续：工作台 Linux 的全历史 `test-install-release-ordering.sh` 失败，最终日志为硬编码 `/tmp` 的完整包夹具创建时 gzip/tar 报 `No space left on device`，不记通过。本次安装器准确 diff 仅删除 `customerDetail` 页面必需项；相关 preflight 改用 `test_customer_profile_install_contract.py`，读取并执行真实安装器的 admin 文档检查循环，验证旧页不要求、新客户列表和订单详情仍必需、缺任一时拒绝，另检安装器 shell 语法。新合同使用遵守 TMPDIR 的临时空文档，不生成完整 tar 包。原全历史脚本保留给明确维护，不改安装器运行行为。最终同包预发安装与认证旅程继续由工作台执行；已通过且输入不变的页面47项与六项后端证据复用。此接续仅修改检查及其说明，不涉及 OneID、持久化或外部效果，没有新增业务限制。

发现入口接续：带连字符的测试文件名在受控 Python3.14 unittest discover 下产生 Ran0/NO TESTS RAN（exit5），原直接执行4项不算 Linux 发现通过。重命名为可导入的下划线文件名，并按发布器准确命令 `python3 -m unittest discover -s scripts -p test_customer_profile_install_contract.py` 执行验证。合同内容和应用源码均未改变，继续复用原页面/后端证据。
