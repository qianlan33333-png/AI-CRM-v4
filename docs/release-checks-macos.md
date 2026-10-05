# 本批 macOS WebKit 检查

本批基线是累计 tip `c58b0a7cf71c0410dde557c2a0f4bbb6af723bc2`。Linux 执行 `docs/release-checks.json` 的未执行检查；以下两个检查在已具备 WebKit 15.4 / 26.0 的 macOS 环境运行并以准确候选收据交付工作台。它们不再列入缺少这些夹具的 Linux 短清单，也不把跳过当通过。没有修改发布器或清单解析器。

| 平台及实际环境 | 具名检查 | 数据与浏览器断言 |
| --- | --- | --- |
| macOS / PostgreSQL / WebKit 15.4 和 26.0 | `TestPostgreSQLPublicSurveyOtherWebKitRecovery` | 两种呈现方式；Other 输入、刷新恢复、提交已落库但响应丢失后的原键恢复；一份提交、九份答案、一份 claim |
| macOS / PostgreSQL / WebKit 15.4 和 26.0 | `TestPostgreSQLPublicSurveyDefinitionChangeWebKitRecovery` | 两种呈现方式；实际后台更新发布后真实 409、旧答案不映射到新题、新定义归属与保护字段读回 |

两项定义均在 `cmd/aicrm/public_survey_other_webkit_postgres_test.go`，驱动为 `cmd/aicrm/public_survey_other_webkit.mjs`。驱动实际断言 `browser.version()` 等于 15.4 / 26.0；OAuth 使用既有 loopback Provider 夹具，无真实客户效果。

现有 macOS 夹具（不安装新浏览器）对应命令：

```sh
env AICRM_REQUIRE_SURVEY_WEBKIT=1 \
  AICRM_PHASE2_OLD_PLAYWRIGHT_ROOT=/private/tmp/crm-survey-180-investigation-20261003/browser-legacy \
  AICRM_PHASE2_OLD_WEBKIT_DRIVER=/private/tmp/crm-survey-180-investigation-20261003/browser-legacy/webkit-1578/pw_run.sh \
  AICRM_PHASE2_MODERN_PLAYWRIGHT_ROOT=/private/tmp/crm-survey-180-investigation-20261003/browser-test \
  AICRM_PHASE2_MODERN_WEBKIT_DRIVER=/private/tmp/crm-survey-180-investigation-20261003/browser-test/webkit/pw_run.sh \
  go test -json -p 1 -count=1 -timeout=5m ./cmd/aicrm \
  -run '^(TestPostgreSQLPublicSurveyOtherWebKitRecovery|TestPostgreSQLPublicSurveyDefinitionChangeWebKitRecovery)$'
```

数据库必须是显式的本地隔离测试 PG。收据保留准确 HEAD/tree、命令、输出摘要和八个 engine/display 子场景的真实终态，skip/空匹配/缺环境不得算通过。

ce545c 的 Linux 已通过 30 个顶层具名检查，保护日志 SHA256 为 `12b2418747e4b8a0186d35f0198716ce1e828c48465c0502dbf261e2b2d291f2`，只在实际执行输入 blob 不变时复用。两个 WebKit skip 不属于这 30 个通过结果。其他 Go 包未执行；前端在首项因缺 `web/dist/admin/spProductForm.html` 停止，保护日志 SHA256 为 `f24b29d2888bf11564785f8a0250e2216d85a406f1058bc84ebf81c3efe7161a`，不复用为通过。

Linux 仍保留 Survey Store/App/HTTP、前端 resume、受众及相关合同的未执行检查。商品 adapter 的六个源码合同使用当前 V4 跟踪的模板绑定和测试内的最小 DOM；不依赖上次构建的 dist。最终同包 HTML/Host 是否挂载 Alipay、信息收集及退役库存断言由既有 `periodic-contact` 安装验收执行，源码 DOM 不能代替这项安装结果。

## 11ace7d Linux 执行后的剩余分工

工作台已实际接收 11ace7d 并接受上述两项 macOS 收据。Linux 后端在 402 个顶层具名 pass 后因 `internal/product/http` 整包拉入八个缺 jsdom 的浏览器合同而停止；保护日志 SHA256 为 `f5f70450167053c64cff0e16d4fa4963637afeaaa57d6f511bb0e92e31d62be7`。八项没有执行业务断言，不记作 pass。发布器和业务实现不修改，清单进一步收窄为 23 项尚未执行的后端/前端/浏览器合同；实际通过部分只凭逐项输入 blob 不变复用，不重复整阶段。

本次 Product HTTP 直接合同取如下八项，已在该日志全部真实 pass，列为复用而非重复执行：`TestHandlerUsesOneToOneLimitAndProductListDTO`、`TestHandlerDeleteCompatibilityPathReachesLifecycle`、`TestHandlerShareReturnsPublicRouteOrProductNotEnabled`、`TestHandlerWriteRequiresAdminCSRFAndMintsCompatibilityKey`、`TestHandlerReturnsTruthfulCompatibilityReads`、`TestHandlerRejectsMalformedIDWithoutApplicationCall`、`TestCompatibilityIdempotencyKeyFailsClosedWhenRandomReadFails`、`TestDistributionPolicyDTOUsesActualReadAndKeepsLegacyUpdateCompatible`。最后一项实际检查普通/周期商品创建及更新兼容旧库存输入、内部 stock 占位0、响应无库存，并保留分销策略合同。

八个缺 jsdom 的失败分别是 Member Grid 两项、公开支付完成/原订单/地区级联/收集矩阵四项、周期公开页和身份生命周期两项。Member Grid 不消费本次库存/Survey 退役改动；其余公开页/身份 JS 本次未变。与此次字段退役直接相连的编辑页、新建、contact checkout 由本批真实 PG/Chromium 收据和最终 `periodic-contact` 同包安装旅程覆盖，因此本轮不另跑这八项、不删除测试或放宽 skip 规则。它们仍是未完成的额外断言，不以缩清单声称其通过。


## 32865a2 执行后：最终构件旅程归安装验收

工作台准确32865a2 Linux执行：剩余后端165个顶层全部pass（保护日志513a177397804aa249acc32a376099d26df84ea8ad3c8de9ade41dfa553f6e9d），十项前端全pass（37c5beaff423c264bcf93f4a8ca0ce8b4cc645c569baf7f9473f13d87d476b09）。最后Survey退役浏览器在fixture准备时缺web/dist/asset-manifest.json而fail（45aace92d5dad925b6dc376d3cec9f2951424adb6591c1469945647f32b7871f），未完成业务断言，绝不当pass。该fixture明确依赖staged release artifact，因此仅从安装前browser lane移走；既有scripts/run-installed-audience.sh已包含TestSurveyAudienceOwnerRetirementChromiumJourney，最终同包必跑它及原send-history/confirmation三项。installed_checks四项与Phone build_commands保持原样。

22个已通过清单条目、原30/402与macOS两项WebKit的执行输入完全相同，保护日志/原HEAD收据保留复用，不重跑阶段。清单必须非空，仅保留一个validate-openapi源码合同。这里只改清单和说明，业务、验证脚本、发布器、OneID/事务/Provider/页面均不变，沿原c58单parent与队首/锁由工作台统一构建、安装和实际退役旅程；已有生产授权继续。Library/GitHub归档不是门禁，本次先国内push/readback和紧凑交接，不等待归档。
