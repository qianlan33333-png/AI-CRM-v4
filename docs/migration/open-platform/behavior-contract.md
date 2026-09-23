# Open platform behavior contract

## Classification

```text
OneID: resolves scoped external identity and reads canonical customer; never provisions, links, or merges.
Persistence: Access machine clients/grants/audit use one local PostgreSQL Unit of Work.
External Effects: no new effect writer. Every business command reaches its existing domain Port/UoW/River/EER path.
```

## Frozen sources and V3 disposition

| Frozen dd8d60d source | Behavior retained | V3 seam | Status |
| --- | --- | --- | --- |
| `platform/platform_foundation/auth_platform/{api,models,profiles,service,repository,credentials,client_authentication}.py` | Basic/form `client_credentials`, 1800 default, 60–3600 TTL, audience/scope/CIDR validation and disabled/expiry rejection | `internal/access/app.MachineService`, `internal/access/store`, migration 0096 | Go-equivalent implemented; composition key and route adapter pending |
| `platform/admin_config/api_clients.py` | client list/create/one-time secret/rotate/enable/disable and masked recent-use state | Access machine client admin API | Implemented; V3 host page pending |
| `platform/admin_config/direct_api_key{,_api}.py` | one fixed Bearer direct key limited to `external_read` | `direct_external_api_key` template in Access | Implemented; V3 host page pending |
| `channels/integration_gateway/{api,mcp,dispatch}.py`, `mcp_tool_catalog.py`, `mcp_composition.py` | JSON-RPC 2024-11-05, initialize/list/call, only resolve_customer/get_customer_context/get_recent_messages | `internal/openplatform/http`, composition-owned executor | Protocol/auth/tool catalog implemented; canonical customer/archive Port adapter pending |
| `crm/identity_contact/api.py` | scoped identity resolve without implicit customer creation | Identity `port.Resolver` | Adapter pending |
| `extensions/{archive,message_archive,forms,radar,commerce}/...` | seven external GET reads | owner Port adapters | Adapter pending |
| `extensions/ai/ai_audience_ops/{external_api,api}.py` | 11 legacy paths and audience catalog/package/run paths, including publish and retired webhook gate | existing segment/automation/AI Ports | Adapter pending; no raw SQL route will be added |
| `automation/automation_engine/group_ops/api.py`, `extensions/ai/ai_assist/api.py`, `extensions/hxc/operation_cycles/api.py` | broadcast, AI campaign, and operation-cycle routes | existing GroupOps/AI Assistant/OperationCycle Ports | Adapter pending; existing approval, idempotency, River, and Provider gates remain authoritative |

## Route inventory

`internal/openplatform/http/inventory.go` has the authoritative 56 `method + path + capability` records transcribed from the approved `03a-machine-route-inventory.md`. The HTTP mux registers every record explicitly; it does not open a prefix or an admin route by accident. A record is complete only after the composition executor maps it to an existing owner Port and its behavior test passes.

## Archive historical compatibility facts

- Donor chat reads used the archived row's `unionid` and its row/raw-payload `group_name`; neither is reconstructed from current Identity or a group directory.
- 0098 adds one Archive-owned row per retained message when the imported source contains either fact. Its `TEXT` columns are protected by Archive table ownership and the existing authorized Archive read boundary; they are not represented as field-level ciphertext. It carries no second message stream.
- `migrate-message-archive extract` reads donor `archived_messages` only in an explicit repeatable-read, read-only transaction, maps row `unionid` and row/raw-payload `group_name`; its actual SDK wrapper contributes only the validated `decrypted_message`, while the full protected wrapper contributes a SHA-256 source fact, then writes a `0600` offline manifest. Import includes those values and the wrapper digest in the source-row digest, receipt/replay comparison, target equivalence check, and reconcile path. A source conflict is quarantined; a stored projection mutation makes reconcile fail. Source rows without either field intentionally return empty fields.
- `migrate-survey-v2` already extracts donor `questionnaire_submissions.unionid` into its sealed source snapshot. 0099 preserves it only as a Survey-owned historical external-read projection, with an independent projection digest and replay/reconcile check. It never promotes a missing source scope into verified OneID evidence, does not resolve/provision/link a Customer, and deliberately has no projection row where the source value is absent.
- `GET /api/external/questionnaire-submissions` first resolves the request through scoped OneID. Its Survey Port then unions two disjoint owner reads: imported rows use only the 0099 historical UnionID projection, while V3-native rows use only that resolved canonical `customer_id`. A historic union never becomes an Identity fact. For a supplied legacy `questionnaire_id`, a source-map target is authoritative; an unrelated V3 questionnaire with the same number is an explicit conflict, not a numeric guess. Without a source map, the number selects the V3 questionnaire ID. Native rows in an imported questionnaire use its mapped donor ID in the compatibility response.
- Raw phone is returned only when needed for this sensitive donor projection. The composition adapter calls the existing Identity Directory reader and appends a redacted existing Access machine-audit fact in the same Unit of Work; the phone is absent from audit data and logs.

## Security invariants

- Secrets are shown only in create/rotate responses, are stored as Argon2id hashes, and do not appear in summaries or audit details.
- `auth_version`, enabled state, expiry, audience, requested-scope subset, and source CIDR are checked against PostgreSQL on every machine request. A JWT signature alone is insufficient.
- The direct key only accepts the fixed readonly template. A machine principal has capabilities; it cannot become an Access `super_admin` or use a payload `operator` value as authority.
- Untrusted `X-Forwarded-*` headers are ignored. TLS is required unless the remote address belongs to an explicitly configured trusted-proxy CIDR and asserts HTTPS.
- Historical import receipts have no secret/token fields. A sealed source snapshot has its own batch identifier; the frozen donor revision is provenance only. Source records replay across batches only when their source scope, identity, and digest agree; drift is rejected. Local legacy customer-id scope is pending/excluded until trusted Identity evidence maps it, so reissue and activation cannot turn coincident numeric IDs into authorization. Non-verifiable source credentials are recorded `inactive` or `reissue_required`; they never become live credentials.

## 56-route implementation evidence

This matrix records the current executable state rather than treating a registered mux route as complete. At this checkpoint, four routes reach an owner Port with a real PostgreSQL journey (the Radar mapping still needs its final authenticated HTTP-combination fixture), five routes are only partially equivalent, and 47 routes remain unmigrated. `Not migrated` means the machine executor returns no business success for that route and it remains delivery work in this PR.

| Route | Capability | Current state | Evidence or remaining work |
| --- | --- | --- | --- |
| `GET /mcp` | `mcp_read` | 已真实接通 | Open Platform HTTP 的 JSON-RPC tools/list；冻结三工具 schema。 |
| `POST /mcp` | `mcp_execute` | 已真实接通 | 三工具调用经 OneID、Customer/Archive/Timeline Port；机器 scope/capability 均验证。 |
| `GET /api/identity/resolve` | `identity_resolve` | 已真实接通 | 受可信配置 scope 限定的 OneID Resolve；不建客、不合并。 |
| `GET /api/external/chat-records` | `external_read` | 部分接通 | Archive owner Port applies canonical customer + trusted external-user scope; 0098 preserves donor historical `unionid`/`group_name` as a protected one-to-one projection and CLI reconcile detects drift. PG journey passes; final frozen machine HTTP request/response fixture remains. |
| `GET /api/external/questionnaire-submissions` | `external_read` | 部分接通 | Survey owner Port has real PG/race evidence for imported UnionID rows plus canonical V3-native submissions, mixed descending pagination, unrelated-customer exclusion, and source-ID/V3-ID collision rejection. Composition Host preserves the donor request/response envelope after OneID; final authenticated machine HTTP fixture remains. |
| `GET /api/external/radar-clicks` | `external_read` | Not migrated | Radar-owned logical-click projection and approved identity response boundary remain. |
| `GET /api/external/radar-links` | `external_read` | 已真实接通（最终协议组合待测） | Radar owner Port uses donor-compatible descending `radar_id` keyset and includes retained disabled mappings; Composition is bound and PostgreSQL journey passes. Final authenticated machine HTTP combination fixture remains. |
| `POST /api/external/ai-audience/spec/dry-run` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/spec/apply` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/spec/publish` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/packages/{package_key}/archive` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/templates/preview` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/templates/apply` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/simple/preview` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/simple/apply` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/simple/{package_key}/activate` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/simple/{package_key}/archive` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/external/ai-audience/e2e/run` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/automation/group-ops/broadcast` | `group_broadcast_execute` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/external/orders` | `external_read` | 部分接通 | V3 已持久化查询与旧列表 envelope 已接通；付款时间/paid filter 与 provider 选择仍需 Order owner Query Port，owner_userid、多客户 owner_scope 需 #171 LocalOwner 鉴权 Adapter。 |
| `GET /api/external/orders/{order_no}` | `external_read` | 部分接通 | 单 customer_id + corp_id 走 SQL CustomerID+OrderRef Port；provider 选择、owner_userid、多客户范围仍待相应 Order Port 与 #171 LocalOwner Adapter。 |
| `GET /api/external/users/resolve` | `external_read` | 部分接通 | 旧 user envelope 与 OneID/customer 投影已接通；owner 字段的 LocalOwner 优先语义待 #171 Adapter。 |
| `POST /api/ai-assist/external/campaigns` | `campaign_draft_create` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai-assist/external/campaigns/{campaign_code}` | `campaign_status_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai-assist/external/campaign-preparations` | `campaign_preparation_create` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai-assist/external/campaign-preparations/{preparation_id}` | `campaign_preparation_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai-assist/external/campaign-preparations/{preparation_id}/commit` | `campaign_preparation_commit` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/schema-catalog` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/packages` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/packages/{package_id}` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages/{package_id}/versions` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages/{package_id}/preview` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages/{package_id}/publish` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages/{package_id}/pause` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages/{package_id}/archive` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages/{package_id}/refresh` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/ticks/incremental` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/ticks/daily` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/source-dirty` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/packages/{package_id}/outbound-subscriptions` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/packages/{package_id}/outbound-subscriptions` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `PATCH /api/ai/audience/outbound-subscriptions/{subscription_id}` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/ai/audience/outbound-subscriptions/{subscription_id}/pause` | `external_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/packages/{package_id}/runs` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/packages/{package_id}/members` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/packages/{package_id}/events` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/packages/{package_id}/external-effects` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/ai/audience/health` | `external_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/operation-cycles/reports` | `operation_cycle_report_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/operation-cycles/runner/heartbeat` | `operation_cycle_runner_heartbeat` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/operation-cycles/action-requests/claim` | `operation_cycle_action_claim` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/operation-cycles/action-requests/{request_id}/events` | `operation_cycle_action_event_write` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/operation-cycles/context-index` | `operation_cycle_context_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `GET /api/operation-cycles/strategies/{strategy_key}/context` | `operation_cycle_context_read` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
| `POST /api/operation-cycles/strategy-change-proposals` | `operation_cycle_strategy_propose` | Not migrated | Frozen machine route is registered and authenticated, but its owner Port adapter and behavior journey remain to be implemented. |
