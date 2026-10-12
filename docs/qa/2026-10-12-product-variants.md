# 普通商品规格实现与验证收据

基线：`8245526b89d04111acb2b03f66416b9c10d4064e`。分支：`codex/product-variants`。原任务：`01a12e77-4c1c-7891-9d87-ada185685b8a`。准确候选 head/tree 随提交后的工作台交接消息提供。

实现仅增加 Product 规格子表、Checkout 可选选择字段及 Order 明细快照。金额仍由服务端读取规格价格并进入已有优惠/结账快照；支付、退款、父商品已购规则、身份和外推/履约复用原能力。

## 直接影响与兼容性

| 项目 | 证据/结果 |
| --- | --- |
| 对外合同 | Product 增加可选开关/列表；Checkout 增加可选 ID/版本；受保护订单/恢复接口提供规格。旧命令新字段省略，不改变旧摘要格式。 |
| 业务机制 | 所选规格版本独立递增；全商品配置仍用父版本 CAS；同一 UoW 保存订单、收款任务、收据。编辑其他规格不阻断当前选择。 |
| 直接模块 | Product、Order、Payment、Coupon；Invoice 仅我的订单展示与原单继续付款 DTO；侧边栏仅订单名称。 |
| 页面 | 嵌入普通商品售卖信息；既有付款页选择；后台/我的订单/侧边栏显示规格；导出仍固定 27 列。 |
| 数据变更 | `0237` 新增可选模式和规格子表；`0238` 订单明细新增 nullable JSON 快照。引用 ID、版本和内容仅保存在这一份规格快照中；原价/优惠/应付仍用原结账字段。没有历史回填。 |

OneID 继续使用可信会话，未引入身份匹配。未新增 Provider、任务队列、独立规格管理页、库存、人数、权益倍数、规格级分销/履约或微信小店映射。现有微信小店一 SKU 对应一个 CRM 商品的绑定保持。

## 已执行检查

本机 macOS，PostgreSQL 16.13，Node 24.21.0 / npm 11.19.0。前端锁文件要求 24.18 / 11.12.1，本机存在 engine 提示；以下为实际本机结果，不代替发布工作台的 Linux 工具链验证。PG 使用本机临时 schema/隔离测试库，无生产数据库连接。

| 检查 | 已观察结果 |
| --- | --- |
| Product PG `TestProductVariantsPostgreSQLAtomicCompatibilityAndVersion` | 创建/重放、旧客户端省略字段保留、复制新 ID、其他规格改价不影响当前版本、跨商品 ID 引发整体回滚、并发 CAS 恰一成功、移除保留记录、关闭后明确单价通过。 |
| Order PG `TestPostgreSQLOrderVariantSnapshotSurvivesReadsAndReplay` | 快照写入、查询、重放与副本隔离通过。 |
| 组合 PG `TestPostgreSQLVariantCheckoutTransactionsAndFrozenRecovery` | 缺选/过期/未知规格拒绝且无付款记录；服务器价格与数量 1；改规格重用键拒绝；移除后精确重放及受保护原单读回；重复合成回调只留一个付款事件；付款后父商品拦截新规格购买通过。 |
| `TestVariantCheckoutPriceSelectionAndReplay` | 归属/版本/模式/移除拒绝、忽略父商品旧价、目录变化后原请求重放、同键内容冲突和缺省摘要兼容通过。 |
| Coupon `TestVariantCouponAcceptsAnyEligiblePrice` + `couponAdapter.test.mjs` | ¥99/¥999 配置允许 ¥100 券；后台显示价格区间；H5 选择低价不显示该券、高价按 ¥899 计算通过。 |
| `TestPublicVariantSelectionCouponAndRecovery` | 单选自动、多选必选、空列表停止、网络未知保留原键/原规格；关闭模式仍可重试；原单冻结名称/金额；过期允许重新下单后恢复完整选项通过。 |
| `TestPublicOriginalCheckoutBrowserJourney`、`TestPublicAlipayOriginalOrderBrowserJourney`、`TestPublicPaymentCompletionRefreshJourney` | 原有普通/周期商品订单恢复、微信/支付宝路径和付款结果页回归通过。 |
| Chromium `TestPostgreSQLProductVariantsChromiumJourney` | 真实管理员登录、真实界面编辑保存、PG 读回两个稳定 ID、刷新后内容和价格区间、空名称反馈且不保存通过。 |
| `TestExportXLSXPreservesFullFactsBlankContactsAndEveryItem` 与相关导出测试 | 规格追加商品名；列数仍 27；订单/优惠/退款字段不变。 |
| `TestPostgreSQLShopProductRefreshReplayAndNewSKU` | 既有小店刷新、重放、新 SKU 绑定通过。 |
| Product/Order store 包 | 已执行整包；周期商品旧最小迁移夹具缺新列，补齐 `0235/0237` 后单独重跑失败用例通过；其余用例通过。 |
| 相关 Go 包 | Product app/port、Order domain/app/http、Payment app、Coupon app、Invoice 通过；Product/Payment/Coupon HTTP 首轮因 Node 准备并行导致模块缺失的用例均在准备完成后重跑通过。Product 旧推广 URL 断言补齐基线既有 resume 分支后通过。没有把首轮失败算通过。 |
| 前端 | Product contact_collection/save_recovery/distribution_dimensions、Coupon、Order、WeChat Shop 适配器测试通过；`npm run typecheck` 通过。 |
| 静态/设计 | `git diff --check`、脚本 shell 语法通过；Product Design 视觉复核见根目录 `design-qa.md` 和 `docs/design/product-variants/`。 |

具名发布清单在 `docs/release-checks.json`，只含直接消费者。首轮依赖准备与使用并行的问题已消除；后续通过 `AICRM_TEST_PREP_DIR` 复用准备结果，环境准备和依赖测试顺序执行。

可复现的关键命令：

```sh
go test ./internal/payment/app -run '^TestVariantCheckoutPriceSelectionAndReplay$' -count=1
go test ./internal/coupon/app -run '^TestVariantCouponAcceptsAnyEligiblePrice$' -count=1
# 需设置指向本机隔离测试库的 AICRM_DATABASE_URL
go test ./internal/product/store -run '^TestProductVariantsPostgreSQLAtomicCompatibilityAndVersion$' -count=1
go test ./internal/order/store -run '^TestPostgreSQLOrderVariantSnapshotSurvivesReadsAndReplay$' -count=1
go test ./cmd/aicrm -run '^TestPostgreSQLVariantCheckoutTransactionsAndFrozenRecovery$' -count=1
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 go test ./cmd/aicrm -run '^TestPostgreSQLProductVariantsChromiumJourney$' -count=1
go test ./internal/product/http -run '^TestPublicVariantSelectionCouponAndRecovery$' -count=1
node web/v3/couponAdapter.test.mjs
npm run typecheck
```

## 发布边界

仅开发及推送候选获授权。本任务不安装共享预发/生产。发布工作台使用现有队列、锁和同包流程，`installed_checks` 的 `product-variants` 对应 `scripts/run-installed-product-variants.sh`，沿用既有安装态 Product 夹具、精确二进制/资源 SHA 校验和隔离库。

未验证：Linux 构建、安装态同包旅程、生产读回、真实微信/支付宝付款/退款、外推 Provider 结果及真实权益履约。合成可信回调只证明本地幂等与记账，不证明 Provider 调用。两项增量迁移进入生产前按既有规则备份数据库；不回写历史订单，不宣称逆向 schema 回滚。
