# Cached-DNS front door handover

Classification: identity/provider ownership involved; no new identity resolution or provider execution. This change only prepares edge configuration. No reload, DNS update, service stop or business mutation was executed.

## Proposed routing

| Request | Arrives at old IP | Arrives at new IP | Final owner |
|---|---|---|---|
| All pages/new orders/OAuth | HTTPS fixed new IP, formal Host/SNI | V3 local8080 | V3 |
| WeCom two existing aliases | HTTPS fixed new IP | V3 local8080 | V3, after grouped credential switch |
| POST old H5 payment/refund notify | local5001 | HTTPS fixed old IP → exact local5001 | Old payment owner temporarily |
| GET/POST old shop notify | local5001 | HTTPS fixed old IP → exact local5001 | Old shop owner temporarily |
| Wrong method old money notify |405|405|No mutation|
| Two old automation internal callbacks |503 +Retry-After|503 +Retry-After|Pending explicit ownership; no fake ACK|
| Old sslip/IP HTTP alias |308 formal URL|n/a|V3 after redirect/proxy|

The old general location MUST point to fixed124.220.53.183; target's only old-origin bridge MUST match exact financial callback paths. Those paths MUST terminate at source127.0.0.1:5001 rather than catch-all. This directed graph has no forwarding cycle. Do not activate a target WeCom-back-to-source hold variant together with this source forward-all variant: that combination loops. Use the included formal-ready pair only after WeCom ownership switches.

We keep request method/body unchanged; no retry-next-upstream for generic mutations. Verified TLS with formal SNI, no insecure flags. Callback/auth query logs are disabled on the candidate source server. Caller-supplied forwarding headers are not credentials; target trusted-proxy configuration must not confer identity based on them.

The two old internal callbacks currently point at5013, but read-only `ss -lnt` found no5013 listener. Candidates return retryable503 consistently rather than falsely accepting or reintroducing old business writes. Stop legacy producers or explicitly settle this unsupported owner before cutover; retries are not guaranteed indefinitely.

## Actual source shop contract

Source `extensions/commerce/commerce/api.py:661–689` owns GET/POST `/api/wechat-shop/notify`. `wechat_shop_service.py:136–199` verifies query SHA1 signature, takes `order_info.order_id`, records event, queries Provider order detail and upserts source order. V3 `/api/public/wechat-shop/callbacks/refund` is a different contract, not a replacement. The exact bridge preserves current validation but leaves a temporary old runtime dependency and new source mutations. V3 takeover of ongoing shop order events is NOT complete until a native owner adapter exists, or Provider ingestion is explicitly paused. Continue audited source delta/reconciliation during the temporary bridge; do not imply the first snapshot covers later shop events.

Do not bridge `/api/wechat-pay/notify` or `/api/alipay/notify`: source typed compatibility routes report local_only and are not the verified H5 payment notification path. Old `/api/alipay/return` reports fake; it is not payment evidence.

## Validation and activation prerequisites

- Actual source nginx parsed the candidate isolated main file successfully (`nginx -t -c /root/aicrm-source-frontdoor-candidate-20260911/nginx.conf`). It does not include/modify active sites. Revalidate final exact files after any edit.
- Actual target Caddy2.6.2 parsed the paired candidate. Staged path `/root/aicrm-source-proxy-pair-candidate-20260911/Caddyfile` root0600.
- Source→target IP with id-dev SNI `/healthz`: TLS verification0, HTTP200. Formal SNI currently fails TLS handshake: **do not activate source proxy until target formal certificate is live and verified**. Previously copied certificate is staged, not active.
- Source nginx inventory showed only formal site and `aicrm-sslip.conf`. Both must be replaced together. Backend5001/5002 currently bind127.0.0.1;5013 absent. Recheck external ports/firewall and all aliases at cutover, because config files cannot stop separate public app ports.

Activation owner sequence (not executed): freeze old intake/schedulers and settle/freeze old effect backlog; apply target formal TLS/ready routing and grouped WeCom config with target/source worker ownership coordinated; verify formal TLS directly using fixed IP; install source formal+sslip pair after backups and `nginx -t`; reload source; verify cached-DNS reads through source and direct target use same release; change DNS; verify actual signed challenges/controlled receipts. No real checkout/provider mutation is part of this candidate test.

Read-only checks before activation:
```
curl --resolve www.youcangogogo.com:443:124.220.53.183 https://www.youcangogogo.com/healthz
curl --resolve www.youcangogogo.com:443:150.158.82.186 https://www.youcangogogo.com/healthz
```
Do not use `-k`. After source reload both must report the target release. Verify old callback GET405, shop unsignedGET403, not acceptance; real signed receipt verification remains separate. Historical callback bridge must retain source cert validity (current expiry2026-10-17) and be removed after terminal reconciliation. Do not assume old HTTP-01 renewal works after DNS moves.

Rollback before target accepts mutations: quiesce target and restore paired backups/ownership. After any target mutation/effect, never blindly restore source general5001 or old DB: freeze, reconcile data/receipts then choose one owner. Do not operate target and source checkout paths simultaneously.


## 已准备的正式 TLS 启动配置

`Caddyfile.formal-bootstrap.candidate` 使用已核对源证书作为 DNS 尚未切换时的启动证书，避免 ACME 挑战仍到旧机而导致目标 TLS 无法先建立。目标 `/etc/caddy/formal-cutover-20260911/` 已安装独立候选：目录 root:caddy0750，证书/私钥/候选配置 root:caddy0640；以实际 caddy 用户执行 validate 成功。此目录目前未被活动 Caddy 配置引用，443 与现有业务均未改变。

切流时先使用 bootstrap 候选完成目标正式 Host/TLS 实测，再切源固定IP代理，最后由用户改 DNS。DNS 权威和公共解析均稳定指向目标后，采用本目录 `Caddyfile.formal-ready.candidate`（不指定手工证书）交给 Caddy 自动签发与续期；检查实际正式域名证书、链、到期日和 HTTPS 业务读回。自动签发未成功则回到 bootstrap 候选并保留失败证据，不宣称完成证书接管。暂存源证书有效至2026-10-17，不应长期依赖手工副本。
