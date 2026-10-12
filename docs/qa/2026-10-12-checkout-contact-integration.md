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


## cc16 后的安装态验收补齐

沿 cc16 线性追加，base 仍为 a8cf8a0b；本次只改测试、检查清单和来源元数据，不改变业务/UI/迁移。

- 原 11 个前端合同保留全部断言；productAdapter.contact_collection、contactPrefill、sidebarOneIDPresentation 三项移到 `checkout-contact-dist` 安装检查，读取 `AICRM_INSTALLED_PRODUCT_ROOT/web/dist`。其余 8 项仍在源码清单。此 DOM 合同使用源码测试 harness 和最终包文档/manifest Host/Overlay，不宣称它独立完成安装包 HTTP 验收。
- 规格 Chromium 移出源码阶段，只登记 `product-variants` 最终包检查；安装模式实际启动指定 binary 并经 TLS proxy 发请求，不再只校验 binary 后继续访问源码 handler。
- `customer-contact-reuse` 复用标准四个 `AICRM_INSTALLED_PRODUCT_*` 输入与现有隔离 fixture，原五组 PostgreSQL 用例全部保留。设置安装输入时追加 `installed_http`：真实 HTTP 下单→预填，整页手机号题/逐题收货题提交→另一问卷与下一结账会话预填；未映射推荐人不覆盖；匿名/跨客户/普通员工拒绝或无完整资料；管理员 CSRF reveal 与无原始资料审计；订单和答卷重放不覆盖较新资料；数据库注入资料写失败时答卷/资料/收据全部回滚；三种采集模式仅返回指定信息。
- 安装态成功标记为 `installed_customer_contact_reuse_http: PASS`，包含 source SHA、binary SHA256、`stopped_process=true`、`provider_attempts=0`，仅在既有进程退出检查成功后输出。无安装输入时只有源码合同，不输出此标记。维护 inspect/apply 不运行。
- 复用 Survey.DataKey，没有新增运行时配置键。OpenAPI 来源提交修正为 V4 `310af8ee5d5c2ef51d2ff87a1fc98e1a5c2cce44`；确认该提交与当前 API blob 同为 `50baf5ebf00addd45e1f3718dd60658c9a6b3009`，API 字节未变，231 个来源绑定/230 个派生视图校验通过。

本机补测：macOS / PG 16.13，完整 `TestPostgreSQLCustomerContactReuse` 六组通过（含上述安装 HTTP 子用例）；`TestPostgreSQLProductVariantsChromiumJourney` 实际 binary 模式通过；三项 dist DOM 合同通过，shell 语法及 diff 检查通过。使用 cc16 未变业务源码本机编译的二进制（SHA256 `f9d267598ad6b1ce9faf555951c31c5b5df06dadaa0819b270b5f854e95a2a84`）和既有 cc16 Host，未再次构建 source Host。新增测试夹具最初的 actor/排序/201 状态/已消费会话预填假设已按现有合同修正；未修改运行时来适配测试。

这些是本机入口验证，不代表新候选最终 Linux 构件通过。新 head 的准确 Linux binary/Host、pinned Node/npm 和最终同包预发旅程仍交给唯一工作台；原有有效结果复用，production=false/open。
