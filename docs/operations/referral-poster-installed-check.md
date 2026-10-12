# Referral B 版同包验收入口

`referral-poster` 是原 stage-evidence 中的一项业务检查名称，不新增发布器、服务或验收通道。此同一浏览器旅程仅登记在installed_checks，安装前清单不含browser lane；最终Linux/PG/375与430视口/实际导出独立解码必须执行，不以源码Host重复构建替代。

复用 `cmd/aicrm/referral_chromium_postgres_integration_test.go:TestPostgreSQLReferralChromiumJourney` 和 `cmd/aicrm/referral_chromium_journey.mjs`。未给安装输入时为源码旅程；给现有固定 helper 的 `-domestic-release-smoke-input` 时，复用 `referralInstalledArtifact` 的 protected-input、staging角色、准确路径/二进制SHA校验，启动该安装二进制的隔离实例，经原loopback TLS入口访问真实安装的API和静态资源。此模式不重打前端包，不将源码Host当成已安装Host，所有Provider disabled；源码composition只准备合成身份/管理员/数据库事实。

在已有Linux检查工作区及私有测试库执行（输入文件由原固定helper提供，不能用生产库）：

```sh
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 \
  bash scripts/run-go-with-donor-views.sh \
  go test -json -p 1 -count=1 -timeout=4m \
  -run '^TestPostgreSQLReferralChromiumJourney$' ./cmd/aicrm \
  -args -domestic-release-smoke-input=/path/to/existing-private-smoke-input.json
```

沿用既有 `AICRM_DATABASE_URL`、Chromium resolver、合成fixture和stage-evidence格式，`/path/to/...`需替换为原helper的实际0600输入文件。同时保留原已累计成员的installed checks。Go和Chromium错开，不在预备机并行跑整仓测试。

必要断言：

- `/login`真实登录返回303，管理员cookie访问后台；邀请使用真实可信browser session和Identity Port，不从请求体自报Customer。
- 已发布图片URL含64位v，JPEG且字节小于上传PNG，304无body，旧无参数可读，错误v=409；草稿即使If-None-Match=*也404。304不读BYTEA另由已选HTTP spy合同断言，不以线上304推测SQL行为。
- 连点3次只增加1次POST invite；合成img确实为JPEG、1250×2426，个人邀请预览指向当前actor。PNG回退、加载失败/取消/迟到的详细拒绝断言由已选源码合同覆盖，不冒称浏览器只凭DOM就证明资源释放。
- 两活动和375/430视口，切换配套话术、纯文本/换行/emoji复制、空话术隐藏、滑动、关闭和重开布局更新。JPEG有损，删除原PNG-only逐像素相等断言；排除码区边缘8像素JPEG块后，背景RGB RMSE≤8（0..255量纲），码区确有替换。默认相关灰度纹理PNG实测323045字节，JPEG95为285553字节，满足后台手动上传≤1048576字节且严格压缩收益；不冒充B画质验收。`AICRM_REFERRAL_POSTER_SOURCE`覆盖素材也必须是有效PNG且≤1MiB，超限直接失败，不预种或绕过真实上传。已批准的约789024字节B文件是JPEG参考样张，历史原PNG和真实保存/QR证据保留审计，不宣称原PNG符合本批新上传政策。
- 保存实际`composed-375/430.jpg`、`adjusted-375/430.jpg`及expected_url JSON，独立解码需唯一且等于expected_url。微信原生长按/保存后码区及真实归因仍是真机未验证项。

订单读取继续复用既有 `scripts/run-installed-wechat-shop.sh` / `TestPostgreSQLShopInstalledAPI` 的真实安装API读取（相同安装包SHA和二进制摘要）；该入口已经用于本批survey/staff的同包检查，无需新建订单探针服务。poster旅程本身验证真实安装的管理员登录；完整包中的其他订单/登录检查以实际收据单列。另有本机1000用户负载的Order.Get和Access会话/账号探针，不能当作最终包读回。

通过后仅把实际日志、SHA/摘要、terminal pass和浏览器产物写入原stage-evidence，名称`referral-poster`。源码检查、macOS负载、此前截图均不算本轮Linux/最终安装包通过。开发任务不运行共享安装或生产晋级。
