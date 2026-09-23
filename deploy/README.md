# AI-CRM v3 deployment

The production process listens only on `127.0.0.1:8080`. Caddy owns public
ports 80/443 and proxies `id-dev.youcangogogo.com` to that loopback listener.
PostgreSQL 16 is the only runtime data dependency.

## Runtime layout

- immutable releases: `/opt/aicrm/releases/<40-char-git-sha>`
- active symlink: `/opt/aicrm/current`
- secrets and runtime settings: `/etc/aicrm/aicrm.env` (`0640`, never in Git)
- API: `aicrm.service`
- migrations: `aicrm-migrate.service` (oneshot before a release restart)
- Excel preparation and observation (only when both pre-provisioned component files exist): `aicrm-excel-batches.service`, bound to `127.0.0.1:8791`
- Automation Operations defaults: `aicrm-automation-bootstrap.service` (idempotent oneshot after API and River readiness)
- WeCom inbox: `aicrm-wecom-worker.service` plus external systemd timer
- durable River jobs: `aicrm-effects-worker.service` (External Effects and customer directory queues)
- Daily customer reconciliation creation: `aicrm-customer-sync-daily.service` plus the 02:30 Asia/Shanghai systemd timer

The app has `api`, `worker`, and `effects-worker` roles. The oneshot worker runs
one bounded callback-inbox claim or creates a scheduled customer-sync run. The
long-running effects worker is the single River runtime for both durable queues;
there is no ticker or scheduler inside a domain package.

### Controlled WeChat Pay confirmation recovery

`payment-reconcile` is an exceptional, one-shot operations role for a known
existing V3 Payment whose signed WeChat Pay query must repair a missing paid
confirmation. It is not a service or timer. Run it only on the sole production
host with existing operations authorization, after the deployed release has
passed its normal readiness gate. A local `pending_payment` status is not proof
that the Provider did not succeed: first use the approved read-only operations
path to identify the exact numeric Payment ID for the authorized case.

Do not source `/etc/aicrm/aicrm.env` in a shell or copy values from it. Let
systemd load its native EnvironmentFile format and run the exact installed
binary as `aicrm`:

```bash
sudo systemd-run --wait --collect --pipe --service-type=exec \
  --unit="aicrm-payment-reconcile-<payment-id>" \
  --property=User=aicrm \
  --property=Group=aicrm \
  --property=WorkingDirectory=/opt/aicrm/current \
  --property=RuntimeMaxSec=60s \
  --property=EnvironmentFile=/etc/aicrm/aicrm.env \
  --property=EnvironmentFile=-/opt/aicrm/current/release.env \
  /usr/bin/env AICRM_ROLE=payment-reconcile AICRM_PAYMENT_RECONCILE_ID=<payment-id> \
  /opt/aicrm/current/bin/aicrm
```

The role accepts only a positive numeric Payment ID; it cannot take a merchant
order number, create a payment, issue a refund, or call a WeChat Provider write
endpoint. It performs the normal signed Provider *read* and then reuses the
same Payment/Order transaction and paid-event consumers as a callback. A valid
paid result can therefore queue ordinary configured post-payment work (for
example, a product push); it must be verified and delivered by the normal
effects worker, not inferred from the command exit. Its completion log contains
only Payment ID, final status and release SHA—never transaction, customer,
merchant-order, callback body, certificate or credential data.
`AICRM_ROLE` and `AICRM_PAYMENT_RECONCILE_ID` are deliberately assignments on
the executed `/usr/bin/env` command—not `systemd-run --setenv` values—so any
same-named legacy EnvironmentFile value cannot turn this one-shot into an API
listener or change its target.

Official WeCom tag directory writes stay disabled until an operator starts the
`ci` workflow on `main` with **Activate official WeCom tag catalog writes**.
That deployment-owned action changes only
`AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED` and its explicit
`catalog-write-authorized` acknowledgement. Before replacing either setting it
requires exactly one active catalog read gate, WeCom gate, outbound gate, and
contact credential in the existing runtime environment. It restarts the API and
effects worker, verifies the installed release through `/readyz`, and restores
the prior environment if restart or readiness fails. It never enables the read,
WeCom, outbound, or credential prerequisites itself.

## Staging-built release

The normal release path builds once on the Linux staging host and promotes the
same package after merge:

1. Run fast local checks only, then create a verified local v3 bundle and set
   `AICRM_SOURCE_BUNDLE`. The staging host receives that bundle over the pinned
   SSH connection; it never clones GitHub. `deploy/build-release-on-staging.sh`
   invokes `scripts/run-donor-view-consumers.sh release-fast` on Linux amd64
   under a single-flight staging build lock.
2. Install the resulting package to staging `49.232.57.128` and complete the
   affected capability's synthetic API/browser/readback and rollback checks.
3. Put the staging SHA/tree and receipt in the PR. GitHub PR checks validate
   the receipt, current tree, governance and merge consistency; they do not
   repeat long lanes by default.
4. After merge, compare the merged tree with the staging tree and run
   `deploy/promote-staging-release.sh` for the same archive digest. The staging
   receipt must bind repository, commit, tree, package SHA-256, capability and
   business readback. A mismatched tree or digest stops promotion. Production
   is never rebuilt from a different checkout.

Staging and production use separate release locks. An unrelated disabled
Provider is recorded as `external_config_unavailable`; it blocks only when the
capability declaration lists that Provider as a required dependency.

The local deploy helper uploads the archive and installer, then invokes
`deploy/run-release-as-root.sh`. That root wrapper opens fd 9 itself and
exports `AICRM_RELEASE_LOCK_HELD=1`, so sudo cannot close the descriptor that
the success observer needs. Do not call `sudo bash install-release.sh` directly.

The production Actions deploy remains a break-glass path and requires both
`AICRM_ENABLE_ACTIONS_DEPLOY=true` and `AICRM_CLOUD_DEPLOY_BREAKGLASS=true`.
Changes to this deployment path automatically use the complete GitHub CI lanes;
ordinary feature PRs keep the shorter staging consistency gate.

## Controlled release

The `deploy` job runs only after the required `check` job succeeds on `main` and
the repository variable `AICRM_ENABLE_ACTIONS_DEPLOY` is exactly `true`. The
variable is unset by default, so a normal merge completes CI without SSH upload,
installation, runtime configuration, or production verification. After the
approved PRs are merged, use the authorized local full-package release process.

When the explicit Actions opt-in is enabled, the job builds static Linux
binaries, uploads one archive over pinned-host SSH and runs
`install-release.sh`. The installer requires every release-owned migration,
including `0124_operation_excel_batch_lifecycle.sql`, applies forward-only
migrations, atomically switches `/opt/aicrm/current`, restarts the API and
checks `/readyz`. When `/etc/aicrm-excel/config.json` and
`/etc/aicrm-excel/service.env` were already provisioned together, it also
restarts the loopback Excel component and verifies its authenticated `/health`
endpoint before applying the migration. The installer never invents component
configuration, source SQL, or provider permissions. A configured component
must use `EXCEL_BATCH_URL=http://127.0.0.1:8791` and a token exactly matching
the component service file; the installer compares them without logging either
value and rejects an unconditional `coverage_sql` query.
If migration, restart or readiness fails, the active symlink and service return
to the previous binary. Database migrations are forward-compatible and are not
destructively rolled back.

After the API and durable worker are ready, every release runs the Automation
Operations semantic bootstrap. It creates only paused 7/30/90-day canonical
customer audiences and queues their initial River snapshots. It does not copy
legacy rows or create outbound effects, and it preserves later operator edits.

Required GitHub Actions secrets:

- `DEPLOY_HOST`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`
- `DEPLOY_KNOWN_HOSTS` (the verified ED25519 host-key line, not a fingerprint alone)

## Provider boundary

`AICRM_WECOM_ENABLED=false` and `AICRM_WECOM_CUSTOMER_SYNC_ENABLED=false` are safe defaults. Enabling WeCom requires the
Corp ID, Agent ID, application Secret, callback Token, callback EncodingAESKey
and a separate context signing key to be present together. Customer directory
sync additionally requires its own customer-contact Secret. Alipay has no live
runtime configuration in this release.

AI Assistant has three independent switches. `AICRM_AI_ASSISTANT_UI_ENABLED`
serves the frozen two-level review UI; signed machine intake additionally needs
an integration key, a 32-byte-or-longer secret and an internal actor ID. Real
private-message dispatch requires all of
`AICRM_AI_ASSISTANT_DISPATCH_ENABLED=true`,
`AICRM_OUTBOUND_PROVIDER_ENABLED=true`, enabled WeCom contact credentials and
`AICRM_AI_ASSISTANT_PROVIDER_PERMISSION=private-message-authorized`. Approval
creates durable External Effects; Provider acceptance is shown separately from
delivery proof, and ambiguous outcomes require the fenced reconciliation API.

The phone migration command never connects to the source host. Export a minimal
snapshot through a separately authorized read-only channel, compute its SHA-256,
then run `inspect`, `dry-run`, `apply --confirm-apply`, and `reconcile` in order.
Do not place snapshots, source credentials, raw phones, external user IDs, or
command output containing them in Git or structured logs.

## Acceptance

```bash
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
curl --fail --silent --show-error https://id-dev.youcangogogo.com/readyz
```

### Capability-scoped staging readback

Staging acceptance is capability-scoped. Write the changed modules, required routes, and required provider dependencies into a capability manifest, then validate the readback with:

```sh
python3 scripts/validate-staging-capability.py capability.json readback.json
```

An unavailable unrelated provider is recorded as `external_config_unavailable`; a failure on a declared route or provider remains blocking. For example, a Payment checkout change does not require the Distribution product route when WeChat Distribution is disabled on staging.

验收辅助脚本不生成 accepted receipt，也不执行 HTTP 请求。required_routes 是非空路径字符串数组；observations 必须覆盖全部必需路由/Provider，并记录 status 与业务断言 business_verified。HTTP 200 本身不能替代业务验收。支付板块默认使用 acceptance_mode=virtual：在预发布用本地虚拟支付适配器或确定性数据库事实验证待支付订单重购、幂等、已支付拦截和状态回读，不连接真实支付、不扣款、不等待 Provider 回执。只有明确需要 Provider 集成的板块才使用 acceptance_mode=live。

Payment staging uses a virtual acceptance mode by default. It verifies pending-order replacement, idempotency replay, preservation of the old order, paid ownership blocking, and business readback with a local adapter or deterministic database facts. It never charges money or requires merchant credentials. Provider callbacks, real payment, and reconciliation remain a separate explicitly requested live acceptance.

All staging external effects use virtual acceptance by default. Payment, WeCom, group messaging, automation, webhooks, settlement, and other Provider writes are not connected on staging. Validate local intent, idempotency, queue acceptance, replay, state transitions, failure classification, and business readback with `effect_mode=virtual`; classify unconfigured external services as `external_config_unavailable`. Live Provider testing is an explicit separate operation.
