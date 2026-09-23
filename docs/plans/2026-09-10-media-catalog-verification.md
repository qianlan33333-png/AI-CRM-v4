# Media catalog local verification

This verification used only the dedicated local PostgreSQL 16 database created
for this branch. It did not connect to production or use Provider credentials.

```sh
AICRM_DATABASE_URL='postgres://qianlan@localhost/aicrm_daily_media_refresh_0910?sslmode=disable' \
  go test ./internal/media/store \
  -run 'TestPostgreSQL(ExcelCoverReuseDoesNotReviveDisabledOrLoseFrozenBytes|MediaMaterialAcceptanceSharesMutationTransaction)' \
  -count=1
```

Observed result on 2026-09-10: `ok .../internal/media/store`.

The test creates and drops its own random schema. It verifies 12 concurrent
same-content cover uploads resolve to one enabled image, a disabled image is
not revived, a repeated idempotency key remains bound to its original frozen
image, a changed payload conflicts, frozen bytes remain readable, GIF selection is
rejected, invalid source metadata is reported as a per-source failure, and the
scan advances past it. It also proves Media creates a source snapshot and
accepts durable preparation in its existing transaction when a material is
created enabled or re-enabled; a rejection rolls that Media mutation back.

After the Media-preparation HTTP and AI/automation consumer wiring changes, the
same isolated database command was repeated on 2026-09-10 and passed. The
related package regression command also passed locally:

```sh
go test ./internal/media/store ./internal/media/http ./internal/wecom/adapter \
  ./internal/outbound ./cmd/aicrm
```

## Real Host, PostgreSQL, Chromium, and Python XLSX journey

The browser journey uses only the dedicated local PostgreSQL database
`aicrm_daily_media_refresh_browser_0910`. It starts the released local Python
component from `components/excel-batches/batches.py` on a random loopback port
with a temporary SQLite database, generates a genuine XLSX with the five
required columns plus optional `分层`, and starts only a local fake WeCom
upload endpoint. The Python component parses `/prepare`; it is not replaced by
a fixed JSON fixture. No real WeCom endpoint or production database is used.

```sh
PATH=/tmp/excel-node-cache/_npx/852433782da5924e/node_modules/.bin:$PATH \
AICRM_DATABASE_URL='postgres://qianlan@localhost/aicrm_daily_media_refresh_browser_0910?sslmode=disable' \
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 \
go test ./cmd/aicrm -run '^TestPostgreSQLMediaRefreshChromiumJourney$' -count=1 -v -timeout=100s
```

Observed result on 2026-09-10: PASS. The journey signs into the real Host,
checks its page title and scroll container, shows the Media refresh panel and
the explicit historical missing-source prompt, uploads an image, reads its
first local-fake credential, accepts a manual refresh, reads the replacement
credential, imports the real Python-parsed XLSX into an unapproved Excel draft,
uploads its cover, and proves that no private-message intent or submitted plan
was created. It also verifies the persisted parsed content and current
`fixture-media-2` credential in PostgreSQL.

The latest forced run emitted
`--- PASS: TestPostgreSQLMediaRefreshChromiumJourney` and
`media_refresh_chromium: PASS`. Its real Python input is generated with
`openpyxl` and has the five required columns `unionid`, `话术`, `小程序 path`,
`发送人 userid`, and `标题`, plus the optional `分层` column. It verifies both
parsed rows after the component’s actual `/prepare` request; no Excel response
is stubbed.

Screenshot (opened and visually checked after the final run): `/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/aicrm-daily-media-refresh-browser-artifacts/media-refresh-chromium.png`

The required forced browser-group command was also run with the same Node 24
toolchain and `AICRM_REQUIRE_CHROMIUM_JOURNEY=1`:

```sh
python3 scripts/dev_preflight.py browser --group all
```

Its evidence directory is
`/private/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/aicrm-preflight-8akgo8sp`.
The final post-GroupOps group evidence directory is
`/private/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/aicrm-preflight-ibhdfk04`.
With the new source-only GroupOps preparation contract, the final run passed
`TestPostgreSQLExcelBatchesChromiumJourney` in 6.35s,
`TestPostgreSQLGroupOpsStandardHostChromiumJourney` in 2.43s, and
`TestPostgreSQLMediaRefreshChromiumJourney` in 8.06s. The invoked `cmd/aicrm`
Go package reported `PASS` in 52.448s. The Excel and Media journeys are both
forced by `AICRM_REQUIRE_CHROMIUM_JOURNEY=1` and neither has a platform skip
path.

Two pre-existing journeys are intentionally skipped on macOS by their own
Linux-CDP guards: `TestPostgreSQLOpenPlatformV1ChromiumJourney` and
`TestPostgreSQLProductExternalPushChromiumJourney`. Their skip output says
“Chromium CDP journey requires Linux CI; the PostgreSQL Composition preflight
runs separately.” The preflight verifier treats those skips as non-passing, so
the command exits 1 on this macOS host despite the package-level `PASS`. This
is evidence of all locally executable journeys, not a zero-skip full-preflight
claim.
