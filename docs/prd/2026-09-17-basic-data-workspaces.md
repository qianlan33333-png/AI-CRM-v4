# 基础多维表与指标看板 PRD

## 业务判断
OneID: reads canonical customer。复用现有 Product/Order/Customer/HXC 窄 Port；不解析、建客、合并身份。
Persistence: local transaction。视图、分享授权及幂等收据与审计同一 PostgreSQL UoW；HXC 已有刷新不变。
External Effects: 不涉及新增 Provider 写入、任务或队列。

## 选型与范围
用户确认保留 Go 后端，使用 Tabulator 6.3.1（MIT）与 ECharts 6.1.0（Apache-2.0），依赖单独锁定在 web/v3/package.json；根冻结包清单保持不变。参考 https://github.com/tabulator-tables/tabulator 与 https://github.com/apache/echarts 。
两类页面提供基础筛选、排序、分组、保存视图、列显隐及指标选择。周期商品沿用 20 条件、8 排序、2 级分组、10000 候选上限；HXC 沿用字段白名单与单级分组。全量查询先执行，之后分页；汇总不使用当前页行数。
周期商品统计总人数、有效、到期、7天内到期，退款等独立为其他；HXC 展示当前三段分布，不宣称历史转化率。

## 分享与兼容
新链接独立于旧 Product 分享，绑定冻结基础条件、模式及字段。匿名附加条件与基础范围取交集。默认仅指标，可选脱敏明细；无修改与导出。令牌只存在浏览器 fragment 和请求体，库中仅哈希，日志和审计不记录凭证。撤销在读取时重新核实。
HXC 视图仅创建者可见；管理员/具有既有写角色的员工可保存；分享仅 SuperAdmin。Product 沿用既有 CanManageViews/CanShare。

## 前端复用
管理端使用现有 admin_base 单壳、pageHeaderActions 与 transport。共享 DataWorkspace 封装 Tabulator/ECharts，领域适配独立；匿名页面不加载管理端入口。冻结供体只作为合同，不修改。Product Design skill 在当前会话不可用，该环节未完成。

## 验收
检查跨页匹配、分组全量人数、到期边界、未知值、视图 CAS/幂等/回滚、分享字段白名单/范围交集/撤销、中文输入、失败重试、请求乱序与真实浏览器桌面/移动布局。编译、专项、真实 PG、浏览器、完整 CI、生产回读分别记录。

## 实现决策与复现
- HXC 明细、组人数、指标、会员等级分布在同一个 repeatable-read 只读事务中计算。缺失或被清理的版本明确失败，不返回伪零值。游标签名包含查询摘要及分享范围。
- Product、HXC 各自持有分享签名密钥与授权表，令牌由持久密钥和幂等键派生，重启重试可返回同一能力凭证。轮换先幂等撤销旧链接，再幂等签发相同配置的新链接；失败可重试，旧链接不会恢复。
- 视图保存列显隐、宽度、顺序和指标顺序，页面 URL 的 view fragment 用于重开选择；旧配置使用默认布局。查询失败保留成功数据，并显式重试失败时的原条件。
- 安装依赖：根目录执行 `npm ci`，随后 `npm ci --prefix web/v3`。CI setup 与 donor consumer 使用同样命令，冻结根包清单不变。
- 依赖审计将 ECharts 从候选 6.0.0 更新到 6.1.0；[GHSA-fgmj-fm8m-jvvx](https://github.com/advisories/GHSA-fgmj-fm8m-jvvx) 标注 6.1.0 为修复版本。图表仅启用 Bar 与 richText tooltip。许可原文位于 `web/v3/THIRD_PARTY_NOTICES.txt`。

开发验证使用独立 PostgreSQL 16 测试库，不连接生产。核心命令：
```
python3 scripts/dev_preflight.py fast
python3 scripts/dev_preflight.py compile
go test ./internal/product/... ./internal/hxcdashboard/... ./internal/platform/readshare/... ./internal/webshell/... -count=1
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 go test ./cmd/aicrm -run '^TestPostgreSQLDataWorkspaceChromiumJourney$' -count=1 -v
npx tsc -p web/v3/tsconfig.json --noEmit
node web/v3/hxcPresentation.test.mjs
```
Go 集成测试需要 `AICRM_DATABASE_URL` 指向独立测试库。桌面 Chromium 截图和局部验收不能替代 GitHub Linux 全量 CI；本次不执行生产部署或生产业务验收。
