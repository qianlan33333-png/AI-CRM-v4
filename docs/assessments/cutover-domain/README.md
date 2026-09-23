# Callback/domain cutover audit — 2026-09-11

Classification: identity scopes and provider effects involved; existing Identity/Outbound owners remain authoritative. Preparation only; no reload/DNS/provider write.

## Candidate status
Target root0700 `/root/aicrm-domain-cutover-candidate-20260911`: `Caddyfile` temporarily holds exact old WeCom callbacks on source; `Caddyfile.wecom-ready` is intended formal takeover. Both passed actual Caddy 2.6.2 validation. Active config unchanged. Exact old payment/refund POST paths alone remain bridged to fixed source IP via verified HTTPS with formal SNI/Host. No old checkout/admin prefix fallback. Source cached DNS/IP writes must be frozen independently.

TLS chain/key staged root0600 under tls/, public keys match. SAN www.youcangogogo.com only; valid 2026-07-19 11:30:13 UTC to 2026-10-17 11:30:12 UTC. Root staging is inaccessible to caddy user: activation must install a private service-readable directory and manual tls directive or obtain certificate after DNS points target. No issuance/activation occurred. Old HTTPS bridge needs valid source TLS; after DNS changes old HTTP-01 renewal cannot be assumed.

## WeCom grouped adoption
Both versions accept GET/POST /wecom/external-contact/callback and /api/wecom/events. Sorted SHA1 signature, AES256CBC/PKCS7 block32 envelope, CorpID validation, 300-second past/60-second future window match. GET echoes decrypted challenge; POST durable inbox then encrypted success. Source main/ingress six grouped values match. Target CorpID/contact secret match, but AgentID/application secret/callback token/AES differ. Current Config release has no core override. Move all six together; preserve target DB/vault/HMAC/session keys and worker switches.

prepare-wecom-environment.py creates an exclusive root0600 candidate and checks active Config overrides; never activates. It emits systemd EnvironmentFile quoted syntax, not shell source. Recheck overrides before activation; use Config Owner for changes.

Evidence: source channels/channel_entry/api.py, application.py, wecom_crypto.py; V3 internal/wecom/handler.go, callback.go, crypto.go; cmd/aicrm/config.go and runtime_config_apply.go. Payment URLs in composition.go; verification in internal/payment/http/handler.go.

## Actual queue observation
WeCom inbox 33,794 succeeded, open 0. Current open effects 0; unknown/reconciliation-required jobs 0. Three old unknown media attempts have current cancelled jobs; do not retry.
Internal backlog NOT drained: 183,117 pending +256 retryable consumers; outbox 22,392 pending (11,582 settled +10,810 completed). Consumers include welcome media, questionnaire tags, group operations, broadcast, automation and external push continuations/settlements. Retryable is identity settlement; 12 payment automation consumers blocked. Do not bulk replay or silently mark complete. Zero current effect jobs does not prevent these continuations generating effects.
Run drain-readonly.sql again at cutoff; aggregate-only READ ONLY transaction. Do not expose payloads/identity keys.

## Switch gates and rollback
1. Freeze source new business/scheduler intake; brief callback POST intake returns retryable failure rather than fake success. Drain only explicitly accepted work and record cutoff.
2. Isolate existing continuation backlog through old owner policy. Stop old consumer/effect workers before V3 owns callbacks. Do not delete or replay old acknowledged inbox into V3: receipt namespaces differ. Legacy payment callbacks may enqueue followups; maintain separate explicit ownership and delta reconciliation.
3. Require no processing inbox/open effects/unknown effects plus explicit frozen backlog disposition. Inventory timers and services at activation.
4. Back up target env/drop-ins/Caddy; stop API and all affected workers. Apply grouped env override preserving existing roles/settings. Check runtime overrides. Install service-readable TLS if needed and validate final config. Start target and ready Caddy variant only with coordinated owner switch.
5. Verify signed GET challenge and controlled real provider event/receipts. Unsigned GET or syntax validation is not provider acceptance. Source WeCom workers remain stopped. Old payment bridge retires after pending old transactions/refunds and later deltas reconcile.
6. Before V3 accepts writes/effects, quiesce and restore saved config to roll back. After any V3 write/effect, freeze and reconcile receipts/data delta before choosing owner; never restore old DB or blindly reroute/replay. Unknown outcomes, duplicate effects or scope/config mismatch stop the switch.

Observed target group: aicrm, aicrm-effects-worker, aicrm-excel-batches, aicrm-wecom-worker, plus customer-sync/dashboard timer units. Re-inventory rather than blind service restart script. No activation script executed.
