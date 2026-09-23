# 商品创建与分享合同修复

## 用户问题与结论

管理端的已启用普通商品点击“分享”会显示“商品分享响应不完整或越过站内边界”；创建普通商品时，实际入口是无 ID 的 `/admin/wechat-pay/productForm.html`，页面没有单独的分销设置面板，却可能提示“分销设置尚未加载，未提交保存。”

源码确认分享失败的原因是前端把购买路径错误地约束为 `/p/{数据库商品ID}`：`web/v3/productAdapter.ts` 的 `readShare` 以 `resourceId` 比较 `purchase_url`。Product Owner 的 `ShareLocalProduct` 则按已验证的 `product_code` 返回 `/p/` 加 `url.PathEscape(product_code)`（`internal/product/app/local_lifecycle.go`）。因此编码 `123`、而内部 ID 不是 `123` 的已启用商品会被前端误拒绝。线上只读检查确认了该错误；没有点击保存、发送或支付。

创建问题已由当前主线的 #291（`38ca5ce7`，`fix(products): scope distribution policy to sale information`）覆盖：无 ID 的普通／周期编辑器在售卖信息维度没有服务端快照时使用 Product 合同规定的默认禁用分销策略，并把策略随首个 Product command 原子提交。已部署的旧 cbb 快照尚未含该修复。本 PR 不再复制该运行时逻辑；补足真实别名、首个创建与后续配置失败恢复的回归证据，保证最终发布包含已合主线行为。

## 业务与安全分类

- **OneID：不涉及。** 商品 ID／编码不是客户身份；本修复不解析、创建、关联或展示客户身份。
- **持久化：复用既有 Product Owner PostgreSQL command、CAS 与幂等收据。** 分享仍是受权本地 GET。创建时分销策略仍在同一 Product command/UoW；不新增写路径、表、迁移或后台任务。
- **外部效果：不新增。** 分享不发 Provider 请求。既有外推配置失败后的已创建主体 ID、原外推幂等键和只重试配置的合同保持；未知结果不得再次 POST 创建商品。

## 最小实现

1. 在 V3-owned `productAdapter.ts` 中以服务端回包的 `product_id`、精确 `product_code` 和 `lifecycle=enabled`／`available=true` 校验分享结果；购买路径必须是无查询、无片段、无凭据的同源相对 `/p/{单一编码段}`。
2. 仅接受该编码段安全百分号解码后精确等于已加载商品编码的路径。这样允许 Owner 的 `url.PathEscape`（含斜杠、中文及空格），同时拒绝错误商品 ID、错误编码、额外路径、跨源、查询／片段、畸形编码和远程二维码地址。
3. 不改冻结 product form／列表 donor、Product HTTP、OpenAPI、分享 API、分销规则、支付、外推或 Provider 配置。共享反馈继续识别现有 V3 真实按钮绑定。
4. 更新既有真实冻结表单 + Host 回归，使普通创建从实际无 ID 别名 `/admin/wechat-pay/productForm.html` 运行：无预载策略时保存包含默认／编辑后的策略；首次 Product POST 后外推失败保留 ID 与原 key，重试只继续外推。周期商品继续由已注册的真实 Host 测试验证同一原子策略创建。
5. 在同一已注册的产品 Host 测试中验证编码后的有效分享链接及错误 ID／编码／路径边界。测试是合成 HTTP/JSDOM，不代表线上写入或 Provider 回执。

## 用户可见行为

- 已启用商品可获得同一站点的 `/p/{product_code}` 分享链接；商品编码和数据库 ID 不相同不再被当成越界。
- 失配或不安全响应仍显示明确的分享错误，不展示链接或二维码。
- 新建页没有独立分销控件时，售卖信息会按既有禁用默认值随创建保存；不存在“未加载”错误。若主体已经创建而后续外推配置失败，提示主体已保存并允许按原配置幂等键恢复，不重复创建。

## 参考与取舍

- 仓库 GitHub 历史 #291 / `38ca5ce7` 是无 ID 编辑器策略的直接修复参考；不重做已合主线实现。
- 本仓 `LocalProductShare`／OpenAPI `LocalProductShare` 是路径的唯一 Owner 合同。公开 GitHub 检索未返回本私有仓可访问的同类实现。
- WHATWG URL 的编码路径讨论显示编码／解码语义不能用字符串规范化猜测；本修复以 Owner 回包的精确编码和受控解码比对，不采用宽松前缀匹配或移除站内边界校验。
- Product Design 目录当前不可用；按已读前端一致性 Skill 和 component map 复用 V3 Host、真实冻结 form／renderer 与 shared feedback，不创建平行页面或改 donor。

## 验收

1. 普通商品的编码商品码（含编码斜杠、中文、空格）能分享；错误 ID、错误回包编码、额外路径、查询／片段、跨源与远程二维码均不可分享。
2. 实际 `/admin/wechat-pay/productForm.html` 无 ID 创建可保存；分销策略在首个 POST 中，保存链不因缺少已存策略中断。
3. 普通创建后外推 5xx 的已知部分成功保留创建 ID 与原外推 key；恢复不重复 POST。周期商品保留其现有原子策略创建回归。
4. 跑受影响 Node Host suites、TypeScript、donor/source gate 与最终 canonical consumer。最终 PR CI 和线上重新部署／受权回读另行记录；本地 fixture 不是线上验收。
