# 规格、联系资料与结算 UI 完整候选

累计 base：a8cf8a0bcf68d52c1bb8a15d2df46a030ed5e702。包含本任务此前规格候选 508ab906 的全部行为及最新联系资料、三种结算 UI；旧候选由本候选替代。保留 base 的海报/1 MiB 图片/问卷附件兑换码/零元与优惠封顶行为，迁移仅追加 0241–0243。

## 本地已执行

- `go test ./internal/payment/app ./internal/order/app ./internal/coupon/app ./internal/customer/app ./internal/customer/http ./internal/survey/domain ./internal/survey/app ./cmd/migrate-customer-contacts` 通过。
- `go test ./internal/payment/http ./internal/survey/http ./internal/sidebar ./internal/product/port ./internal/order/domain ./internal/order/http ./internal/invoice ./internal/coupon/http -count=1` 通过；包内未具备环境的其他 PG 用例不算通过，下列具名数据库用例为实际 PG 16.13 执行。
- PostgreSQL：`TestPostgreSQLCustomerContactReuse`（电话与地址分组、重复号码不转移身份、整组并发、来源/资料原子回滚、合并拆分权限、问卷双模式明确映射、重放不覆盖、历史只补空）、`TestPostgreSQLVariantCheckoutTransactionsAndFrozenRecovery`、`TestPostgreSQLZeroVariantCheckoutFreezesSelection`、`TestPostgreSQLZeroCheckoutAtomicReplayOwnershipAndPaidExclusion`、`TestPostgreSQLCheckoutExpectedAmountRollsBackOrderCouponAndPayment` 均通过。
- PostgreSQL Store：`TestProductVariantsPostgreSQLAtomicCompatibilityAndVersion`、`TestServicePeriodEmptyImagesPostgreSQLSave`、`TestPostgreSQLOrderVariantSnapshotSurvivesReadsAndReplay`、`TestPostgreSQLShopProductRefreshReplayAndNewSKU` 通过。
- `AICRM_REQUIRE_CHROMIUM_JOURNEY=1` 的 `TestPostgreSQLProductVariantsChromiumJourney` 通过（真实 fixture 服务端/管理页）。未设置标志的初次 skip 不计入通过。
- Product HTTP 的规格选择/原请求重试/冻结恢复、免费领取、原订单与支付宝恢复、付款完成、地区级联、三种资料预填/晚到主动清空保护、采集模式/Provider 矩阵通过。
- TS noEmit、前端 build、OpenAPI 509 paths 校验、Orval 生成完成；保留新基线已有 web_1080 枚举。
- TS/DOM 具名旅程：contactPrefill、surveySubmissionResume、surveyAttachmentH5、surveyRedemptionH5、sidebarOneIDPresentation、productAdapter.contact_collection/save_recovery/distribution_dimensions、couponAdapter、orderAdapter、wechatShopProducts 通过。
- Product Design 完整三图同尺寸对照、窄屏和交互证据见根目录 `design-qa.md`，结论 passed。

修正过的集成问题：新 CreateCheckout 分支漏带订单规格；checkpoint 规范化漏保留规格 ID/版本；旧测试对无图片/无券占位的断言不适用于新版图标与隐藏空优惠行；进入会话确认异步等待前立即冻结选择；优惠异步返回只作用于对应所选价格。旧测试修改保留业务断言，未以跳过隐藏失败。

## 验证边界

数据库均为本机 loopback 合成 fixture，真实支付、客户认证读回、外部履约与安装后预发由唯一路由工作台独立执行。未自行部署共享环境，生产授权 false；历史资料 inspect/apply 未在真实数据运行，旧问卷来源清单需操作时明确提供。

最终机器检查范围固定在 `docs/release-checks.json`，preflight 的准确 SHA/tree、命令与结果收据随国内候选投递。Node 本机为 24.21.0，npm 11.19.0；不声称本机通过项目 pinned Node/npm 版本检查。正式国内构建使用发布环境。
