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
for image_contract_shell in productForm.html mpLib.html images.html attach.html; do
  test -s "$AICRM_INSTALLED_PRODUCT_ROOT/web/dist/admin/$image_contract_shell"
  cmp "$AICRM_INSTALLED_PRODUCT_ROOT/web/dist/admin/$image_contract_shell" "web/dist/admin/$image_contract_shell"
done
node web/v3/productAdapter.material_order.test.mjs
node web/v3/materialSaveAdapter.test.mjs
bash scripts/run-go-with-donor-views.sh go test -json -p 1 -count=1 -timeout=8m \
  -run '^TestPostgreSQLProductImageDeliveryInstalledJourney$' ./cmd/aicrm
```

具名用例校验安装二进制摘要、Host source SHA与readyz，所有断言HTTP和后台Chromium访问实际安装进程；只在隔离DB准备历史素材/收据和种子数据。复用原Product旅程，具名最终日志 `installed_product_image_delivery: PASS` 及Go包/Test终态均必须pass，任何skip、环境缺失或无匹配不得登记通过。按现有受保护收据写入 `product-image-delivery`，绑定原attempt、head/tree、stage receipt、manifest与日志摘要。

`productAdapter.material_order.test.mjs`第9行读取built productForm.html，`materialSaveAdapter.test.mjs`读取built mpLib.html、images.html、attach.html。两项Node合同从安装前frontend清单移至本installed检查，源码测试和全部原断言逐字保留；先逐字核对准确安装包与checkout的四份真实HTML，再实际执行两个Node合同，均exit0后继续Go旅程。Node仍是JSDOM交互合同，Go旅程负责实际安装进程的HTTP/Chromium断言，两者结果分别记录，不将Node模拟称作安装API成功。

本次一次性核对88清单剩余四项及直接辅助模块，执行位置如下：

| Node合同 | 实际页面依赖 | 执行阶段 |
| --- | --- | --- |
| productAdapter.material_order | 第9行直接读取web/dist/admin/productForm.html | 最终安装product-image-delivery |
| productAdapter.sp_material | product-source-test-page.mjs读取source-index及当前V4 canonical template；material_picker使用已登记派生源码 | 保留源码frontend |
| imageLibraryFilterHost | 内存JSDOM页面及esbuild(write:false)源码bundle，无dist读取 | 保留源码frontend |
| radarAdapter.upload_feedback | 内存JSDOM页面及esbuild(write:false)源码bundle，无dist读取 | 保留源码frontend |

product-source-test-page与test-browser-bundle辅助模块也已核对；不改源码模板辅助模块、不伪造新HTML、不提前重复构建Host。两项移至安装阶段的原Node文件及其全部断言必须在原harness复制准确安装dist后真实执行。

1152正式run.json核实的Linux backend 7条命令、22个terminal pass且无skip/fail；88 attempt2正式backend/run.json亦为success/exit0且7条命令全部exit0，清单输入与业务/SQL/UI/测试载荷保持相同。88正式frontend顺序为imageLibraryFilterHost exit0、material_order因缺built productForm.html exit1，sp_material与radar upload尚未执行。有效后端与素材库收据保留；阶段修复不记为原失败用例通过。素材库、周期素材、雷达继续留在源码frontend，material_order与materialSaveAdapter待实际安装阶段，未执行项均不记为通过。

## 雷达失败的定点修复与收据复用

工作台正式822 attempt1：backend 7/7 exit0；imageLibraryFilterHost与sp_material均实际exit0；radar上传合同在固定等待20ms后断言第三个PDF请求失败（actual calls=2、expected=3）。这些原来源/载荷未变的后端和两条前端通过收据，绑定822准确树与原受保护run.json/E/206诊断保留，不能称作在本修复head重新执行。

本机Node24.18.0/JSDOM30.0.1核对PDF File.arrayBuffer存在；uploadRadarPdfDto先await文件读取与SHA256再fetch。对原合同施加合法80ms异步读取，20ms处calls仍为2且没有提前拒绝，随后指定PDF初始化请求正常成为第3次。runtime图片1MiB guard只作用uploadRadarImage，PDF10MiB原合同没有改动。

只修radar测试fixture：用有界2秒等待`POST /api/admin/attachment-library/uploads`，并同时观察pdfPromise，提前拒绝或无请求完成立即失败；保留严格calls===3、PDF>1MiB可进入原multipart路径和模拟网络拒绝断言。保留80ms合法慢读使固定sleep错误可稳定暴露。失败Promise注入复验确认报出具体文件读取错误而非等到超时。没有补虚假的File能力、改runtime/断言或加入skip。

本修复准确树源码清单只选择失败的radar Node合同，installed_checks仍product-image-delivery；其余后端与两项源码前端明确复用822正式通过收据。API/index/lock、SQL、UI/runtime和其他测试字节不变；两个built Host Node合同及Go安装旅程继续由原最终安装包验收，尚未验证，不再重复构建Host或整阶段。

## 直接断言

- multipart/JSON新素材及Excel原始封面超过1MiB返回413，拒绝后素材、blob、素材收据不增加，Excel plan version不变；单文件恰好1MiB允许并同键重放。
- 原件逐字节及ETag保持；1080×2382不按高度缩小，2160×6000得到1080×3000且严格减少字节；JPEG编码膨胀、动画GIF保持原件；历史大图及已完成同键同载荷收据可读/重放。
- 普通web_1080、周期original、未绑定图片404、公开变体ETag及既有Media管理员原件If-None-Match返回304；实际安装顾客页面/Host可读。不扩展公开图片原有缓存合同。
- 实际安装普通/周期后台原生输入拒绝大图后重选有效文件，草稿保留、普通排序与保存读回。来源源码仅用于fixture准备/DB事实读回，浏览器/API业务请求走安装进程。

源码清单另外只补 `TestPostgreSQLAttachmentSizeRemainsSeparateFromManualImageLimit`：有效接近2MiB问卷PNG的持久化/原件/重放不受手动1MiB策略影响。前一附件成员的0239与已过源码检查不重跑。

## 验证与未验证

原图片Mac源码诊断可复用；接续PG合同已通过。本文的Linux最终安装包路径须由发布工作台执行，本机或源码结果不能代填stage-evidence。本批允许进入预发并停留，生产未授权。OAuth、真实支付/Provider效果与真实顾客观察未由此用例验收。

Mac独立二进制诊断已实际通过该具名用例（11.367秒）：运行时构建自1db附件基线上的本成员开发快照999a25e，二进制摘要8bd75277c8f0f54295d87933423f9d90acc3f9e56638e14759fac1079968d0fa，Host与readyz均校验；修正仅在测试fixture摘要前缀、既有条件请求合同与安装夹具支付宝配置。受保护API与后台浏览器走独立进程，未共享安装。此为验收路径诊断，后续接续源码和Linux最终包仍须独立绑定原控制器收据。
