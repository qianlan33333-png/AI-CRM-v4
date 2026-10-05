# 订单统一导出验证收据

基线：`641d75779c46dc32b7db05d057580dd2f7a4d849`，独立分支 `codex/order-export-unified`。候选确切 head/tree 以本文件所在提交与工作台投递为准。环境：macOS、Go1.26.6、PostgreSQL16.13、Node24.21.0/npm11.19；Node与仓库锁定24.18.0/11.12.1不同，正式国内构建由工作台使用其固定环境。数据库仅新建回环 `aicrm_test_order_export_20261005`，测试自身隔离 schema/克隆库并清理。

## 按影响检查

- `go test ./internal/order/app ./internal/order/http -count=1` 通过；最后编码器/合同补充后 `go test ./internal/order/app -run '^TestExport' -count=1` 与 HTTP `-run 'TestExport|TestWechatExport'` 通过。覆盖固定27列、多商品、订单快照优先、OneID缺号回退、无联系方式、CSV公式防护、文本长编号/前导零、上海时间、未知时间空白、优惠后冻结单价、成功退款、历史事实、收据稳定重放/格式冲突、读取解密失败、空结果、10000明细行与5MiB拒绝、权限/CSRF/身份冲突与错误、微信兼容入口范围。
- 设置本地 `AICRM_DATABASE_URL` 后 `go test ./internal/order/store ./internal/distribution/store ./internal/payment/store ./internal/crmcli -count=1` 通过。初次多包并发建迁移发生一个 PostgreSQL `tuple concurrently updated` 环境冲突，失败用例单独重跑通过，随后 Payment Store 整包顺序运行通过。Order/Distribution/CLI整包第一次即通过。
- 同环境 `go test ./cmd/aicrm -run '^TestPostgreSQLUnifiedOrderExportHTTPAndCLI$' -count=1 -v` 通过：真实Composition + Access登录 + CSRF + Order/Identity/Customer/Payment/Distribution所属Port + PostgreSQL，三种联系信息混合导出。付款人区别受益人，冻结异常收件人/地址保留，实际支付参考号与确认时间、真实分销公开编号、部分退款和缺失时间验证。跨页筛选/默认所有状态、CSV同映射、受保护0600 CLI文件可打开、幂等收据重放、加密身份不可读时503且不增加收据。
- `go test ./internal/config/http -run '^TestOpenAPIDownloadIsAuthenticatedAndMatchesTheReleaseSource$' -count=1` 通过；`node scripts/validate-openapi.mjs` 通过（487 paths，46 active operations，160 mux registrations；既有格式警告仍在）。`go run ./cmd/generate-cli-catalog` 生成549项，未改变 operationId。当前V4 API绑定摘要/字节数同步，`node scripts/prepare-donor-source-views.mjs` 验证231 bindings/230已准备视图，未取旧仓字节。
- `node web/v3/orderAdapter.test.mjs` 通过：真实Host的现有订单/退款旅程，加身份/状态/商品/单号/日期筛选、跨页、不静默转全局、订单与商品行计数、加载/空结果/超限/身份失败/无权限、真实绑定标记与XLSX文件名/类型。
- `node web/scripts/build.mjs` + `node scripts/build-v3-host-adapters.mjs` 构建通过。后者要求先重建基础文档；曾直接重复运行被已有注入检查拒绝，随后正常完整两步构建通过。没有安装共享环境。
- CUA 实际构建页面与基线同状态组合比较，见项目根 `design-qa.md`。修复错误能力toast与提示挤压。Chrome实际下载空结果XLSX，openpyxl独立读回1行27列。浏览器download事件工具超时；实际文件验证通过，未把事件超时当成下载失败或成功凭证。
- 生产三个异常订单：使用先前只读 `diagnosis-source.json` 在本地临时测试中送入本次映射与XLSX编码器；逐字段验证线上单号、买家昵称、支付参考、手机、收件人、省市区、详情地址、商品与上海时间，3/3通过。测试、源数据和PII没有提交，生产订单没有修正。

## 五项影响及边界

1. 对外合同：默认XLSX，CSV共用27列完整数据；预览增加order_count/row_count。旧客户端若依赖旧列名或分单位金额须按新合同接入，已有CSV请求格式可继续使用。
2. 业务机制：逐商品展开，固定列缺失留空；完整收货数据只由现有管理员导出权限进入下载文件，摘要收据不保存联系方式。读取与同事务收据使用Repeatable Read；失败不伪装缺失，不截断。
3. 模块：Order导出映射/收据，Identity canonical与联系方式读取，Customer名称，Payment确认时间/参考号窄Port，Distribution商品行/公开编号窄读取，Composition桥接；无新增Provider、队列或迁移。订单收件人不回填昵称。
4. 页面：沿用筛选与列表，仅合并导出入口为“导出订单 XLSX”，显示订单数/商品明细数与下载状态。
5. 验证：上述局部检查与本地合成组合旅程通过；没有声称完整回归。

未验证：共享预发已安装包、正式固定Node/Linux构建、生产认证下载/独立读回、真实支付/扫码/Provider/外部对接/实际发货。旧历史订单Chromium旅程金额断言已按本次人民币小数合同修订，其整条迁移+浏览器旅程本次未再运行（历史映射已由Order合同与真实Host现有旅程覆盖）。

授权：用户“PLEASE IMPLEMENT THIS PLAN”授权本能力开发、检查、国内候选推送与唯一发布工作台交付；本任务没有“部署到生产机”命令，不自行安装共享预发或生产。工作台应沿用原队列/锁/attempt，最终同包阶段核验默认XLSX/27列、商品筛选跨页、手机号解析、真实管理员下载与缺失留空。没有历史收货修正或自动外传。
