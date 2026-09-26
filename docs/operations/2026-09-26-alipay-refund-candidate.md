# 支付宝退款候选

国内分支 `codex/alipay-refund-984-20260928`；基线 `9845909a239c664a504a24e7652601e513b6a6ae`。父 PRD：`docs/prd/2026-09-26-alipay-refund.md`。准确 head/tree 随国内分支和独立测试证据交付，不改 main，不直接安装共享预发或生产。

复用现有退款页和持久幂等恢复：支付宝交易号确认 → `/api/admin/alipay/orders/{order_ref}/refunds` → 原 Payment 退款事务和 External Effects → SDK 官方退款 → River 按原订单/退款号查询 → 明确 REFUND_SUCCESS 且金额/交易匹配后结算一次。支付宝列表、恢复、外部处理记录保持 provider/order 双条件。10000 或排队成功不代表退款完成；未知结果不创建新键。没有新增数据库迁移。

修改 OpenAPI 同时更新 canonical source-index/source-lock 摘要及 embed 消费视图。现有冻结前端供体没有修改。

验证使用本机独立 PostgreSQL 16 `aicrm_test_alipay_refund_20260926`，测试各自建立并清理隔离 schema，无真实 Provider 或生产数据。命令：

- `python3 scripts/dev_preflight.py fast` 和 `compile`。
- `AICRM_DATABASE_URL=postgresql://qianlan@127.0.0.1:5432/aicrm_test_alipay_refund_20260926 go test -json -p 1 -count=1 ./internal/payment/... ./internal/order/... ./internal/externaleffects/...`。
- `node web/v3/orderAdapter.test.mjs`、`npm run typecheck --silent`、`npm run build --silent`、`make orval-check`、`node scripts/validate-openapi.mjs`。

覆盖官方请求双键、签名篡改、成功码、订单/交易/退款号/金额错配、无成功状态保持待核对、渠道开关与权限、并发申请、原键重放、部分退款余款、晚到回调及重复查询单次结算、已有退款页渠道和持久恢复。测试 JSON 与校验日志位于 `/tmp/aicrm-alipay-refund-evidence/`，源码身份另存 `source.json`。

本地 Node/npm 为 24.21.0/11.19.0，与发布固定版本 24.18.0/11.12.1 有差异；本地证据仅证明所列范围，不作为完整 Linux/预发安装验收。预发受影响业务合同、生产同包晋级及真实支付宝资金退款仍未执行，交由串行发布者记录独立证据。回退应用不删除退款状态和原键，不能借回退重复发起退款。

## 对照指挥台后的部署交接

2026-09-28 从国内 remote 实际 fetch 核对基线为上述 SHA。仅重放原退款提交，未带入其他维护或 bench 候选。旧 `523b7da` 及其失败/测试证据保留，但不作为新候选通过依据。新证据目录 `/tmp/aicrm-alipay-refund-evidence/984-20260928/`，准确 head/tree 另存 `source.json`。

推国内新分支后实际投递 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json` 指定的唯一指挥台，原任务 ID `01a0ddc4-efcf-7da1-b947-e9322f59f597`，保留退款原顺序第 1。开发任务不自行调用 release/poll 或安装共享预发/生产。

现行入口负责候选检查、预发构建与安装、受影响业务合同、完整源码备份、同包生产晋级、文件摘要/进程/readyz/适用业务读回，成功后推进 main 和收据。不能只凭 release 排队成功判定部署完成；失败按其收据继续处理，不另起第二条发布路径。

本候选无数据库迁移，不新增常规数据库备份步骤。技术部署完成后，真实支付宝退款仍须用明确授权的订单和金额单独验收，核对原退款键、支付宝结果、订单及佣金状态；不自动发起真实退款。
