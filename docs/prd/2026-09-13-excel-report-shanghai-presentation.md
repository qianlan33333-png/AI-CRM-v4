# PRD：Excel 逐人报告的上海时间与中文业务结论

## 1. 目标与范围

运营人员从 Excel 批次“发送效果与复盘”下载“逐人报告”时，`sent_at` 当前直接导出为 RFC3339/UTC；同页批次行和逐人回执也直接显示该值。将这些业务可见时间统一为 `Asia/Shanghai` 的 `YYYY-MM-DD HH:mm:ss`，不显示 `T`、`Z`、毫秒或时区后缀。

CSV 同时将发送状态和 12/24/48 小时观察结论显示为中文业务文案。保留 `id`、`unionid`、`sender_userid`、`path` 等原有可定位字段及 CSV 公式注入防护。

本 PR 只处理：

- `components/excel-batches/batches.py` 生成的 `/reports/{plan_id}.csv`；
- `web/v3/excelBatches.ts` 中批次行及逐人回执的 `sent_at` 展示。

不改变 JSON/API 枚举、SQLite 观察记录、UTC 观察窗口计算、Go Bridge 的 CSV 字节透传，或既有发送/回执逻辑。

## 2. 前置判断

- **OneID：不涉及。** 本能力不解析、匹配、创建或变更客户身份；报告保留既有不透明 `unionid` 字段。
- **持久化：既有持久化只读。** CSV 从组件既有 SQLite observation 读取，页面从既有只读接口读取；不新增表、迁移、事务或任务。
- **Provider / External Effects：不涉及新增效果。** 不新增读取或写入 Provider，也不触碰 Outbound、External Effects、River、回执或幂等键。

## 3. 已核对的参考与取舍

- 同仓 [PR #218](https://github.com/qianlan33333-png/AI-CRM-v3/pull/218) 固定了“仅真实成功送达时间启动观察窗口”、组件随 V3 同包发布以及 CSV 公式注入防护。本次复用这些边界，不将任务创建或未知结果伪装成送达。
- 同仓 [PR #223](https://github.com/qianlan33333-png/AI-CRM-v3/pull/223) 明确 Python 组件拥有观察与报表、Go Bridge 负责受控转发。本次在 Python 报表 Owner 格式化 CSV；不在 Bridge 改写字节。
- 已合并的 V3 `web/v3/adminDateTime.ts` 是业务展示的统一上海时间 helper。Host 复用它，避免依赖浏览器本地时区或另建 formatter。

## 4. 行为合同

1. `sent_at` 为带时区的真实送达 instant 时，CSV 和 Host 显示上海时间到秒；例如 `2026-09-30T16:00:00.611265Z` 显示 `2026-10-01 00:00:00`。
2. `sent_at` 缺失保持空单元格（CSV）或显示 `—`（Host）；无效值不得原样泄露，CSV 显示“时间暂时无法显示”，Host 以既有安全占位文案表示。
3. CSV 发送状态使用：待提交、任务已创建，待员工执行、发送成功、明确失败、结果待核实；未知状态为“状态待核对”。观察窗口使用：观察中、已打开、未打开、暂不可统计；未知值为“状态待核对”。不改变组件存储的机器枚举。
4. 公式前缀 `=+-@` 的 CSV 字段继续加单引号；不删除或替换现有机器标识列。
5. Host 的批次行和逐人回执只使用 `formatShanghaiDateTime` 展示时间；状态沿用领域映射，未知状态保留安全中文 fallback。当前 Bridge 的 `failure_reason` 只来自 Outbound 回执的受控失败码：标题/封面、身份、接收对象、内容、结果待核对、企微拒绝和已知限频分别映射为固定中文。未知、混合文本及未登记 Provider 码一律显示“失败原因待核对”，不透传原文。人工审核备注是独立命令/审计字段，不在当前逐人发送回执投影中；本次不改变该字段。

## 5. 验收

- Python 组件测试覆盖 UTC→上海跨日/月、微秒截断、无值、无效时间、中文状态/窗口和公式注入。
- Host DOM 测试同时覆盖批次行与逐人回执的上海时间、中文状态、已知失败码映射，以及混合机器文本的安全 fallback。
- `web/v3/adminDateTime.test.mjs` 在 UTC 和另一浏览器时区下继续通过。
- 不修改 Go Bridge、观察 SQLite 原始字段、API/OpenAPI、Provider 调用和生产数据。
