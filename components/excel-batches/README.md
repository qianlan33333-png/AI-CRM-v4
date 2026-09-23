# Excel 群发批次组件

此目录为独立 Python 服务。上传、审核、发送采用同一个 V3 AI 助手计划；Python **没有发送、批准或绕过审核接口**。

## 边界与持久化

- Python：Excel 解析、不可变封面字节、批准时 Excel 分层快照、逐人观察和报表。单机单实例 SQLite WAL，独立持久卷；不访问 V3 业务表。批次和内容版本由 Go/PostgreSQL 持有。
- V3：现有 PostgreSQL AI 审核记录；批准、快照引用、Outbound intent、External Effects/River 在同一个事务提交。发给企微的客户身份在 worker 执行时才通过 Identity Port 解析。
- 新增 `0124_operation_excel_batch_lifecycle.sql` 迁移；原有非 Excel 私信行为保留。Excel 上传不预查员工或好友关系。原有平台代理能力开关和权限仍生效。
- 组件只监听 `127.0.0.1:8791`，使用至少 32 字符的 Bearer 密钥。浏览器只通过 V3 登录与 CSRF 保护后的接口访问，不能直接访问组件。
- 五分钟周期任务注册在现有 River，恢复后重新扫描原计划，逐页核对官方回执、更新已保存的观察；没有新增发送重试队列。只有成功回执的 `send_time` 能开始观察窗口。

## Python 输入协议

`parse_excel` 按列名读取，不依赖列顺序。必需列是 `unionid`、`话术`、`小程序 path`、`发送人 userid`、`标题`；`标题`单元格可以为空。可选列 `分层` 只允许 `A`、`B`、`C`、`D` 或空值。公式、数字 ID、重复 UnionID、非法 path 和非法分层都拒绝；非法分层返回包含 Excel 行号的 `row_errors`。

`POST /prepare` 只解析并返回 `file_digest`、`segment_source: "excel"`、`segment_column_present`、`has_segments` 和逐行 `segment`，不写入 SQLite。整份替换由 Go/ PostgreSQL 以批次 `expected_version` 负责事务和历史版本；组件只提供同一解析入口，避免产生第二个版本权威。旧 `/imports` 仅为既有本地兼容读取/测试保留，不参与 V3 新批次生命周期。

批准时 Go bridge 将最终审核行通过 `/snapshots` 送入组件。快照只接受行内 `segment`，永远不调用 `segment_sql` 或其他自动分群查询；快照写入后不可替换。没有 `segment` 字段的历史输入标记为 `segment_source: "unavailable"`，不会伪装成 Excel 分层。

## 部署准备

1. 正式发布包已包含本目录，程序跟随 `/opt/aicrm/current/components/excel-batches` 切换。安装器在已配置组件时创建 `/opt/aicrm-excel/venv`、安装依赖并创建无登录权限的 `aicrm-excel` 服务账号。
2. 以 `config.example.json` 为模板填写真实 AppID 和只读数据源。标题由 Excel 第五列“标题”提供，允许空标题导入，但该行发送失败。审核页上传统一 PNG/JPEG 封面（不超过 2 MB），未上传禁止批准；没有任何默认标题或默认封面。数据库凭据文件仅服务账号可读。SQLite 文件、`-wal`、`-shm` 必须置于持久目录；备份使用 SQLite online backup API，不能只复制运行中的主文件。
3. 使用服务单元模板启动组件。`/etc/aicrm-excel/service.env` 配置 `EXCEL_BATCH_TOKEN`；V3 API 和 worker 使用相同的 `EXCEL_BATCH_TOKEN` 和 `EXCEL_BATCH_URL=http://127.0.0.1:8791`。默认二者均为空，即禁用组件。
4. 通过唯一 V3 发布路径应用 `0124_operation_excel_batch_lifecycle.sql`、安装 Go 程序和完整 web/dist 产物；组件源文件纳入同一个校验清单，启用后随主程序发布与回退。不要执行第二套 V3 构建上传路径。
5. `AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID` 需为 UnionID 所属开放平台的 ID；现有 AI 助手 dispatch / WeCom / External Effects 授权设置维持原有门禁。启用导入不等于启用发送。

## 数据源适配及上线前核对

示例 SQL 是按已观察到的 HXC 表/字段提供的适配模板，必须在目标只读数据库核对列名、内容路由、时间语义和权限后启用，不能将模板视为生产验证记录。

- 卡片标题完全来自 Excel，不查询内容标题；封面由审核员统一上传。更换封面在同一 PostgreSQL 事务中更新整批内容版本并取消原有行批准，排除的行仍保持排除。已提交批次禁止修改。
- `user_sql` 参数 UnionID，返回唯一 `user_id`；多个结果不猜测用户。
- 分层来自 Excel 或审核行的最终值，只能是 A/B/C/D/空；不读取 `segment_sql`，也不生成 `unknown` 分组。无实际分层时报告只提供总体；部分分层时报告提供实际存在的 A/B/C/D 和 `unsegmented`。
- `lesson_opens_sql` / `case_opens_sql` 参数为用户 ID、内容 ID、窗口开始、窗口结束，返回 `opened_at`。查询结果必须覆盖指定范围，不能附加全库固定条数上限。默认识别 `pages/article/article?lesson_id=...`、`pages/case/case?case_id=...`、`pages/case-detail/case-detail?case_id=...`；其他实际路由在适配器补充前显示“暂不可统计”。禁止以任意小程序访问替代内容打开。
- `coverage_sql` 参数为内容类型（lesson/case）、内容 ID、窗口开始、窗口结束，须从现有采集进度/日志覆盖记录返回唯一 `complete=1`，证明该内容和时间段的数据完整；采集暂停或范围不足返回 0。默认 null 时全部显示“暂不可统计”，即便事件表为空也不会算零打开。不能配置无条件 `SELECT 1` 代替覆盖证据。
- 返回的时间必须为 UTC；若源表保存北京时间，SQL 需显式转换，并相应转换查询边界。日志采集暂停、保留期不覆盖窗口或源表只有部分数据时，应使适配器返回 unavailable，不得把空结果宣称为无打开。
- 打开数据每次重新读取有界的 48 小时窗口，支持晚到补算。窗口数据不完整时整体打开率为 null，保留已观察到的打开人数，不悄悄缩小约定的分母。报表内保留每行窗口状态。

观察报告的稳定 JSON 形状是：

```json
{
  "segment_source": "excel",
  "has_segments": true,
  "overall": {"12": {"sent": 1, "matured": 1, "observing": 0, "opened": 1, "unavailable": 0, "open_rate": 1.0}},
  "windows": {
    "12": {
      "overall": {"sent": 1, "matured": 1, "observing": 0, "opened": 1, "unavailable": 0, "open_rate": 1.0},
      "groups": {"A": {"sent": 1, "matured": 1, "observing": 0, "opened": 1, "unavailable": 0, "open_rate": 1.0}, "unsegmented": {"sent": 0, "matured": 0, "observing": 0, "opened": 0, "unavailable": 0, "open_rate": null}},
      "has_segments": true
    }
  },
  "rows": []
}
```

`windows` 始终包含 12、24、48 小时。`overall` 永远存在并包含所有成功送达（包括未分层）；`groups` 在 `has_segments=false` 时为空，在有部分分层时只列实际存在的分层和 `unsegmented`。`matured` 是已满窗口成功送达人数，`observing` 是未满窗口人数，`unavailable` 是已满但数据覆盖或身份不完整的人数。只要成熟分母中存在 `unavailable`，`open_rate` 就是 JSON `null`，不可当成 0。逐行 `windows` 状态为 `observing`、`opened`、`not_opened` 或 `unavailable`。`/reports/{plan_id}.csv` 对公式前缀 `=+-@` 自动加单引号。

## 运维与异常

- 页面区分“未批准、待提交、任务已创建待员工执行、发送成功、失败、结果待核实”。企微创建群发任务不代表送达。
- 人员无法匹配、员工不正确、企微限制等异常保留在原行。超时未知结果不得换 key 重发；先用原 msgid 与 sender 查询证据。仅当完整分页且唯一匹配接收用户时采信成功。
- 批次和封面不可因服务重启丢失。组件不可用时保留原审核记录，暂停资料读取和观察；已提交的 V3 任务仍按现有 worker 行为执行。
- 同一 Excel 每位 UnionID 只允许一行，避免同一批次重复触达及打开率分母歧义。跨批次重发必须在上传页明确勾选创建新批次。
- SQLite 和 PostgreSQL 必须同时备份；停用仅取消组件配置，不删除审核、回执或观察历史。

## 验证

```sh
python -m unittest discover -s components/excel-batches -v
node scripts/excel-batches-dom-test.mjs
GOCACHE=/tmp/aicrm-go-cache python3 scripts/dev_preflight.py fast
GOCACHE=/tmp/aicrm-go-cache python3 scripts/dev_preflight.py compile
# 配置独立 AICRM_DATABASE_URL 后：
bash scripts/run-go-with-donor-views.sh go test ./cmd/aicrm -run 'TestPostgreSQLExcelImport|TestAIAssistant' -count=1
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 bash scripts/run-go-with-donor-views.sh go test ./cmd/aicrm -run '^TestPostgreSQLExcelBatchesChromiumJourney$' -count=1
```

真实生产发送、实际分组数据和真实用户打开回传不属于本次本地测试证明的范围。
