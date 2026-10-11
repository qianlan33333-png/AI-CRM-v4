# 微信小店启用与验收

按现有国内发布工作台执行同包预发和生产。0235 新增商品来源与绑定；0236 新增外部订单事实、收据和日窗口进度，并只对微信小店外部金额放开原生乘法约束。原生订单和已有历史事实保持原约束；生产迁移前按现有流程备份。不得在共享预发接入真实小店和真实开通端点。

首轮相关检查见 `docs/release-checks.json`。已安装包执行：

```sh
AICRM_DATABASE_URL='<loopback aicrm_test_*_acceptance_test>' \
  bash scripts/run-installed-wechat-shop.sh \
  --binary /absolute/package/bin/aicrm --root /absolute/package \
  --sha <package-source-sha> --binary-sha256 <binary-sha256>
```

该检查沿用 Product 已安装夹具和现有进程启动器，只在临时数据库写入合成商品、订单、登录会话。通过准确安装二进制验证刷新鉴权与任务复用、来源及退款读取、支付回调验签和收据 / River 去重。不开 Worker，网络出口被阻断；商品 / 订单官方读取与日窗口补漏由同候选的 Provider 合同和 PostgreSQL 测试覆盖。此收据不代表微信 API 读取或实际开通成功。

生产凭据仅写受保护环境文件，不输出值：

- `AICRM_WECHAT_SHOP_APP_ID`
- `AICRM_WECHAT_SHOP_APP_SECRET`
- `AICRM_WECHAT_SHOP_CALLBACK_TOKEN`
- `AICRM_WECHAT_SHOP_CALLBACK_AES_KEY`

运行时配置中心会覆盖环境中的 AppID 和启用开关。工作台必须同时读取、保存并发布 `wechat_shop.app_id`、`wechat_shop.provider_enabled`，使用现有配置发布 / 应用机制；保留原有 AppID 非空不换绑的保护。环境中的凭据加载后，按既有启动配置验证再启用。复用已有 `AICRM_COMMERCE_PUSH_PAYLOAD_DATA_KEY`、目标配置与外推 Provider 开关，不另造密钥或开通地址。配置应用后独立核对 API 与 effects-worker 的准确应用版本、有效配置修订、非空 AppID、服务状态及数据库 T0；报告只包含存在状态，不包含秘密值。

`wechat_shop_order_progress.activated_at` 是每个 AppID 首次有效启用时写入且不重置的 T0。支付时间小于 T0 的订单只入库。首次观察的订单即便已全额退款，也保留不可变支付快照，T0 后按明细外推。停机重启从已成功 `completed_until` 逐个 24 小时窗口补漏；列表与全部详情成功后才推进。微信接口时间边界可能重复返回，同来源去重消除重复。正式启用后的日常范围为过去一天及未成功的遗漏窗口。

商品每天北京时间 02:30 刷新，订单每天 03:00 补拉；运行于既有 effects-worker River，不新建 cron / systemd timer。商品页面一键刷新复用活动任务并显示完成数量。商品导入、外推配置保存通过同事务 River 唤醒未接受订单，已接受或未知结果不重建。原始订单明细不可修改，待绑定恢复仅通过 Product Port 解析来源 SKU，补充外推意图。

微信小店管理后台的正式回调 URL 为 `https://www.youcangogogo.com/api/public/wechat-shop/callback`，Token 与 EncodingAESKey 使用对应受保护配置。控制台 URL 验证、真实支付通知、微信商品读取和接收方实际开通分别取得证据，不能由技术安装替代。原 `/api/public/wechat-shop/` 下的退款合同保留。

首次历史 / 异常窗口由工作台显式运行同一安装二进制，不启动 HTTP 或 Worker：

```sh
AICRM_ROLE=wechat-shop-backfill \
AICRM_SHOP_BACKFILL_FROM='2026-10-10T03:00:00+08:00' \
AICRM_SHOP_BACKFILL_UNTIL='2026-10-11T03:00:00+08:00' \
  /opt/aicrm/current/bin/aicrm
```

使用当前受保护运行环境与配置中心设置。最大 7 天自动分段，不推进日常进度；失败复用原窗口重试。不得把 7 天 / 全历史回填设为日常任务。已有 `history` 来源的小店订单仅保留查询，遇到同来源单号不转换为激活候选。

独立业务验收：确认当前在售 SKU 导入且原配置未覆盖；确认一个 T0 后真实支付单读取、明细外推及接收方结果；退款同步后人工处理权益。缺联系方式按空值发送，接收方拒绝由现有结果记录呈现。结果不明只读对账，沿用原发送键。
