# Excel batch implementation contract

OneID: resolves scoped UnionID at execution via Identity Port; no provisioning, linking or inferred ownership. The independent Python component accepts raw UnionID; only the V3 dispatch adapter resolves it.
Persistence: AI Assistant owns review/content history and atomic Outbound/External Effects acceptance in PostgreSQL. Python owns import receipts, immutable cover blobs, classification snapshots and observation results in its own database. Provider reads may retry; Provider writes use the existing Outbound runtime only.

Explicit user exception: deployable independent Python component, no forced Go implementation or canonical IDs at the import boundary. No second sender, identity matcher or effect retry kernel.

Five text columns: unionid, 话术, 小程序 path, 发送人 userid, 标题. Titles come exclusively from Excel; missing titles fail the row at execution. A batch-wide cover must be uploaded in review before approval; no fallback title or cover. One batch maps to one existing AI Assistant review plan. No upload-age gate. Review approval queues official WeCom group-message tasks; employees still execute them in WeCom. Task acceptance is never delivery proof.

Observation: cumulative 12/24/48 hours from each recipient's provider-reported send time, frozen A/B/C/D or unknown, distinct user opens of the corresponding content, complete source coverage required before recording zero. Late data is recalculated. GitHub publication and production deployment are authorized by the follow-up request. Real sends still require a separately reviewed batch.

## 本地交付记录（2026-09-09）

基于远端 main `ae536c5` 的隔离分支 `codex/excel-operation-batches`。此次只做开发与本地验证，没有部署、启用生产配置或创建真实企微任务。

### 已实现的入口和状态

- 运营闭环：五列 XLSX 上传、相同文件提示原批次、明确勾选创建新批次、批次列表、查看执行与效果、12/24/48 小时及逐人报告下载。
- AI 助手：同一原生审核计划，逐行文字和卡片预览，修改话术/标题/path、上传统一封面，排除/恢复，一次批准并持久化发送意图，审批后冻结内容。AppID 固定为导入配置值，不能通过内容修改接口替换。
- 执行：沿用逐用户官方群发出口；提交时解析可信 UnionID，使用 Excel 中的员工 userid；不预查员工负责人或好友关系，不猜测、不建客。
- 回执：保存 msgid、发送员工和目标客户，读取完整分页并唯一匹配；只有客户级成功回执和实际 send_time 才进入成功计数。已创建任务、待员工执行、明确失败、未知结果分别显示。
- 观察：批准时冻结现有分类结果；按每人的实际发送时间计算成熟窗口。日志源必须提供完整覆盖证据，缺失时显示暂不可统计；晚到数据会补算。

### 验证结果

| 检查 | 结果与范围 |
|---|---|
| Python 组件 | 7 项通过：真实 HTTP 鉴权和导入、重启去重、新批次、文本与公式校验、空标题保留与显式封面上传、窗口与重复/晚到打开、缺失日志覆盖 |
| PostgreSQL 集成 | 通过：原生导入去重、执行时身份解析、排除、一次批准及重复请求、同事务意图/任务/快照、固定 AppID、回执目标匹配和成功投影 |
| 受影响 Go 领域测试 | AI 助手、Outbound、企微 Adapter、配置领域测试已通过；追加回执分页和写入失败未知结果测试通过 |
| DOM 页面交互 | 通过：排除/恢复、单次批准、文本安全展示、运营侧查看执行与效果且不出现批准按钮 |
| 静态和构建 | Go 全仓编译、fast 边界/格式/冻结供体检查、TypeScript 检查、前端构建、374 路径 OpenAPI 校验、Orval 一致性检查通过 |
| Chromium 完整用户旅程 | **未通过**：本机 macOS 沙箱拒绝 Chromium MachPortRendezvousServer 注册，浏览器未启动；不能将 DOM 检查当作浏览器验收 |
| 生产、真实企微与真实打开数据 | **未执行、未验证** |

本机 Node 为 25.9，未声称通过仓库要求的 Node 24.18/npm 11.12.1 工具链门禁；需在标准 CI 环境完成该门禁和 Chromium 验收。冻结供体文件保持不变，新前端逻辑位于 V3 adapter；OpenAPI 索引只更新当前 V3 所属绑定。

### 尚需提供的上线配置

1. 固定小程序 AppID、组件共享密钥、UnionID 所属开放平台 scope。
2. 用户对应关系和打开日志的只读数据源及实际 path 路由；示例 SQL 需核对生产字段和 UTC 时间语义。
3. **现有 A/B/C/D 分类投影或查询规则**。本次可读资料未找到完整规则，`segment_sql` 默认空，界面明确显示“未知”。未重造分类规则。
4. **打开日志覆盖/采集进度来源**。`coverage_sql` 默认空，不能把无记录当作零打开；配置真实覆盖证据后才输出打开率。
5. 生产部署 SHA、企微接口权限、客户级回执字段和真实数据回传需要部署前核对。所有 Provider 写仍受现有能力和授权门禁控制。

### 复核命令

```sh
python -m unittest discover -s components/excel-batches -v
node scripts/excel-batches-dom-test.mjs
npm run typecheck
npm run build
node scripts/validate-openapi.mjs
npm run orval:check
python3 scripts/dev_preflight.py fast
python3 scripts/dev_preflight.py compile
# 使用独立测试 PostgreSQL 的 AICRM_DATABASE_URL；不会连接真实企微。
bash scripts/run-go-with-donor-views.sh go test ./cmd/aicrm -run 'Test.*(Excel|AIAssistant)' -count=1
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 bash scripts/run-go-with-donor-views.sh go test ./cmd/aicrm -run '^TestPostgreSQLExcelBatchesChromiumJourney$' -count=1
```

组件安装配置、持久卷/备份和异常恢复见 `components/excel-batches/README.md`。部署与真实发送应另行安排。
