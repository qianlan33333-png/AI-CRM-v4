# 用户订单中心与腾讯数电发票启用

固定入口为现有官方 HTTPS 域名下 `/my/orders`，可放入公众号菜单、消息或其他入口。用户只经过现有公众号 OAuth；应用不从 URL 接收客户 ID、OpenID 或会话凭据。订单查询不要求发票 Provider 已开通。

## 启用顺序

1. 在微信支付申请本企业自有普通服务商数电发票能力，完成开票商户税务授权、服务商邀请、开票员授权、公众号绑定与电子发票卡券配置。现有收款商户保持为销售方；发票签名使用独立服务商商户凭据。官方入口见 [接入准备](https://pay.wechatpay.cn/doc/v3/partner/4015792554)、[接入指南](https://pay.wechatpay.cn/doc/v3/partner/4015792556)。
2. 沿用现有 `AICRM_PUBLIC_ORIGIN` 和微信 H5 OAuth 配置：公众号网页授权域名、JS 接口安全域名、OAuth 回调、支付 H5 AppID/AppScope 及公众号 Secret。OAuth 只允许精确 `/my/orders` 回跳。
3. 财务填写并保存只读税务映射文件，每个商品的 `product_code` 映射税收分类编码（19 位数字）、税率（万分比）、正式开票项目名。`rate: 600` 表示 6%；0 必须明确填写。折扣按订单真实实付金额分摊，未配置的商品不可开票。示例是格式占位，不是可直接使用的税务建议：

   ```json
   {"<商品编码>":{"code":"<财务确认的19位编码>","rate":600,"goods_name":"<财务确认的开票项目名称>"}}
   ```

4. 在受保护的运行环境保存以下配置，不把 Secret/密钥/税务抬头放进日志、Git 或工单：

   | 配置 | 含义 |
   |---|---|
   | `AICRM_INVOICE_PROVIDER_ENABLED` | 默认 false；全部准备并读回后才设 true |
   | `AICRM_INVOICE_SERVICE_MERCHANT_ID` | 自有服务商商户号 |
   | `AICRM_INVOICE_MERCHANT_SERIAL` | 服务商 API 证书序列号 |
   | `AICRM_INVOICE_PRIVATE_KEY_PATH` | 服务商签名私钥文件 |
   | `AICRM_INVOICE_PLATFORM_CERT_PATH` | 当前有效微信支付平台证书，轮换前更新 |
   | `AICRM_INVOICE_API_V3_KEY` | 服务商 APIv3 密钥，32 字节 |
   | `AICRM_INVOICE_SELLER_NAME` / `...SELLER_TAX_ID` | 本企业开票主体 |
   | `AICRM_INVOICE_BILLING_PERSON_ID` | 已授权开票员 |
   | `AICRM_INVOICE_TAX_CONFIG_PATH` | 财务保存的映射文件绝对路径 |

   启用要求现有 External Effects、微信支付及 H5 OAuth 同时启用。配置缺失、文件无效或税率未明确填写时启动失败，不能宣称已开通。只记录配置存在与摘要，不读出凭据。
5. 将微信抬头、出票、冲红、插卡和卡券作废通知配置到同一个官方 HTTPS 地址 `/api/callbacks/wechat-invoice`。通知经官方 SDK 验签解密并去重，仅触发原申请的认证查询；查询确认最终状态。
6. 保存后独立读回服务商资格、税务授权、公众号/卡券配置和财务文件摘要。通过唯一发布工作台安装同一准确构件，真实验收分别记录微信授权、真实出票、卡包插入、全额/部分退款后的整票冲红及卡券作废。HTTP 204、队列受理和 Provider 接口受理均不等于出票或交付。

## 运行行为与恢复

订单由 Order Port 查询，微信授权只识别 OneID。已合并客户沿用 OneID 归属，付款人与受益人都能查看相关订单，但只有付款人可申请及查看其发票。订单号为完整精确匹配，商品关键词按字面包含匹配（`%`、`_` 不作为通配符）；日期为北京时间，结束当天包含在内。历史原始时间缺失时不使用导入时间冒充。

一单一票，只开原生已确认微信支付订单。进行中的退款暂停提交；确认退款取消未发出的意图。已经提交或结果未知时先查原票号，确认出票后整票冲红。部分退款的剩余金额由财务处理，不自动净额重开，也不影响原退款 Provider 的执行。

抬头入口过期沿用原申请重新取得。未知结果保持原票号与效果，不换号。微信明确 `ISSUE_FAILED` 后用户“重试开票”最多 4 次；冲红明确失败后原票号延迟至少 5 分钟重试，最多 4 次，耗尽后显示“冲红待处理”由财务核对。新通知唤醒同一个待执行查询任务，既有取消的任务不被恢复；用户刷新仍可主动查询。

卡包交付与出票分开显示。发票文件仅本人鉴权后下载；按 [官方下载合同](https://pay.wechatpay.cn/doc/v3/partner/4015792569) 取 30 秒链接，服务端不携带支付 Authorization、不跟随跳转，校验 SM3 摘要。仅尚未冲红的已出票发票可下载。

## 开发验收

`docs/prd/2026-10-08-customer-orders-invoice.md` 是共用父 PRD；`design-qa.md` 记录 Product Design 页面验收。PG Journey 在显式本地 `aicrm_test_*` 隔离库运行，通知签名/解密测试用独立合成 RSA/AES 密钥，绝不调用真实出票。

本地预览可显式运行：

```sh
DATABASE_URL='postgresql://<本机用户>@localhost:5432/aicrm_test_invoice?sslmode=disable' AICRM_INVOICE_PREVIEW_LISTEN='127.0.0.1:8797' go test ./cmd/aicrm -run '^TestInvoicePreview$' -v -timeout 90m
```

该预览使用真实挂载页、真实 OneID/订单/退款/EER PostgreSQL 适配器与合成 Provider；自动注入测试会话仅存在于测试文件且限定 loopback。它不是生产路由、真实授权或出票证明。
