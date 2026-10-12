# 商品图片最终包验收

父PRD：[商品图片上传与详情](../prd/2026-10-12-product-image-delivery.md)。本次只增加具名验收用例，沿用既有InstalledProduct、隔离数据库、stage-evidence和串行发布工作台。

## 精确执行

工作台在已安装最终包、准确干净源码HEAD以及既有 `aicrm_test_*_acceptance_test` 本地合成数据库上执行；以下变量由原控制器的head、manifest、真实二进制摘要与包根目录填写，不能使用源码构建代替已安装文件。

```sh
set -euo pipefail
export AICRM_INSTALLED_PRODUCT_BINARY="$final_package_root/bin/aicrm"
export AICRM_INSTALLED_PRODUCT_ROOT="$final_package_root"
export AICRM_INSTALLED_PRODUCT_SHA="$final_candidate_sha"
export AICRM_INSTALLED_PRODUCT_BINARY_SHA256="$installed_api_sha256"
export AICRM_REQUIRE_CHROMIUM_JOURNEY=1
# AICRM_DATABASE_URL沿用原隔离验收库配置，不使用生产库。
# 原工作台harness将准确安装包web/dist复制到私有源码checkout。
# 校验built shell来自同一安装包，不构建源码Host或生成替代模板。
for image_contract_shell in mpLib.html images.html attach.html; do
  test -s "$AICRM_INSTALLED_PRODUCT_ROOT/web/dist/admin/$image_contract_shell"
  cmp "$AICRM_INSTALLED_PRODUCT_ROOT/web/dist/admin/$image_contract_shell" "web/dist/admin/$image_contract_shell"
done
node web/v3/materialSaveAdapter.test.mjs
bash scripts/run-go-with-donor-views.sh go test -json -p 1 -count=1 -timeout=8m \
  -run '^TestPostgreSQLProductImageDeliveryInstalledJourney$' ./cmd/aicrm
```

具名用例校验安装二进制摘要、Host source SHA与readyz，所有断言HTTP和后台Chromium访问实际安装进程；只在隔离DB准备历史素材/收据和种子数据。复用原Product旅程，具名最终日志 `installed_product_image_delivery: PASS` 及Go包/Test终态均必须pass，任何skip、环境缺失或无匹配不得登记通过。按现有受保护收据写入 `product-image-delivery`，绑定原attempt、head/tree、stage receipt、manifest与日志摘要。

`materialSaveAdapter.test.mjs`读取built shell（包括mpLib.html）。该Node合同从安装前frontend清单移至本installed检查，源码测试和全部原断言逐字保留；必须先验证准确安装包的真实mpLib.html与checkout副本一致，再实际执行Node合同，终态exit0后继续Go旅程。此Node合同依然是JSDOM交互合同；Go旅程负责实际安装进程的HTTP/Chromium断言，两者结果分别记录，不将Node模拟称作安装API成功。

1152正式run.json核实的Linux backend 7条命令、22个terminal pass且无skip/fail，frontend的imageLibraryFilterHost第一条exit0；这些有效收据保留，backend清单输入与全部业务/SQL/UI/测试载荷保持相同。imageLibraryFilterHost继续留在源码frontend，其余源码frontend合同按准确清单在Linux执行；materialSaveAdapter待最终安装阶段，不记作已通过。不新增runner或重复Host构建。此前将失败错误归到imageLibraryFilterHost的口头报告已由正式结果纠正，不据此撤掉有效检查。

## 直接断言

- multipart/JSON新素材及Excel原始封面超过1MiB返回413，拒绝后素材、blob、素材收据不增加，Excel plan version不变；单文件恰好1MiB允许并同键重放。
- 原件逐字节及ETag保持；1080×2382不按高度缩小，2160×6000得到1080×3000且严格减少字节；JPEG编码膨胀、动画GIF保持原件；历史大图及已完成同键同载荷收据可读/重放。
- 普通web_1080、周期original、未绑定图片404、公开变体ETag及既有Media管理员原件If-None-Match返回304；实际安装顾客页面/Host可读。不扩展公开图片原有缓存合同。
- 实际安装普通/周期后台原生输入拒绝大图后重选有效文件，草稿保留、普通排序与保存读回。来源源码仅用于fixture准备/DB事实读回，浏览器/API业务请求走安装进程。

源码清单另外只补 `TestPostgreSQLAttachmentSizeRemainsSeparateFromManualImageLimit`：有效接近2MiB问卷PNG的持久化/原件/重放不受手动1MiB策略影响。前一附件成员的0239与已过源码检查不重跑。

## 验证与未验证

原图片Mac源码诊断可复用；接续PG合同已通过。本文的Linux最终安装包路径须由发布工作台执行，本机或源码结果不能代填stage-evidence。本批允许进入预发并停留，生产未授权。OAuth、真实支付/Provider效果与真实顾客观察未由此用例验收。

Mac独立二进制诊断已实际通过该具名用例（11.367秒）：运行时构建自1db附件基线上的本成员开发快照999a25e，二进制摘要8bd75277c8f0f54295d87933423f9d90acc3f9e56638e14759fac1079968d0fa，Host与readyz均校验；修正仅在测试fixture摘要前缀、既有条件请求合同与安装夹具支付宝配置。受保护API与后台浏览器走独立进程，未共享安装。此为验收路径诊断，后续接续源码和Linux最终包仍须独立绑定原控制器收据。
