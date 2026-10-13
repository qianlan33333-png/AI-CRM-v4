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
   | `AICRM_INVOICE_AUTH_MODE` | `public_key` 或 `certificate`；缺省保留 certificate 兼容 |
   | `AICRM_INVOICE_PUBLIC_KEY_ID` / `...PUBLIC_KEY_PATH` | public_key 模式的已核验微信支付公钥 ID 和 PEM；不可使用商户 API 证书或其序列号代替 |
   | `AICRM_INVOICE_PLATFORM_CERT_PATH` | certificate 模式的有效平台证书；不可与公钥配置混用 |
   | `AICRM_INVOICE_API_V3_KEY_PATH` | 推荐：服务商 APIv3 密钥文件，32 字节，可有单个末尾换行；属主为运行用户，权限 0600 |
   | `AICRM_INVOICE_API_V3_KEY` | 兼容原环境变量；不得同时设置 KEY_PATH |
   | `AICRM_INVOICE_SELLER_NAME` / `...SELLER_TAX_ID` | 本企业开票主体 |
   | `AICRM_INVOICE_BILLING_PERSON_ID` | 已授权开票员 |
   | `AICRM_INVOICE_TAX_CONFIG_PATH` | 财务保存的映射文件绝对路径 |

   启用要求现有 External Effects、微信支付及 H5 OAuth 同时启用。配置缺失、文件无效或税率未明确填写时启动失败，不能宣称已开通。只记录配置存在与摘要，不读出凭据。
5. 将微信抬头、出票、冲红、插卡和卡券作废通知配置到同一个官方 HTTPS 地址 `/api/callbacks/wechat-invoice`。通知经官方 SDK 验签解密并去重，仅触发原申请的认证查询；查询确认最终状态。
6. 保存后独立读回服务商资格、税务授权、公众号/卡券配置和财务文件摘要。通过唯一发布工作台安装同一准确构件，真实验收分别记录微信授权、真实出票、卡包插入、全额/部分退款后的整票冲红及卡券作废。HTTP 200、队列受理和 Provider 接口受理均不等于出票或交付。

## 配置检查与填写位置

`cmd/invoice-setup` 复用现有官方 SDK，不依赖数据库或 APIv3 密钥；默认只读，不开票或退款。工具校验服务商 API 证书的商户号、有效期和私钥匹配，以及显式公钥/平台证书信任材料。参数文件 0600；每次使用一个新的回执路径，文件包含先持久化的操作意图和最终结果两条 JSONL。原始回执及开票人/销售方信息只保存在私密文件；stdout 仅输出汇总。

配置文件格式（路径与商户参数由运维从实际证书/环境填写，logo 尚未确认可以留空做 read）：

```json
{
  "service_merchant_id": "<服务商商户号>",
  "sub_merchant_id": "<收款商户号>",
  "merchant_certificate_path": "/private/apiclient_cert.pem",
  "private_key_path": "/private/apiclient_key.pem",
  "auth_mode": "public_key",
  "public_key_id": "PUB_KEY_ID_<已核验公钥ID>",
  "public_key_path": "/private/pub_key.pem",
  "public_origin": "https://<官方域名>",
  "card_appid": "<现有公众号AppID>",
  "card_logo_url": "<公众号身份上传永久素材得到的mmbiz.qpic.cn图片URL>",
  "show_invoice_cell": true
}
```

```sh
invoice-setup -config /private/invoice-setup.json -action read -receipt /private/read-1.jsonl
invoice-setup -config /private/invoice-setup.json -action development -receipt /private/development-1.jsonl
invoice-setup -config /private/invoice-setup.json -action card -allow-card-replace -receipt /private/card-1.jsonl
```

- `read`：读取权限、商户基本信息、税码目录、服务商及商户开发配置、最近六天授权记录。`complete` 仅代表这些读取全部成功；不代表应用启用。基础信息接口报地址校验错误时保持错误事实，可从匹配该商户的已验签邀请记录获取主体事实，再由财务确认；不得填造地址。空税码目录不自动猜税，需要填写本地财务映射。
- `development`：必须先安装本候选回调处理器；GET 返回 200 仅表示可达，不接受业务通知。配置官方 HTTPS 回调，再分别 GET 服务商回调及商户开关。专票保持关闭。统一交易单号实现安装后，`show_invoice_cell=true` 开启微信账单入口；两入口共用交易单号、原订单归属、申请及开票 / 冲红任务。开关保存后独立读回 true 才算配置完成，真实微信账单可见性另记。公众号菜单仍用原订单链接。

回调初始化顺序：先设置 `AICRM_INVOICE_CALLBACK_ENABLED=true`，保留 `AICRM_INVOICE_PROVIDER_ENABLED=false`，安装并独立读回。此时只加载 pinned 通知信任和私密 APIv3 密钥，不创建开票 ProviderAdapter；已知申请的业务通知返回 503、无回执或查询任务，未知申请只有严格验签解密后才应答。完成 `development` 保存并独立读回后，再按已有授权启用正式开票。两项都默认 false，正式开票启用会自动初始化回调，无需强制重复设置开关。切勿把非空无签名 POST 改成 200。

预发安装验收：在唯一工作台的显式 loopback `aicrm_test_*_acceptance_test` PostgreSQL 环境执行 `bash scripts/run-installed-invoice-callback.sh --binary <已安装bin/aicrm> --root <已安装包目录> --sha <准确HEAD> --binary-sha256 <准确二进制摘要>`，检查 `TestInvoicePostgreSQLCallbackBootstrapInstalled` 非跳过通过及 `invoice-callback-bootstrap-installed PASS`。测试只启动已安装 API 程序和合成密钥，不重建源码应用；读取申请、回执、River 查询任务和 Invoice 外部效果数量确认无业务写入。
- `card`：明确确认后才使用 `-allow-card-replace`，官方接口会覆盖原模板；logo 必须来自公众号永久素材。保存验签的 `card_appid/card_id` 创建回执。官方没有模板 GET，该回执不冒称独立卡包验收；启用后用真实出票/插卡/卡券读取验收。网络或验签结果未知时停止写入，不创建新回执盲目重试覆盖。

给用户的准确链接位置：

| 位置 | 填写内容 | 谁填写 |
|---|---|---|
| 公众号后台 → 自定义菜单 → 菜单内容「跳转网页」 | `https://<官方域名>/my/orders`，保存并发布菜单 | 用户 |
| 公众号后台 → 设置与开发 → 公众号设置 → 功能设置 → 网页授权域名、JS 接口安全域名 | 官方域名主机名，不带 https 和路径；复用当前已生效 OAuth/JS 配置 | 用户核对已有配置 |
| 微信支付合作伙伴平台 → 账户中心 → API 安全 → APIv3 密钥 | 服务商号的 32 字节密钥；本机写入私密 KEY_PATH 文件 | 用户 |
| 私密 `invoice-setup.json` 的 `card_logo_url` | 公众号永久素材的 logo URL | 用户 |
| 私密税务映射 `product_code → code/rate/goods_name` | 财务确认；只保留首版要支持的商品。可显式填写官方 `preferential_policy_code`（1–18），不推断政策 | 财务 |
| 官方配置接口 `PATCH .../merchant/development-config` 的 `callback_url` | `https://<官方域名>/api/callbacks/wechat-invoice` | 运维用工具配置，用户不用寻找后台输入框 |
| 官方卡券模板 `custom_cell.jump_url` | `https://<官方域名>/my/orders` | 运维用工具配置 |

商品税率仅接受官方支持值（单位万分比），遗漏或 `null` 不等同于 0。官方抬头查询的可选地址、电话、银行、联系方式及留言保留；手机号和邮件用商户 API 私钥解密，出票前以副本用对应微信支付信任公钥重新加密，持久化快照及效果摘要不因加密随机性改变。

生产准备：由唯一发布工作台将服务商私钥、公钥、APIv3 密钥及财务映射安装至 `/etc/aicrm/keys/invoice/`（目录 0700，文件 0600，属主运行用户 `aicrm`），将发票配置合入 `/etc/aicrm/aicrm.env`；只修改 `AICRM_INVOICE_*`，现有微信收款凭据保持原商户。最终输入与启用授权齐备后才设置 `AICRM_INVOICE_PROVIDER_ENABLED=true`，所有 API / worker 使用同一配置并独立读回。此任务只交付候选，不安装共享环境。

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

### 2026-10-09 统一交易单号流程

原生账单回调 FAPIAO.USER_APPLIED 只通过 Payment Port 找本商户已确认的原交易、付款人及可信 Identity handle；未确认交易返回可重试，不建客。CRM 表单最终确认和原生回调使用同一申请 ID（交易单号），同一 Order 锁与创建函数；官方抬头读取固定 WITH_WECHATPAY。CRM POST 缺单位抬头或确认金额直接拒绝，不再生成官方跳转链接。个人抬头不接受开票效果。

回调及提交会唤醒原查询任务；发出票据意图后也立即唤醒，避免已有退避时间遮挡终态。旧唯一测试申请不做兼容或自动重开，税码问题保持待办。启用账单展示不代表出票或卡包成功。

安装收据 `invoice-callback-bootstrap-installed` 同时运行 CallbackBootstrapInstalled 和 BillEntryInstalled，后者通过准确安装 binary、隔离 PG 与合成 RSA/AES 回调创建交易单号申请，验证重复通知仅一份回执 / 任务及本人详情读取；API 角色不执行 Provider 写。

安装测试的账单夹具必须满足正式 Config.Load 对 Invoice / Effects / WeChatPay / H5 OAuth 的开启与合成凭据合同，不关闭或绕过校验。夹具仍只启动 API 角色，无 effects/query worker，远程 HTTP 由失效 loopback proxy 阻断；支付平台证书、签名及 OAuth secret 全为隔离生成。已通过 bootstrap 的输入不变；工作台在原安装参数与严格 isolated PG 保护下，直接用具名 Go 命令在同一已安装包只重跑 BillEntryInstalled，原 runner 保持两例。此修复只改 Go 测试/说明/清单，应用二进制不重建。

合成启动检查 `TestInvoiceInstalledFixtureConfigContract` 使用与安装测试相同的环境构造器，直接执行正式 Config.Load，覆盖 bootstrap / bill 正常装载及把未生效变量当开关时的拒绝。Effects 开关的正式入口是 `AICRM_OUTBOUND_PROVIDER_ENABLED`；仅 bill 设 true，WeCom 仍关闭、只运行 API。

`TestInvoicePostgreSQLInstalledFixtureStartup` 在隔离 PG 中以同一环境执行正式 Config.Load → compose → bootstrap → readiness → 本人已挂载开票记录读取，先检查完整启动链。合成 H5 AppID 与 Survey/OA AppID 一致，开放平台 scope 有明确合成值；不启用 Survey OAuth Provider，不放松正式检查。该结果仍是源码初始化证据，准确安装包 BillEntryInstalled 需工作台另行执行。目录准备抽为测试 helper，bootstrap 环境值及业务断言保持不变。

同一源码完整启动用例进一步复用安装回调断言，验证合成签名的未知本商户交易、其他商户、原生申请创建/通知重放及本人详情。未知本商户交易沿用 HTTP Handler 已有 `ErrPending → 409 pending` 合同（非成功，Provider 可重试），安装夹具的 503 预期纠正为 409；不修改应用应答映射或通知业务逻辑。

### 明确失败后的恢复

本人“重试开票”与新的可信账单抬头通知都须先查询原申请/原票号，只有 `ISSUE_FAILED` 且无蓝票才读取最新财务配置。普通刷新只读。申请/订单加锁后，原票号变化、效果仍 queued/attempted/outcome_unknown、退款或财务主体/金额变化均禁止重开。原税务快照和票号保存在 `invoice_audit_events.material`，新票号、快照、审计、EER 与查询任务同 UoW。账单通知恢复任务绑定失败票号，重复通知或旧任务不再生成另一张票。

针对用户本次明确授权的旧测试汇合，唯一发布工作台在迁移前备份，安装候选后仅运行已安装 `aicrm` 的一次性 `invoice-recover` 角色，并用私密环境指定 `AICRM_INVOICE_RECOVER_ID`。该角色无 HTTP、无独立 Worker、无直接开票调用；仅通过 Invoice Service 核验原失败、目标交易申请尚未提交、官方单位抬头及 Payment/OneID 原付款事实，原子把此失败记录归一为交易单号并接受原 EER。旧 lookup 不做别名兼容，收据/回调/审计 FK 级联保留。

恢复输出的 queued 仅表示意图接受，必须另读原 EER 尝试、微信签名查询、票号/PDF、卡包状态；未知结果只读对账，不重复运行或换申请号。

### 已尝试但官方明确未受理

一次性 invoice-recover 角色还可处理 canonical 申请的 issuing 状态：仅在全申请签名查询明确未受理、原 EER outcome_unknown 租约已过期且归属/付款/金额/主体/退款核验通过时，通过既有 Finality Port 对账并接受相同 apply_id、相同 fapiao_id、原冻结材料的受限重放。普通页面读取、原生通知和查询任务均不触发此路径。网络未确认或官方已有票据时停止。

已验签的 POST 错误在 invoice_audit_events 中以 official_response / material 保存 HTTP 状态、code 与 message，随 EER 完成事务一起提交；仅授权运维读取，不能输出到日志或公共页面。发布后由唯一工作台选择原交易号执行一次恢复，先读取这份业务回执再判断参数或外部配置阻塞；不因 API 受理宣称出票。

### 已明确购买方不一致的材料修正

invoice-recover 对选定 issuing 原单先检查私密 official_response 是否确属当前 issue_effect 的 dispatch_prepared 之后，摘要须与规范化回执一致。仅已验签的 HTTP403/RULE_LIMIT/购买方不一致可进入修正；根申请仍未受理、原 Unknown 过期、单位核心及付款/主体/财务不变后，补回官方完整附加字段，同 UoW 保留前后 Buyer、关闭旧 finality、接受材料摘要幂等的修正意图和任务。原 apply/fapiaoID及原样重放预算均不改。buyer_corrected 按当前票号封住第二次修正，不能以后来再有字段差异重复执行。

自填首单在 Provider 读取事务外检查官方抬头。已存在且单位核心一致时保持完整官方 Buyer；单位不同要求用户核对，未知不采用缺字段副本。公开页面仍不显示银行/联系方式，不新增邮件发送能力。


### 官方抬头存在但本地申请缺失

操作员显式选择原交易号的 invoice-recover 也可补取遗漏的原生抬头申请。只有官方已验签/解密的单位抬头与全申请明确未提交证明均通过，且原 Payment/OneID/Order、实付金额、商户订单号、主体、税务及退款核验通过，才能在原 UoW 创建或复用该交易号申请，保存 native_title_recovered 审计并接受已有效果及共享查询任务。不得补造官方通知收据，不直接调用开票。延迟通知或并发恢复返回同一申请，不覆盖已接受抬头。公开读取和普通页面重试不会创建缺失申请。结果不明/已有票据停止；接口接受仍须另行查询出票和卡包结果。


### 全商品默认开票配置

税务map中的 `*` 是财务明确设置的全商品默认，本次已获用户授权使用3040203000000000000、rate100（1%）、服务费。新商品缺少专属配置时自动继承，存在专属配置时以专属为准。保留原两项专属配置，不需要再补113322单独项；配置保存及运行时加载须独立读回。不为所有历史订单批量开票；仍须本人已确认微信付款、无退款、主体一致和单位抬头等原资格。

## 测试层及准确构件输入

普通 source/PG 检查不选择安装包合同，结果明确为未验证（SKIP），不能记 PASS。安装入口设置 `AICRM_REQUIRE_INSTALLED_PRODUCT_CONTRACTS=1`；缺输入、部分输入、错误摘要或失败必须停止该合同。四个安装用例分别是 CallbackBootstrapInstalled、BillEntryInstalled、DistributionInstalledAuthorizationBoundaries 和 SettingsInstalled。

`run-installed-invoice-callback.sh` 可在原四对参数后传入 `--host-source-sha SHA --host-manifest-sha256 SHA256 --source-repository /absolute/current-v4-repository`。复用 Host 时必须提交这些准确来源；验收比较 App 与 Host 的实际 Git web tree 并校验安装资源字节。独立运行授权合同使用同样的 `AICRM_INSTALLED_PRODUCT_*` 输入。不得通过关闭摘要或资源验证来修复夹具。
