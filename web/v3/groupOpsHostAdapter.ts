// V3 Host adapter for the byte-derived Group Ops presentation. It owns only
// authenticated transport and DTO projection; plan, node and directory facts
// remain in internal/groupops and the existing WeCom read adapter.
import { openGroupPicker, type GroupPickerRecord } from './shared/ui/groupPickerAdapter';
import { installStaffPickerAdapter } from './shared/ui/staffPickerAdapter';
import { installMaterialPickerAdapter, type MaterialPickerLoadRequest, type MaterialPickerRecord, type MaterialType } from './shared/ui/materialPickerAdapter';
import { openContentComposer, openReadonlyContentPresentation, type ContentComposerResult } from './shared/ui/contentComposer';
import type { ContentMaterialKind, ContentMaterialRecord } from './shared/ui/contentPresentation';
import { installCommittedTextSearch } from './shared/ui/committedTextSearch';
import { mountPageHeaderActions, setPageHeaderActionDisabled } from './shared/ui/pageHeaderActions';

declare global {
  interface Window {
    AICRMPageHeaderActions?: {
      mount: typeof mountPageHeaderActions;
      setDisabled: typeof setPageHeaderActionDisabled;
    };
  }
}

installCommittedTextSearch();
// The byte-derived standard script owns the GroupOps list state. Give it this
// narrow V3 bridge so it can mount actions into the existing shell topbar
// without recreating a page header or importing a domain command.
window.AICRMPageHeaderActions = {
  mount: mountPageHeaderActions,
  setDisabled: setPageHeaderActionDisabled,
};

type Json = Record<string, any>;
const base = "/api/admin/automation-conversion/group-ops";
const revisions = new Map<number, number>();
const planGroupViews = new Map<number, Json[]>();
const planGroupDirectoryViews = new Map<number, Map<string, GroupPickerRecord>>();
const planSummaryViews = new Map<number, Json>();
type InitialDetailReadKind = 'plan' | 'groups';
type InitialDetailReadEpoch = {
  id: number;
  generation: number;
  planClaimed: boolean;
  groupsClaimed: boolean;
  pairable: boolean;
  claims: number;
  detail: Promise<Json>;
  groups: Promise<Json[]> | null;
};
const initialDetailReadEpochs = new Map<number, InitialDetailReadEpoch>();
const detailReadGenerations = new Map<number, number>();
type GroupSelectionStep = { planID: number; kind: "add" | "remove"; reference: string; idempotencyKey: string; body?: Json };
type GroupSelectionOperation = { signature: string; steps: Map<string, GroupSelectionStep> };
const groupSelectionOperations = new Map<number, GroupSelectionOperation>();
let refreshedGroupTotal: number | null = null;
let openingGroupPicker = false;
let activeGroupPickerPlan: number | undefined;
function csrf(): string {
  return (
    document.cookie
      .split(";")
      .map((part) => part.trim())
      .map((part) => part.split("="))
      .find(([name]) => name === "aicrm_csrf" || name === "aicrm_admin_csrf")
      ?.slice(1)
      .join("=") || ""
  );
}
function key(): string {
  return `groupops-${Date.now()}-${crypto.randomUUID()}`;
}
function privateCreateRecoveryKey(value: unknown): string {
  const normalized =
    typeof value === "string" ? value.trim().toLowerCase() : "";
  if (!/^[a-z0-9._:-]{16,128}$/.test(normalized))
    throw new Error("创建恢复请求无效");
  return normalized;
}
function createRecoverySessionMarker(value: unknown): string {
  if (typeof value !== "string" || !value) throw new Error("创建恢复请求无效");
  return value;
}
function createRecoveryOptions(value: unknown): Json | null {
  if (!value || typeof value !== "object") return null;
  const source = value as Json;
  const stage = source.stage;
  if (stage !== "post" && stage !== "configuration")
    throw new Error("创建恢复请求无效");
  const result: Json = {
    stage,
    createKey: privateCreateRecoveryKey(source.create_key),
    configurationKey: privateCreateRecoveryKey(source.configuration_key),
    sessionMarker: createRecoverySessionMarker(source.session_marker),
    planName: String(source.plan_name || "").trim(),
    planType: source.plan_type,
  };
  if (
    !result.planName ||
    (result.planType !== "standard" && result.planType !== "webhook")
  )
    throw new Error("创建恢复请求无效");
  result.ownerID = requiredIdentifier(source.owner_userid, "创建恢复请求无效");
  if (stage === "configuration") {
    result.planID = requiredIdentifier(source.plan_id, "创建恢复请求无效");
    result.expectedRevision = requiredNumericPositiveInteger(
      source.expected_revision,
      "创建恢复请求无效",
    );
  }
  return Object.freeze(result);
}
function createRecoverySessionChangedError(recovery: Json): Error {
  const result = new Error("登录状态已变化，不能恢复这次创建");
  Object.assign(result, {
    groupOpsCreateRecovery: {
      stage: recovery.stage,
      plan_id: recovery.planID || 0,
      expected_revision: recovery.expectedRevision || 0,
      session_changed: true,
    },
  });
  return result;
}
function assertCreateRecoverySession(recovery: Json): void {
  if (!recovery.sessionMarker || csrf() !== recovery.sessionMarker)
    throw createRecoverySessionChangedError(recovery);
}
function createRecoveryError(error: unknown, recovery: Json): Error {
  const result =
    error instanceof Error
      ? error
      : new Error(errorMessage(error, "创建结果未确认"));
  Object.assign(result, {
    groupOpsCreateRecovery: {
      stage: recovery.stage,
      plan_id: recovery.planID || 0,
      expected_revision: recovery.expectedRevision || 0,
      session_changed: Boolean((error as Json)?.groupOpsCreateRecovery?.session_changed),
    },
  });
  return result;
}
function html(value: unknown): string {
  // The donor expects legacy new/updated counters; V3 returns a snapshot total.
  // Translate only the next notice belonging to a completed refresh/readback.
  if (refreshedGroupTotal !== null && value === "已刷新：新增 0 个，更新 0 个") {
    value = `已刷新 ${refreshedGroupTotal} 个群聊`;
    refreshedGroupTotal = null;
  }
  return String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ] || c,
  );
}
function errorMessage(error: unknown, fallback = "请求失败"): string {
  const message = error instanceof Error
    ? error.message
    : typeof error === "string"
      ? error
      : typeof (error as Json)?.message === "string"
        ? (error as Json).message
        : "";
  return message && message !== "[object Object]" ? message : fallback;
}
function responseMessage(data: Json, fallback: string): string {
  if (data?.code === "operations_conflict" || (data?.error as Json)?.code === "operations_conflict") return "计划状态、版本或配置不满足要求，请刷新后检查";
  const candidates = [data?.error_message, data?.message, data?.error, data?.code];
  for (const candidate of candidates) {
    if (typeof candidate !== "string") continue;
    const text = candidate.trim();
    if (text && text !== "[object Object]" && !/^[a-z][a-z0-9_]+$/i.test(text)) return text;
  }
  return fallback;
}
function isOperationsConflict(data: Json): boolean {
  return data?.code === "operations_conflict" || (data?.error as Json)?.code === "operations_conflict";
}
function announceDirectoryReadFailure(): void {
  // The detail renderer has a second, decorative owner-scoped group read.
  // Its failure must remain visible without changing the saved bindings. The
  // groups list owns its own explicit failed-read state instead.
  if (document.getElementById("group-ops-app")?.dataset.pageMode !== "detail") return;
  window.setTimeout(() => {
    const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
    if (notice) {
      notice.hidden = false;
      notice.classList.add("group-ops__notice--error");
      notice.textContent = "群目录读取失败，请重试；已绑定群仍可查看。";
    }
  }, 0);
}
function planIDFromAPIURL(value: string): number | null {
  const match = new URL(value, window.location.origin).pathname.match(/\/plans\/(\d+)(?:\/|$)/);
  if (!match) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}
async function nativeRequest(url: string, options: Json = {}, onOperationsConflict?: (planID: number) => void): Promise<Json> {
  const headers = new Headers(options.headers || {});
  headers.set("Accept", "application/json");
  if (options.body !== undefined)
    headers.set("Content-Type", "application/json");
  if (options.method && options.method !== "GET") {
    headers.set(
      "Idempotency-Key",
      typeof options.idempotencyKey === "string"
        ? options.idempotencyKey
        : options.privateCreateRecoveryKey || key(),
    );
    const token = csrf();
    if (token) headers.set("X-CSRF-Token", token);
  }
  const response = await fetch(url, {
    method: options.method || "GET",
    headers,
    credentials: "same-origin",
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal && typeof options.signal === "object" ? options.signal as AbortSignal : undefined,
  });
  const raw = await response.text();
  let data: Json = {};
  try {
    data = raw ? JSON.parse(raw) : {};
  } catch {
    /* reported below */
  }
  if (!response.ok || data.ok === false) {
    // A lifecycle/configuration conflict invalidates the cached optimistic
    // revision. The UI re-reads before an operator can choose another write.
    if (response.status === 409 && isOperationsConflict(data)) {
      const planID = planIDFromAPIURL(url);
      if (planID !== null) {
        if (onOperationsConflict) onOperationsConflict(planID);
        else revisions.delete(planID);
      }
    }
    const error = new Error(responseMessage(data, `HTTP ${response.status}`)) as Error & { status?: number; payload?: Json };
    Object.assign(error, { status: response.status, payload: data });
    throw error;
  }
  return data;
}
function planOwner(value: Json): Json {
  const owner = value.owner && typeof value.owner === "object" ? value.owner : {};
  const staffID = Number(owner.staff_id);
  if (!Number.isSafeInteger(staffID) || staffID < 1)
    return { owner_userid: "", owner_name: "未配置负责人", owner_state: "unconfigured" };
  const name = String(owner.display_name || "").trim();
  if (owner.profile_read_state === "ready" && owner.name_source === "wecom_profile" && name)
    return { owner_userid: String(staffID), owner_name: name, owner_state: "ready" };
  if (owner.profile_read_state === "unavailable")
    return { owner_userid: String(staffID), owner_name: "负责人目录不可用", owner_state: "directory_unavailable" };
  return { owner_userid: String(staffID), owner_name: "负责人目录未同步", owner_state: "directory_pending" };
}
function boundGroupCount(value: Json): number | null {
  // Servers that predate this list projection remain readable. The caller
  // renders the missing fact as unknown; it must never turn into a false zero
  // or trigger the former per-plan detail and directory waterfall.
  if (!Object.prototype.hasOwnProperty.call(value, "bound_group_count")) return null;
  const count = value.bound_group_count;
  if (typeof count !== "number" || !Number.isSafeInteger(count) || count < 0)
    throw new Error("计划绑定群数数据无效");
  return count;
}
function requiredIdentifier(value: unknown, message: string): number {
  if (typeof value !== "number" && (typeof value !== "string" || !/^[1-9][0-9]*$/.test(value))) throw new Error(message);
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < 1) throw new Error(message);
  return number;
}
function requiredNumericPositiveInteger(value: unknown, message: string): number {
  if (typeof value !== "number") throw new Error(message);
  const number = value;
  if (!Number.isSafeInteger(number) || number < 1) throw new Error(message);
  return number;
}
function requiredNumericNonNegativeInteger(value: unknown, message: string): number {
  if (typeof value !== "number") throw new Error(message);
  const number = value;
  if (!Number.isSafeInteger(number) || number < 0) throw new Error(message);
  return number;
}
function requestedPlanPage(url: URL): { limit: number; offset: number } {
  const hasLimit = url.searchParams.has("limit");
  const hasOffset = url.searchParams.has("offset");
  if (!hasLimit && !hasOffset) return { limit: 50, offset: 0 };
  if (!hasLimit || !hasOffset) throw new Error("计划列表页码请求无效");
  const limit = requiredIdentifier(url.searchParams.get("limit"), "计划列表页码请求无效");
  const offsetValue = url.searchParams.get("offset");
  if (offsetValue === null || !/^(?:0|[1-9][0-9]*)$/.test(offsetValue)) throw new Error("计划列表页码请求无效");
  const offset = Number(offsetValue);
  if (!Number.isSafeInteger(offset)) throw new Error("计划列表页码请求无效");
  if (limit !== 50 || offset > 1000000) throw new Error("计划列表页码请求无效");
  return { limit, offset };
}
function plan(value: Json, publishRevision = true): Json {
  const id = requiredIdentifier(value.plan_id, "计划列表 ID 数据无效");
  const revision = requiredNumericPositiveInteger(value.revision, "计划列表版本数据无效");
  const count = boundGroupCount(value);
  const queueCount = Object.prototype.hasOwnProperty.call(value, "queue_count")
    ? requiredNumericNonNegativeInteger(value.queue_count, "计划通知排队数据无效")
    : 0;
  if (publishRevision) revisions.set(id, revision);
  return {
    id,
    plan_name: value.name,
    plan_code: `v3-${id}`,
    plan_type: value.plan_type || "standard",
    status: value.status === "paused" ? "disabled" : value.status,
    revision,
    ...planOwner(value),
    queue_count: queueCount,
    bound_group_count: count,
    today_estimated_reach: null,
    updated_at: value.updated_at,
  };
}
function node(value: Json): Json {
  const resolvedRecords = contentRecordsFromUnknown(value.content_material_records);
  const records = resolvedRecords.length ? resolvedRecords : materialRecordsFor(value.material_plan);
  return {
    id: Number(value.node_id),
    day_index: Number(value.day_index || 1),
    scheduled_time: value.scheduled_time || "20:00",
    trigger_time_label:
      value.trigger_time_label || value.scheduled_time || "20:00",
    action_title: value.action_title || "",
    text_content: value.message_text || "",
    content_package_json: packageFor(value.material_plan),
    // This is a UI read projection of the exact owner sequence. The eventual
    // node command checks it against content_package_json before mapping it
    // back to material_plan.references; labels never become domain facts.
    content_material_records: records,
    content_material_order_json: records,
    attachments: [],
    sort_order: Number(value.position || 1),
    status: value.status || "active",
  };
}
const materialKinds = ['image', 'miniprogram', 'attachment', 'group_invite'] as const;
type GroupOpsMaterialKind = typeof materialKinds[number];
const materialPackageFields: Record<GroupOpsMaterialKind, string> = {
  image: 'image_library_ids', miniprogram: 'miniprogram_library_ids', attachment: 'attachment_library_ids', group_invite: 'group_invite_library_ids',
};
const materialLabels: Record<GroupOpsMaterialKind, string> = {
  image: '图片', miniprogram: '小程序', attachment: '附件', group_invite: '群邀请',
};
function materialKind(value: unknown): GroupOpsMaterialKind | undefined {
  const candidate = String(value || '').trim();
  return (materialKinds as readonly string[]).includes(candidate) ? candidate as GroupOpsMaterialKind : undefined;
}
function materialID(value: unknown): number | undefined {
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}
function materialRecord(kind: GroupOpsMaterialKind, id: number): ContentMaterialRecord {
  return { source: 'media-library', kind, id, label: `${materialLabels[kind]}素材 #${id}`, disabledReason: '素材状态待目录确认' };
}
function materialRecordsFor(value: Json | undefined): ContentMaterialRecord[] {
  const output: ContentMaterialRecord[] = [];
  const seen = new Set<string>();
  for (const reference of Array.isArray(value?.references) ? value!.references : []) {
    const kind = materialKind(reference?.kind);
    const id = materialID(reference?.id);
    if (!kind || id === undefined) continue;
    const key = `${kind}:${id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    output.push(materialRecord(kind, id));
  }
  return output;
}
function packageFor(value: Json | undefined): Json {
  const result: Json = {
    content_text: "",
    image_library_ids: [],
    miniprogram_library_ids: [],
    attachment_library_ids: [],
    group_invite_library_ids: [],
  };
  for (const ref of value?.references || []) {
    const target = (
      {
        image: "image_library_ids",
        miniprogram: "miniprogram_library_ids",
        attachment: "attachment_library_ids",
        group_invite: "group_invite_library_ids",
      } as Json
    )[ref.kind];
    if (target) result[target].push(Number(ref.id));
  }
  return result;
}
function packageReferences(value: unknown): Array<{ kind: GroupOpsMaterialKind; id: number }> {
  const pkg = value && typeof value === 'object' ? value as Json : {};
  const output: Array<{ kind: GroupOpsMaterialKind; id: number }> = [];
  const seen = new Set<string>();
  for (const kind of materialKinds) {
    const raw = (pkg[materialPackageFields[kind]]);
    const items = Array.isArray(raw) ? raw : [];
    for (const candidate of items) {
      const id = materialID(candidate);
      if (id === undefined) throw new Error('素材标识无效，请重新打开内容编辑器。');
      const key = `${kind}:${id}`;
      if (seen.has(key)) throw new Error('同一类型的素材不能重复，请重新确认内容。');
      seen.add(key);
      output.push({ kind, id });
    }
  }
  return output;
}
function persistedMaterialOrder(value: unknown, expected: readonly { kind: GroupOpsMaterialKind; id: number }[]): Array<{ kind: GroupOpsMaterialKind; id: number }> | undefined {
  if (value === undefined) return undefined; // Existing form: stable historic per-kind order.
  if (!Array.isArray(value)) throw new Error('素材顺序信息无效，请重新打开内容编辑器。');
  const expectedKeys = new Set(expected.map((reference) => `${reference.kind}:${reference.id}`));
  const output: Array<{ kind: GroupOpsMaterialKind; id: number }> = [];
  const seen = new Set<string>();
  for (const record of value) {
    if (!record || typeof record !== 'object' || (record as Json).source !== 'media-library') throw new Error('素材来源无效，请重新打开内容编辑器。');
    const kind = materialKind((record as Json).kind);
    const id = materialID((record as Json).id);
    if (!kind || id === undefined) throw new Error('素材顺序信息无效，请重新打开内容编辑器。');
    const key = `${kind}:${id}`;
    if (!expectedKeys.has(key) || seen.has(key)) throw new Error('素材顺序与当前内容不一致，请重新确认内容。');
    seen.add(key);
    output.push({ kind, id });
  }
  if (output.length !== expected.length || seen.size !== expectedKeys.size) throw new Error('素材顺序与当前内容不一致，请重新确认内容。');
  return output;
}
function materialPlan(input: Json): Json {
  const references = packageReferences(input.content_package_json);
  const persisted = persistedMaterialOrder(input.content_material_order_json, references);
  return { references: (persisted || references).map((reference) => ({ kind: reference.kind, id: reference.id })) };
}
async function detail(id: number, onOperationsConflict?: (planID: number) => void): Promise<Json> {
  // This path feeds the donor's full plan projection and also supplies
  // revision checks before a node command. It never starts a Media lookup.
  return nativeRequest(`${base}/plans/${id}`, {}, onOperationsConflict);
}
async function revision(id: number): Promise<number> {
  if (!revisions.has(id)) plan(await detail(id).then((v) => v.plan || v));
  return revisions.get(id) || 0;
}
function groupView(asset: Json, directoryItem?: GroupPickerRecord): Json {
  const external = directoryItem?.external_member_count;
  const total = directoryItem?.member_count;
  const knownExternal = external !== null && external !== undefined && Number.isFinite(Number(external));
  const knownTotal = total !== null && total !== undefined && Number.isFinite(Number(total));
  return {
    chat_id: String(asset.asset_reference || asset.chat_reference || ''),
    group_name: String(directoryItem?.display_name || asset.display_name || '群名称待同步'),
    owner_userid: directoryItem?.owner_staff_id ? String(directoryItem.owner_staff_id) : '',
    internal_member_count_snapshot: knownTotal && knownExternal ? Number(total) - Number(external) : null,
    external_member_count_snapshot: knownExternal ? Number(external) : null,
  };
}
async function groupsForPlan(id: number, source?: Pick<InitialDetailReadEpoch, 'detail'>): Promise<Json[]> {
  const value = await (source?.detail || detail(id));
  const known = planGroupDirectoryViews.get(id) || new Map<string, GroupPickerRecord>();
  // Group bindings are Owner facts. This read only decorates them with a
  // scoped picker page already authorised for this plan; it never starts a
  // whole-directory crawl while the detail or node form is loading.
  return (value.group_assets || []).map((asset: Json) => groupView(asset, known.get(String(asset.asset_reference || ''))));
}
async function expectedRevision(body: Json, id: number): Promise<number> {
  if (Object.prototype.hasOwnProperty.call(body, "expected_revision"))
    return requiredNumericPositiveInteger(body.expected_revision, "计划版本数据无效");
  return revision(id);
}
function createPlanResult(
  value: Json,
  message: string,
): { raw: Json; id: number; revision: number } {
  const raw =
    value?.plan && typeof value.plan === "object" ? value.plan : value;
  if (!raw || typeof raw !== "object") throw new Error(message);
  return {
    raw,
    id: requiredIdentifier(raw.plan_id, message),
    revision: requiredNumericPositiveInteger(raw.revision, message),
  };
}
function confirmedCreatedPlan(value: Json, recovery: Json): { raw: Json; id: number; revision: number } {
  const created = createPlanResult(value, "创建结果未确认，请重新确认创建");
  if (created.revision !== 1 || created.raw.status !== "draft" || created.raw.name !== recovery.planName)
    throw new Error("创建结果未确认，请重新确认创建");
  return created;
}
function createConfigurationPayload(_body: Json, recovery: Json): Json {
  return {
    expected_revision: recovery.expectedRevision,
    name: recovery.planName,
    plan_type: recovery.planType,
    owner_staff_id: recovery.ownerID,
  };
}
function confirmedCreateConfiguration(
  value: Json,
  recovery: Json,
  payload: Json,
): Json {
  const confirmed = createPlanResult(
    value,
    "基础配置结果未确认，请重新确认配置",
  );
  if (
    confirmed.id !== recovery.planID ||
    confirmed.revision !== recovery.expectedRevision + 1 ||
    confirmed.raw.name !== payload.name ||
    confirmed.raw.status !== "draft" ||
    confirmed.raw.plan_type !== payload.plan_type
  )
    throw new Error("基础配置结果未确认，请重新确认配置");
  if (
    !Array.isArray(value.members) ||
    value.members.length !== 1 ||
    requiredIdentifier(
      value.members[0]?.staff_id,
      "基础配置结果未确认，请重新确认配置",
    ) !== payload.owner_staff_id
  )
    throw new Error("基础配置结果未确认，请重新确认配置");
  return plan(confirmed.raw);
}
async function completeCreateConfiguration(
  body: Json,
  recovery: Json,
): Promise<Json> {
  const payload = createConfigurationPayload(body, recovery);
  let value: Json;
  try {
    assertCreateRecoverySession(recovery);
    value = await nativeRequest(`${base}/plans/${recovery.planID}`, {
      method: "PUT",
      body: payload,
      privateCreateRecoveryKey: recovery.configurationKey,
    });
  } catch (error) {
    throw createRecoveryError(error, recovery);
  }
  try {
    return confirmedCreateConfiguration(value, recovery, payload);
  } catch (error) {
    throw createRecoveryError(error, recovery);
  }
}
function newInitialDetailReadEpoch(id: number): InitialDetailReadEpoch {
  const generation = (detailReadGenerations.get(id) || 0) + 1;
  detailReadGenerations.set(id, generation);
  let epoch: InitialDetailReadEpoch;
  const clearCurrentRevisionOnConflict = () => {
    if (initialDetailReadEpochs.get(id) === epoch && detailReadGenerations.get(id) === generation)
      revisions.delete(id);
  };
  epoch = {
    id,
    generation,
    planClaimed: false,
    groupsClaimed: false,
    pairable: true,
    claims: 0,
    detail: detail(id, clearCurrentRevisionOnConflict),
    groups: null,
  };
  // The donor starts its plan/groups pair synchronously. A later standalone
  // read gets a fresh epoch even when this first request is still pending.
  void epoch.detail.catch(() => undefined);
  queueMicrotask(() => { epoch.pairable = false; });
  return epoch;
}
function claimInitialDetailRead(id: number, kind: InitialDetailReadKind): { epoch: InitialDetailReadEpoch; release: () => void } {
  let epoch = initialDetailReadEpochs.get(id);
  const alreadyClaimed = epoch && (kind === 'plan' ? epoch.planClaimed : epoch.groupsClaimed);
  if (!epoch || !epoch.pairable || alreadyClaimed) {
    epoch = newInitialDetailReadEpoch(id);
    initialDetailReadEpochs.set(id, epoch);
  }
  if (kind === 'plan') epoch.planClaimed = true;
  else epoch.groupsClaimed = true;
  epoch.claims += 1;
  let released = false;
  return {
    epoch,
    release: () => {
      if (released) return;
      released = true;
      epoch.claims -= 1;
      if (epoch.claims === 0 && initialDetailReadEpochs.get(id) === epoch)
        initialDetailReadEpochs.delete(id);
    },
  };
}
function currentInitialDetailRead(epoch: InitialDetailReadEpoch): boolean {
  return initialDetailReadEpochs.get(epoch.id) === epoch && detailReadGenerations.get(epoch.id) === epoch.generation;
}
function invalidateInitialDetailRead(id: number): void {
  detailReadGenerations.set(id, (detailReadGenerations.get(id) || 0) + 1);
  initialDetailReadEpochs.delete(id);
}
function groupsForInitialDetailRead(epoch: InitialDetailReadEpoch): Promise<Json[]> {
  if (!epoch.groups) epoch.groups = groupsForPlan(epoch.id, epoch);
  return epoch.groups;
}
async function summary(id: number): Promise<Json> {
  return summarizeGroups(await groupsForPlan(id));
}
function publishGroupViews(id: number, items: Json[], publish: boolean): Json {
  const values = summarizeGroups(items);
  if (!publish) return values;
  planGroupViews.set(id, items);
  const view = planSummaryViews.get(id) || {};
  Object.assign(view, values);
  planSummaryViews.set(id, view);
  return view;
}
function summarizeGroups(rows: Json[]): Json {
  const known =
    rows.length > 0 &&
    rows.every((item) =>
      item.external_member_count_snapshot !== null &&
      item.external_member_count_snapshot !== undefined &&
      Number.isFinite(Number(item.external_member_count_snapshot)),
    );
  return {
    bound_group_count: rows.length,
    internal_member_count: known
      ? rows.reduce((sum, item) => sum + Number(item.internal_member_count_snapshot), 0)
      : null,
    external_member_count: known
      ? rows.reduce((sum, item) => sum + Number(item.external_member_count_snapshot), 0)
      : null,
    estimated_reach: null,
  };
}
async function requestJson(url: string, options: Json = {}): Promise<Json> {
  const method = String(options.method || "GET").toUpperCase();
  const body = options.body || {};
  const parsedURL = new URL(url, window.location.origin);
  const match = parsedURL.pathname.match(/\/plans\/(\d+)/);
  const id = match ? Number(match[1]) : 0;
  const readOnlyContentPreview = id > 0 && method === "POST" && parsedURL.pathname === `${base}/plans/${id}/content/preview`;
  if (id && method !== "GET" && !readOnlyContentPreview) invalidateInitialDetailRead(id);
  if (parsedURL.pathname === `${base}/plans` && method === "GET") {
    const requested = requestedPlanPage(parsedURL);
    const data = await nativeRequest(url, { signal: options.signal && typeof options.signal === "object" ? options.signal as AbortSignal : undefined });
    if (!Array.isArray(data.items)) throw new Error("计划列表数据无效");
    const total = requiredNumericNonNegativeInteger(data.total, "计划列表总数数据无效");
    const limit = requiredNumericPositiveInteger(data.limit, "计划列表页码数据无效");
    const offset = requiredNumericNonNegativeInteger(data.offset, "计划列表页码数据无效");
    if (limit !== requested.limit || offset !== requested.offset || typeof data.has_more !== "boolean" || data.items.length > limit)
      throw new Error("计划列表页码数据无效");
    // Parse the complete page before publishing any row revision. A malformed
    // later row must not advance CAS for an earlier row that remains visible
    // after the list read fails.
    data.items.forEach((item: Json) => {
      boundGroupCount(item);
      requiredIdentifier(item.plan_id, "计划列表 ID 数据无效");
      requiredNumericPositiveInteger(item.revision, "计划列表版本数据无效");
      requiredNumericNonNegativeInteger(item.queue_count, "计划通知排队数据无效");
    });
    // A page only publishes into the Standard controller's local snapshot.
    // Detail reads retain the existing revision cache; a list response must
    // not advance CAS for a row whose page was never rendered.
    const items = data.items.map((item: Json) => plan(item, false));
    return {
      ...data,
      items,
      total,
      limit,
      offset,
      has_more: data.has_more,
      queue_count: items.reduce(
        (sum, item) => sum + Number(item.queue_count || 0),
        0,
      ),
    };
  }
  if (url === `${base}/plans` && method === "POST") {
    const recovery = createRecoveryOptions(options.createRecovery);
    if (recovery?.stage === "configuration")
      return { item: await completeCreateConfiguration(body, recovery) };
    if (recovery?.stage === "post") {
      let created: Json;
      try {
        assertCreateRecoverySession(recovery);
        created = await nativeRequest(url, {
          method,
          body: { name: recovery.planName },
          privateCreateRecoveryKey: recovery.createKey,
        });
      } catch (error) {
        throw createRecoveryError(error, recovery);
      }
      let current: { raw: Json; id: number; revision: number };
      try {
        current = confirmedCreatedPlan(created, recovery);
      } catch (error) {
        throw createRecoveryError(error, recovery);
      }
      const configuration = Object.freeze({
        ...recovery,
        stage: "configuration",
        planID: current.id,
        expectedRevision: current.revision,
      });
      return { item: await completeCreateConfiguration(body, configuration) };
    }
    const created = await nativeRequest(url, {
      method,
      body: { name: String(body.plan_name || "").trim() || "新建群运营计划" },
    });
    let value = created.plan || created;
    const current = plan(value);
    const owner = Number(body.owner_userid);
    // Creation has no member yet. The optional owner command below is one
    // atomic plan mutation, rather than a separate member add operation.
    if (body.plan_type || owner > 0) {
      const payload: Json = {
        expected_revision: await revision(current.id),
        name: current.plan_name,
        plan_type: body.plan_type,
      };
      if (owner > 0) payload.owner_staff_id = owner;
      value = await nativeRequest(`${base}/plans/${current.id}`, {
        method: "PUT",
        body: payload,
      });
    }
    return { item: plan((value.plan || value).plan || value.plan || value) };
  }
  if (id && /\/enable$/.test(url))
    return nativeRequest(`${base}/plans/${id}/enable`, {
      method: "POST",
      body: { expected_revision: await expectedRevision(body, id) },
    });
  if (id && /\/disable$/.test(url))
    return nativeRequest(`${base}/plans/${id}/disable`, {
      method: "POST",
      body: { expected_revision: await expectedRevision(body, id) },
    });
  if (id && /\/groups$/.test(url) && method === 'GET') {
    const claimed = claimInitialDetailRead(id, 'groups');
    try {
      const items = await groupsForInitialDetailRead(claimed.epoch);
      return { items, summary: publishGroupViews(id, items, currentInitialDetailRead(claimed.epoch)) };
    } finally {
      claimed.release();
    }
  }
  if (id && /\/groups$/.test(url) && method === "POST")
    return nativeRequest(`${base}/plans/${id}/groups`, {
      method,
      body: {
        expected_revision: await revision(id),
        asset_reference: body.chat_id,
      },
    });
  if (id && /\/groups\//.test(url) && method === "DELETE")
    return nativeRequest(
      `${base}/plans/${id}/groups/${encodeURIComponent(url.split("/").pop() || "")}`,
      { method, body: { expected_revision: await revision(id) } },
    );
  if (id && /\/nodes$/.test(url) && method === "GET") {
    const value = await detail(id);
    return { items: (value.nodes || []).map(node) };
  }
  if (id && /\/nodes\/\d+$/.test(url) && method === "DELETE")
    return nativeRequest(`${base}/plans/${id}/nodes/${encodeURIComponent(url.split("/").pop() || "")}`, { method, body: { expected_revision: await revision(id) } });
  if (id && /\/nodes(?:\/\d+)?$/.test(url) && method !== "GET") {
    const nodeID = Number(url.split("/").pop());
    const currentDetail = await detail(id);
    const nodes = currentDetail.nodes || [];
    const current = nodeID
      ? nodes.find((item: Json) => Number(item.node_id) === nodeID) || {}
      : {};
    // The frozen form keeps its historic default sort value (10) for a new
    // action. V3 stores contiguous positions and rejects a position after the
    // current tail, so interpret an out-of-range legacy sort value as append.
    // Existing actions retain their real position unless the submitted value
    // identifies a valid insertion point in this current plan.
    const requestedPosition = Number(body.sort_order);
    const maximumPosition = nodes.length + (nodeID ? 0 : 1);
    const position =
      Number.isInteger(requestedPosition) &&
      requestedPosition >= 1 &&
      requestedPosition <= maximumPosition
        ? requestedPosition
        : Number(current.position || nodes.length + 1);
    const payload = {
      expected_revision: await revision(id),
      position,
      kind: "message",
      day_index: Number(body.day_index || 1),
      scheduled_time: body.scheduled_time || "20:00",
      trigger_time_label: body.scheduled_time || "20:00",
      action_title: String(body.action_title || "").trim(),
      status: body.status || "active",
      message_text: body.text_content || "",
      delay_minutes: 0,
      material_plan: materialPlan(body),
    };
    if (!payload.action_title) throw new Error("动作标题不能为空");
    return nativeRequest(
      `${base}/plans/${id}/nodes${nodeID ? `/${nodeID}` : ""}`,
      { method: nodeID ? "PUT" : method, body: payload },
    );
  }
  if (id && /\/webhook$/.test(url) && method === "GET") {
    const descriptor = await nativeRequest(`${base}/plans/${id}/webhook-descriptor`);
    const path = String(descriptor.path || "").trim();
    const configured = Boolean(descriptor.configured && descriptor.reference && path);
    return {
      configured,
      reference: String(descriptor.reference || ""),
      webhook_url: configured ? new URL(path, window.location.origin).href : "",
      signature_algorithm: descriptor.signature_algorithm || "",
      signature_header: descriptor.signature_header || "",
      timestamp_header: descriptor.timestamp_header || "",
      nonce_header: descriptor.nonce_header || "",
      client_id_header: descriptor.client_id_header || "",
    };
  }
  if (id && /\/webhook-descriptor$/.test(url) && method === "PUT") {
    const value = await nativeRequest(`${base}/plans/${id}/webhook-descriptor`, {
      method,
      body: { expected_revision: await revision(id), reference: String(body.reference || "").trim() },
    });
    const updated = value.plan || value;
    if (Number.isSafeInteger(Number(updated.revision))) revisions.set(id, Number(updated.revision));
    return value;
  }
  if (id && url === `${base}/plans/${id}` && method === 'GET') {
    const claimed = claimInitialDetailRead(id, 'plan');
    try {
      const [value, items] = await Promise.all([claimed.epoch.detail, groupsForInitialDetailRead(claimed.epoch)]);
      const rawPlan = value.plan || {};
      // A short-lived compatibility fallback preserves the local staff key when
      // a browser reads a server that predates the owner projection. It never
      // makes a second directory request or invents a profile name.
      const legacyOwner = (value.members || [])[0];
      const publish = currentInitialDetailRead(claimed.epoch);
      const projected = plan(rawPlan.owner || !legacyOwner?.staff_id
        ? rawPlan
        : { ...rawPlan, owner: { staff_id: legacyOwner.staff_id } }, publish);
      return { ...projected, groups_summary: publishGroupViews(id, items, publish) };
    } finally {
      claimed.release();
    }
  }
  if (id && url === `${base}/plans/${id}` && method === "DELETE")
    return nativeRequest(url, {
      method,
      body: { expected_revision: await expectedRevision(body, id) },
    });
  if (
    id &&
    url === `${base}/plans/${id}` &&
    (method === "PUT" || method === "PATCH")
  ) {
    const current = await detail(id);
    const wantedOwner = Number(body.owner_userid);
    const currentOwner = Number((current.members || [])[0]?.staff_id || 0);
    const payload: Json = {
      // The Standard controller captures the revision that was actually
      // rendered with this form. Do not pre-read a newer value and silently
      // turn a user's stale save into a write against an unseen definition.
      expected_revision: await expectedRevision(body, id),
      name: body.plan_name,
      plan_type: body.plan_type,
    };
    // The donor submits its visible value on every save. Preserve legacy
    // multi-member plans unless the responsible employee actually changed.
    if (wantedOwner > 0 && wantedOwner !== currentOwner)
      payload.owner_staff_id = wantedOwner;
    let value = await nativeRequest(url, { method: "PUT", body: payload });
    const wanted = body.status;
    const mapped = (value.plan || value).status;
    if (wanted === "active" && mapped !== "active") {
      const persisted = value.plan || value;
      try {
        value = await nativeRequest(`${base}/plans/${id}/enable`, {
          method: "POST",
          body: { expected_revision: persisted.revision },
        });
      } catch (error) {
        // A save followed by an activation is two existing owner commands.
        // Distinguish "PUT accepted, enable rejected" from a stale PUT so the
        // Standard controller never infers persistence from a later revision.
        (error as Error & { groupopsSavedBeforeLifecycle?: Json }).groupopsSavedBeforeLifecycle = {
          plan_id: id,
          revision: persisted.revision,
        };
        throw error;
      }
    }
    if (wanted === "disabled" && mapped === "active")
      value = await nativeRequest(`${base}/plans/${id}/disable`, {
        method: "POST",
        body: { expected_revision: (value.plan || value).revision },
      });
    return value;
  }
  if (url.startsWith(`${base}/groups`) && method === "GET") {
    let data: Json;
    try {
      data = await nativeRequest(url);
    } catch (error) {
      refreshedGroupTotal = null;
      announceDirectoryReadFailure();
      // The groups screen distinguishes a directory outage from a confirmed
      // empty page and retains any rows it already rendered. Do not translate
      // a failed read into an empty success payload.
      throw error;
    }
    if (!Array.isArray(data.items)) {
      refreshedGroupTotal = null;
      throw new Error("群聊列表暂不可读取");
    }
    return {
      ...data,
      items: (data.items || []).map((item: Json) => {
        const total = item.member_count;
        const external = item.external_member_count;
        const knownTotal = total !== null && total !== undefined && Number.isFinite(Number(total));
        const knownExternal = external !== null && external !== undefined && Number.isFinite(Number(external));
        return {
          chat_id: item.chat_reference,
          group_name: item.display_name || item.chat_reference,
          owner_userid: String(item.owner_staff_id || ""),
          internal_member_count_snapshot: knownTotal && knownExternal ? Number(total) - Number(external) : null,
          external_member_count_snapshot: knownExternal ? Number(external) : null,
        };
      }),
    };
  }
  if (url === `${base}/groups/sync`) {
    refreshedGroupTotal = null;
    const result = await nativeRequest(url, {
      method,
      body: {
        owner_staff_id: Number(body.owner_userid),
        limit: Number(body.limit || 100),
      },
    });
    const planID = Number(document.getElementById("group-ops-app")?.dataset.planId);
    if (planID > 0) {
      try {
        // A scoped-directory refresh changes the projection a current detail
        // load would decorate. Its authoritative plan read must therefore
        // publish from a new generation, never an earlier hydration epoch.
        invalidateInitialDetailRead(planID);
        // Refresh returns the current owner-scoped directory page. Merge only
        // matching decoration into the Host cache; plan bindings remain Owner
        // facts and an unsaved form still does not trigger a plan write.
        const refreshed = new Map<string, GroupPickerRecord>((result.items || []).flatMap((item: Json): [string, GroupPickerRecord][] => {
          const record = groupRecord({ asset_reference: item.chat_reference }, item);
          return record.chat_reference ? [[record.chat_reference, record]] : [];
        }));
        const directoryViews = planGroupDirectoryViews.get(planID) || new Map<string, GroupPickerRecord>();
        for (const [reference, directoryRecord] of refreshed) directoryViews.set(reference, directoryRecord);
        planGroupDirectoryViews.set(planID, directoryViews);
        // Read the Owner binding again and decorate only references returned
        // by this scoped refresh. Do not reload the donor form or create a
        // broader directory read, so an unsaved owner/name draft remains local.
        const persisted = await detail(planID);
        const rows = (persisted.group_assets || []).map((asset: Json) => {
          const reference = String(asset.asset_reference || "");
          return groupView(asset, refreshed.get(reference) || directoryViews.get(reference));
        });
        const view = planGroupViews.get(planID);
        if (view) view.splice(0, view.length, ...rows);
        const counts = planSummaryViews.get(planID);
        const summaryValue = summarizeGroups(rows);
        if (counts) Object.assign(counts, summaryValue);
        window.dispatchEvent(new CustomEvent("aicrm:groupops-directory-decoration", { detail: { planId: planID, rows, summary: summaryValue } }));
      } catch {
        throw new Error("群聊已刷新，但页面读回失败，请重新打开页面查看");
      }
    }
    if (Number.isSafeInteger(result.total) && result.total >= 0) refreshedGroupTotal = result.total;
    return result;
  }
  if (url.startsWith("/api/admin/common/operation-members")) {
    const data = await nativeRequest(url);
    return {
      ...data,
      items: (data.items || []).flatMap((item: Json) => {
        const staffID = String(item.staff_id || "").trim();
        const userID = String(item.sender_userid || item.user_id || staffID).trim();
        if (!userID || !staffID) return [];
        return [{
          user_id: userID,
          staff_id: staffID,
          display_name: String(item.display_name || item.name || `员工 #${userID}`),
        }];
      }),
    };
  }
  return nativeRequest(url, options);
}


type GroupOpsContentBridgeOptions = {
  title?: unknown;
  value?: unknown;
  selectedRecords?: unknown;
  onConfirm?(result: ContentComposerResult): void | Promise<void>;
  onCancel?(): void;
  /** The frozen node form owns whether the opening request still belongs to it. */
  isCurrent?(): boolean;
  /** A native action remains visible while one node's authorised detail reads settle. */
  loadingTarget?: unknown;
};
type GroupOpsReadonlyContentOptions = Omit<GroupOpsContentBridgeOptions, 'onConfirm' | 'onCancel'>;
type MaterialPickerBridge = {
  open(options: {
    type: MaterialType;
    title?: string;
    selectedIds?: number[];
    selectedRecords?: MaterialPickerRecord[];
    limit?: number;
    onCommit?(result: { selected: MaterialPickerRecord[]; added: MaterialPickerRecord[]; removed: MaterialPickerRecord[] }): void | Promise<void>;
    onCancel?(): void;
  }): unknown;
};
const groupOpsMaterialLimits: Record<GroupOpsMaterialKind, number> = { image: 3, miniprogram: 1, attachment: 9, group_invite: 1 };
const groupOpsMaterialEndpoints: Record<GroupOpsMaterialKind, string> = {
  image: '/api/admin/image-library', miniprogram: '/api/admin/miniprogram-library', attachment: '/api/admin/attachment-library', group_invite: '/api/admin/group-invite-library',
};
function wellFormedUnicode(value: string): boolean {
  const nativeCheck = (String.prototype as unknown as { isWellFormed?: (this: string) => boolean }).isWellFormed;
  if (typeof nativeCheck === 'function') return nativeCheck.call(value);
  for (let index = 0; index < value.length; index += 1) {
    const unit = value.charCodeAt(index);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const following = value.charCodeAt(index + 1);
      if (following < 0xdc00 || following > 0xdfff) return false;
      index += 1;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) return false;
  }
  return true;
}
function groupOpsMessageIssue(value: string): string | undefined {
  if (!wellFormedUnicode(value)) return '话术包含无效字符，请重新输入。';
  if (value.trim() !== value) return '话术首尾不能包含空白字符，请调整后确认。';
  return undefined;
}
function contentRecordFromUnknown(value: unknown): ContentMaterialRecord | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const input = value as Json;
  const kind = materialKind(input.kind);
  const id = materialID(input.id);
  if (input.source !== 'media-library' || !kind || id === undefined) return undefined;
  return {
    source: 'media-library', kind, id,
    label: String(input.label || `${materialLabels[kind]}素材 #${id}`),
    subtitle: input.subtitle ? String(input.subtitle) : undefined,
    thumbnailURL: input.thumbnailURL ? String(input.thumbnailURL) : undefined,
    disabledReason: input.disabledReason ? String(input.disabledReason) : undefined,
  };
}
function contentRecordsFromUnknown(value: unknown): ContentMaterialRecord[] {
  if (!Array.isArray(value)) return [];
  const output: ContentMaterialRecord[] = [];
  const seen = new Set<string>();
  for (const candidate of value) {
    const record = contentRecordFromUnknown(candidate);
    if (!record) continue;
    const key = `${record.kind}:${record.id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    output.push(record);
  }
  return output;
}
function pickerRecordFromContent(record: ContentMaterialRecord): MaterialPickerRecord {
  return {
    type: record.kind as MaterialType,
    library_id: record.id,
    title: record.label,
    subtitle: record.subtitle,
    thumbnail_url: record.thumbnailURL,
    selectable: !record.disabledReason,
    enabled: !record.disabledReason,
    unavailable_reason: record.disabledReason,
  };
}
function contentRecordFromPicker(value: MaterialPickerRecord, expectedKind: ContentMaterialKind): ContentMaterialRecord | undefined {
  const kind = materialKind(value.type);
  const id = materialID(value.library_id);
  if (!kind || kind !== expectedKind || id === undefined) return undefined;
  return {
    source: 'media-library', kind, id,
    label: String(value.title || `${materialLabels[kind]}素材 #${id}`),
    subtitle: value.subtitle ? String(value.subtitle) : undefined,
    thumbnailURL: value.thumbnail_url ? String(value.thumbnail_url) : undefined,
    disabledReason: value.unavailable_reason ? String(value.unavailable_reason) : undefined,
  };
}
function mediaTitle(kind: GroupOpsMaterialKind, value: Json, id: number): string {
  return String(value.title || value.name || value.file_name || value.description || `${materialLabels[kind]}素材 #${id}`).trim() || `${materialLabels[kind]}素材 #${id}`;
}
function mediaSubtitle(kind: GroupOpsMaterialKind, value: Json): string {
  return String(value.subtitle || value.description || value.appid || value.app_id || value.mime_type || value.file_name || (kind === 'group_invite' ? value.join_url : '') || '').trim();
}
function mediaThumbnail(value: Json): string | undefined {
  const candidate = value.thumb_320_url || value.thumb_160_url || value.thumb_image_url || value.thumbnail_url || value.variant_url;
  return typeof candidate === 'string' && candidate.trim() ? candidate.trim() : undefined;
}
function mediaPickerRecord(kind: GroupOpsMaterialKind, value: Json): MaterialPickerRecord | undefined {
  const id = materialID(value.id ?? value.library_id ?? value.resource_id);
  if (id === undefined) return undefined;
  const enabled = value.enabled !== false;
  return {
    type: kind, library_id: id, title: mediaTitle(kind, value, id), subtitle: mediaSubtitle(kind, value), thumbnail_url: mediaThumbnail(value),
    enabled, selectable: enabled, metadata: value, unavailable_reason: enabled ? undefined : '素材已停用',
  };
}
function detailPayload(kind: GroupOpsMaterialKind, value: Json): Json {
  const named = kind === 'image' ? value.image : kind === 'miniprogram' ? (value.miniprogram || value.mini_program) : kind === 'attachment' ? value.attachment : value.group_invite;
  const item = value.item || named || value;
  return item && typeof item === 'object' ? item as Json : {};
}
function unresolvedMaterialRecord(kind: GroupOpsMaterialKind, id: number, reason: string): ContentMaterialRecord {
  return { source: 'media-library', kind, id, label: `${materialLabels[kind]}素材 #${id}`, disabledReason: reason };
}
async function readGroupOpsMaterialRecord(kind: GroupOpsMaterialKind, id: number, signal?: AbortSignal): Promise<ContentMaterialRecord> {
  try {
    const payload = await nativeRequest(`${groupOpsMaterialEndpoints[kind]}/${id}`, { signal });
    const normalized = mediaPickerRecord(kind, detailPayload(kind, payload));
    if (!normalized || normalized.library_id !== id) return unresolvedMaterialRecord(kind, id, '素材详情返回无效，保留当前引用；可明确移除。');
    return {
      source: 'media-library', kind, id,
      label: String(normalized.title || `${materialLabels[kind]}素材 #${id}`), subtitle: normalized.subtitle || undefined, thumbnailURL: normalized.thumbnail_url || undefined,
      disabledReason: normalized.unavailable_reason || undefined,
    };
  } catch (error) {
    const status = (error as { status?: unknown })?.status;
    const reason = status === 404 ? '素材已删除，保留当前引用；可明确移除。'
      : status === 401 || status === 403 ? '当前无权确认素材状态，保留当前引用。'
        : '素材详情暂时无法读取，保留当前引用。';
    return unresolvedMaterialRecord(kind, id, reason);
  }
}
async function resolveGroupOpsContentRecords(records: readonly ContentMaterialRecord[], controller = new AbortController()): Promise<ContentMaterialRecord[]> {
  // The page only needs labels for the one node the operator asked to view or
  // edit. Keep that bounded: a stale Media read becomes an explicit retained
  // state rather than delaying plan rendering or a node save indefinitely.
  const timeout = window.setTimeout(() => controller.abort(), 2500);
  const resolved = new Array<ContentMaterialRecord>(records.length);
  let next = 0;
  const worker = async () => {
    for (;;) {
      const index = next++;
      if (index >= records.length) return;
      const record = records[index];
      resolved[index] = await readGroupOpsMaterialRecord(record.kind as GroupOpsMaterialKind, record.id, controller.signal);
    }
  };
  try {
    await Promise.all(Array.from({ length: Math.min(4, records.length) }, worker));
    return resolved;
  } finally {
    window.clearTimeout(timeout);
  }
}

async function loadGroupOpsMaterialPage(request: MaterialPickerLoadRequest): Promise<{ items: MaterialPickerRecord[]; nextCursor?: string }> {
  const kind = materialKind(request.type);
  if (!kind) throw new Error('素材类型无效，请重新打开内容编辑器。');
  const offset = Number(request.cursor || '0');
  if (!Number.isSafeInteger(offset) || offset < 0) throw new Error('素材目录分页标记无效，请重新打开内容编辑器。');
  const query = new URLSearchParams({ limit: '50', offset: String(offset), enabled_only: 'false' });
  if (request.query.trim()) query.set('q', request.query.trim());
  const page = await nativeRequest(`${groupOpsMaterialEndpoints[kind]}?${query.toString()}`, { signal: request.signal });
  if (request.signal.aborted) throw new DOMException('素材目录读取已替换', 'AbortError');
  const entries = Array.isArray(page.items) ? page.items : [];
  const items = entries.flatMap((entry: Json) => {
    const record = mediaPickerRecord(kind, entry);
    return record ? [record] : [];
  });
  return { items, nextCursor: page.has_more === true && entries.length ? String(offset + entries.length) : undefined };
}
function ensureGroupOpsMaterialPicker(): MaterialPickerBridge {
  installMaterialPickerAdapter({
    source: 'groupops-node-content', scope: 'group_ops.node_material_plan', loadPage: loadGroupOpsMaterialPage,
    accessLossMessage: (error) => {
      const status = (error as { status?: unknown })?.status;
      return status === 401 || status === 403 ? '素材目录权限已失效；已选素材仍可查看，请取消后重新登录。' : undefined;
    },
  });
  const picker = (window as unknown as { AICRMMaterialPicker?: MaterialPickerBridge }).AICRMMaterialPicker;
  if (!picker || typeof picker.open !== 'function') throw new Error('素材选择器尚未加载完成，请刷新页面后重试。');
  return picker;
}
function chooseGroupOpsMaterials(request: { kind: ContentMaterialKind; selectedRecords: readonly ContentMaterialRecord[]; limit: number }): Promise<readonly ContentMaterialRecord[] | undefined> {
  return new Promise((resolve, reject) => {
    try {
      const picker = ensureGroupOpsMaterialPicker();
      picker.open({
        type: request.kind as MaterialType,
        title: `选择${materialLabels[request.kind as GroupOpsMaterialKind]}`,
        selectedIds: request.selectedRecords.map((record) => record.id),
        selectedRecords: request.selectedRecords.map(pickerRecordFromContent),
        limit: request.limit,
        onCommit: async ({ selected }) => {
          const records = selected.map((record) => contentRecordFromPicker(record, request.kind)).filter((record): record is ContentMaterialRecord => Boolean(record));
          if (records.length !== selected.length) throw new Error('素材选择返回了不匹配的类型，未更新当前草稿。');
          resolve(records);
        },
        onCancel: () => resolve(undefined),
      });
    } catch (error) {
      reject(error);
    }
  });
}
type GroupOpsContentSession = {
  token: number;
  controller: AbortController;
  resolving: boolean;
  loadingTarget?: HTMLButtonElement;
  loadingLabel?: string;
};
let groupOpsContentSequence = 0;
let activeGroupOpsContentSession: GroupOpsContentSession | undefined;

function contentBridgeCurrent(options: GroupOpsContentBridgeOptions): boolean {
  try { return options.isCurrent ? options.isCurrent() : true; } catch { return false; }
}
function setContentLoading(session: GroupOpsContentSession, active: boolean): void {
  const target = session.loadingTarget;
  if (!target || !target.isConnected) return;
  target.disabled = active;
  target.toggleAttribute('aria-busy', active);
  if (active) target.textContent = '正在读取素材详情…';
  else if (session.loadingLabel) target.textContent = session.loadingLabel;
}
function releaseGroupOpsContentSession(session: GroupOpsContentSession): void {
  if (activeGroupOpsContentSession !== session) return;
  setContentLoading(session, false);
  activeGroupOpsContentSession = undefined;
}
function cancelPendingGroupOpsContent(): void {
  const session = activeGroupOpsContentSession;
  if (!session || !session.resolving) return;
  groupOpsContentSequence += 1;
  session.controller.abort();
  releaseGroupOpsContentSession(session);
}
function presentGroupOpsContentComposer(options: GroupOpsContentBridgeOptions, selectedRecords: ContentMaterialRecord[], session: GroupOpsContentSession): void {
  openContentComposer({
    title: String(options.title || '配置群运营动作内容'),
    value: options.value || {}, selectedRecords, materialOrder: 'caller_persisted', ordering: 'all_persisted',
    textEnabled: true, materialKinds: materialKinds as readonly ContentMaterialKind[], limits: groupOpsMaterialLimits, totalLimit: 9,
    textRule: {
      maximum: 1000,
      // internal/groupops validates UTF-8 runes, so use JavaScript code points
      // rather than textarea's UTF-16 maxLength semantics.
      count: (value) => Array.from(value).length,
      // Match validText in internal/groupops/app: the caller gets an explicit
      // error instead of previewing one value and silently trimming it on save.
      validate: groupOpsMessageIssue,
      normalize: (value) => value,
    },
    validateContent: ({ package: contentPackage, selectedRecords: draftRecords }) => {
      if (!contentPackage.content_text && !draftRecords.length) return '请填写话术或添加至少一项素材后再确认。';
      return undefined;
    },
    variableNotice: '当前场景未声明可用变量；话术按原文保存和展示。',
    selectMaterials: chooseGroupOpsMaterials,
    onConfirm: async (result) => {
      if (activeGroupOpsContentSession !== session || !contentBridgeCurrent(options)) {
        throw new Error('当前动作已关闭或切换，未更新草稿。');
      }
      await options.onConfirm?.(result);
      releaseGroupOpsContentSession(session);
    },
    onCancel: () => {
      releaseGroupOpsContentSession(session);
      options.onCancel?.();
    },
  });
}
function presentGroupOpsReadonlyContent(options: GroupOpsReadonlyContentOptions, selectedRecords: ContentMaterialRecord[], session: GroupOpsContentSession): void {
  openReadonlyContentPresentation({
    title: String(options.title || '已保存群运营内容'), value: options.value || {}, selectedRecords,
    materialOrder: 'caller_persisted', variableNotice: '当前场景未声明可用变量；话术按原文保存和展示。',
    onClose: () => releaseGroupOpsContentSession(session),
  });
}
function beginGroupOpsContentSession(options: GroupOpsContentBridgeOptions): GroupOpsContentSession | undefined {
  if (activeGroupOpsContentSession) return undefined;
  const target = options.loadingTarget instanceof HTMLButtonElement ? options.loadingTarget : undefined;
  const session: GroupOpsContentSession = {
    token: ++groupOpsContentSequence,
    controller: new AbortController(),
    resolving: true,
    loadingTarget: target,
    loadingLabel: target?.textContent || undefined,
  };
  activeGroupOpsContentSession = session;
  setContentLoading(session, true);
  return session;
}
function resolveGroupOpsContentSession(
  options: GroupOpsContentBridgeOptions,
  present: (options: GroupOpsContentBridgeOptions, records: ContentMaterialRecord[], session: GroupOpsContentSession) => void,
): void {
  const session = beginGroupOpsContentSession(options);
  if (!session) return;
  const initial = contentRecordsFromUnknown(options.selectedRecords);
  const resolved = initial.length ? resolveGroupOpsContentRecords(initial, session.controller) : Promise.resolve(initial);
  void resolved.then((records) => {
    if (activeGroupOpsContentSession !== session || session.token !== groupOpsContentSequence || !contentBridgeCurrent(options)) {
      releaseGroupOpsContentSession(session);
      return;
    }
    session.resolving = false;
    setContentLoading(session, false);
    present(options, records, session);
  }).catch(() => {
    if (activeGroupOpsContentSession !== session) return;
    releaseGroupOpsContentSession(session);
    const target = options.loadingTarget instanceof HTMLElement ? options.loadingTarget : undefined;
    if (target?.isConnected) target.setAttribute('data-v3-content-load-error', '1');
  });
}
function openGroupOpsContentComposer(options: GroupOpsContentBridgeOptions): void {
  resolveGroupOpsContentSession(options, presentGroupOpsContentComposer);
}
function openGroupOpsReadonlyContent(options: GroupOpsReadonlyContentOptions): void {
  resolveGroupOpsContentSession(options, presentGroupOpsReadonlyContent);
}

(window as any).AICRMGroupOpsV3Content = { open: openGroupOpsContentComposer, openReadonly: openGroupOpsReadonlyContent, cancelPending: cancelPendingGroupOpsContent };

(window as any).AdminApi = {
  ...(window as any).AdminApi,
  requestJson,
  escapeHtml: html,
  errorMessage,
  responseErrorMessage: (_response: unknown, data: Json, fallback: string) =>
    responseMessage(data, fallback),
};
// Keep the frozen save flow intact, but observe the promise its DOM listener
// returns. Only this root's two save controls are bridged; no global event
// prototype or unrelated business action is changed.
function installSaveFailureFeedback(): void {
  const app = document.getElementById("group-ops-app");
  if (!app) return;
  const query = app.querySelectorAll.bind(app);
  const decorated = new WeakSet<Element>();
  const clear = () => app.querySelector('[data-groupops-save-error]')?.remove();
  const report = (error: unknown) => {
    let alert = app.querySelector<HTMLElement>('[data-groupops-save-error]');
    if (!alert) {
      alert = document.createElement("div");
      alert.dataset.groupopsSaveError = "1";
      alert.setAttribute("role", "alert");
      alert.className = "group-ops__notice";
      alert.style.color = "#b42318";
      alert.style.backgroundColor = "#fff1f0";
      alert.style.borderColor = "#fda29b";
      app.prepend(alert);
    }
    alert.textContent = `保存失败：${errorMessage(error, "请重试")}；当前填写内容已保留。`;
  };
  app.querySelectorAll = ((selector: string) => {
    const nodes = query(selector);
    if (selector === "[data-action]") for (const element of nodes) {
      if (!element.matches('[data-action="save-plan"],[data-action="save-active-detail-panel"]') || decorated.has(element)) continue;
      decorated.add(element);
      const add = element.addEventListener.bind(element);
      element.addEventListener = ((type: string, listener: EventListenerOrEventListenerObject | null, options?: boolean | AddEventListenerOptions) => {
        if (!listener) return;
        if (type !== "click") return add(type, listener, options);
        add(type, (event: Event) => {
          clear();
          try {
            const result = typeof listener === "function" ? listener.call(element, event) : listener.handleEvent(event);
            void Promise.resolve(result).catch(report);
          } catch (error) { report(error); }
        }, options);
      }) as typeof element.addEventListener;
    }
    return nodes;
  }) as typeof app.querySelectorAll;
}
function groupRecord(asset: Json, directoryItem?: Json): GroupPickerRecord {
  const reference = String(asset.asset_reference || asset.chat_reference || "").trim();
  const displayName = String(directoryItem?.display_name || asset.display_name || "群名称待同步").trim() || "群名称待同步";
  return {
    chat_reference: reference,
    display_name: displayName,
    owner_staff_id: Number.isSafeInteger(Number(directoryItem?.owner_staff_id)) ? Number(directoryItem?.owner_staff_id) : undefined,
    member_count: Number.isFinite(Number(directoryItem?.member_count)) ? Number(directoryItem?.member_count) : undefined,
    external_member_count: directoryItem?.external_member_count === null ? null : Number.isFinite(Number(directoryItem?.external_member_count)) ? Number(directoryItem?.external_member_count) : undefined,
    unavailable_reason: directoryItem ? undefined : "群目录状态待确认，仍保留已绑定记录。",
  };
}

async function selectedGroupRecords(planID: number): Promise<{ records: GroupPickerRecord[]; ownerStaffID: number | undefined; status: string }> {
  const value = await detail(planID);
  const current = value.plan || value;
  const ownerStaffID = Number(current?.owner?.staff_id);
  const owner = Number.isSafeInteger(ownerStaffID) && ownerStaffID > 0 ? ownerStaffID : undefined;
  return {
    ownerStaffID: owner,
    status: String(current?.status || ""),
    records: (value.group_assets || []).flatMap((asset: Json) => {
      // The bound plan asset is authoritative even when the local directory
      // is unavailable. A previous scoped picker page may enrich it, but opening
      // never discards or blocks a persisted binding behind a full crawl.
      const reference = String(asset.asset_reference || "");
      const row = groupRecord(asset, planGroupDirectoryViews.get(planID)?.get(reference));
      return row.chat_reference ? [row] : [];
    }),
  };
}

function updateGroupRevision(planID: number, value: Json): void {
  const candidate = value.plan && typeof value.plan === "object" ? value.plan : value;
  const next = Number(candidate.revision);
  if (Number.isSafeInteger(next) && next >= 0) revisions.set(planID, next);
}

function selectionSignature(added: GroupPickerRecord[], removed: GroupPickerRecord[]): string {
  return [
    ...added.map((record) => `add:${record.chat_reference}`),
    ...removed.map((record) => `remove:${record.chat_reference}`),
  ].sort().join("|");
}

function selectionOperation(planID: number, added: GroupPickerRecord[], removed: GroupPickerRecord[]): GroupSelectionOperation {
  const signature = selectionSignature(added, removed);
  const previous = groupSelectionOperations.get(planID);
  if (previous?.signature === signature) return previous;
  const operation: GroupSelectionOperation = { signature, steps: new Map() };
  const prefix = `groupops-group-selection-${planID}-${crypto.randomUUID()}`;
  for (const [kind, records] of [["add", added], ["remove", removed]] as const) {
    for (const record of records) {
      const reference = String(record.chat_reference || "").trim();
      if (!reference) continue;
      const stepKey = `${kind}:${reference}`;
      operation.steps.set(stepKey, { planID, kind, reference, idempotencyKey: `${prefix}-${operation.steps.size + 1}` });
    }
  }
  groupSelectionOperations.set(planID, operation);
  return operation;
}

type PersistedGroupSelection = { value: Json; references: Set<string>; revision: number };

async function readPersistedGroupSelection(planID: number): Promise<PersistedGroupSelection> {
  const value = await detail(planID);
  updateGroupRevision(planID, value);
  const current = value.plan && typeof value.plan === "object" ? value.plan : value;
  const revisionValue = Number(current.revision);
  if (!Number.isSafeInteger(revisionValue) || revisionValue < 1) throw new Error("群聊绑定读回缺少有效版本，请刷新计划后重试。");
  return {
    value,
    references: new Set((value.group_assets || []).map((asset: Json) => String(asset.asset_reference || "")).filter(Boolean)),
    revision: revisionValue,
  };
}

function stepReached(selection: PersistedGroupSelection, step: GroupSelectionStep): boolean {
  return step.kind === "add" ? selection.references.has(step.reference) : !selection.references.has(step.reference);
}

function partialSaveError(cause: unknown, confirmed: GroupSelectionStep[]): Error {
  const confirmedText = confirmed.length
    ? `已实际保存：${confirmed.map((step) => `${step.kind === "add" ? "添加" : "移除"} ${step.reference}`).join("、")}；`
    : "尚未确认新的保存步骤；";
  return new Error(`${confirmedText}${errorMessage(cause, "保存结果未确认")}。已保留本次选择，请使用原确认操作重试未完成差异。`);
}

function updateRenderedGroupBindings(planID: number, persisted: PersistedGroupSelection, selected: GroupPickerRecord[]): void {
  // This cache intentionally retains source directory records. The renderer
  // projection (`groupView`) has different field names, so caching it here
  // would make a confirmed group fall back to “群名称待同步” on reopen.
  const directoryViews = planGroupDirectoryViews.get(planID) || new Map<string, GroupPickerRecord>();
  for (const record of selected) directoryViews.set(record.chat_reference, { ...record });
  planGroupDirectoryViews.set(planID, directoryViews);
  const rows: Json[] = (persisted.value.group_assets || []).map((asset: Json): Json => groupView(asset, directoryViews.get(String(asset.asset_reference || ""))));
  planGroupViews.set(planID, rows);
  const summaryValue = summarizeGroups(rows);
  const summaryView = planSummaryViews.get(planID) || {};
  Object.assign(summaryView, summaryValue);
  planSummaryViews.set(planID, summaryView);

  // Let the existing Group Ops renderer own the DOM refresh and event binding.
  // It calls this Host's cached request adapter, so the immediate readback stays
  // local and no full directory is introduced merely to repaint a binding.
  window.dispatchEvent(new CustomEvent("aicrm:groupops-detail-refresh", { detail: { planId: planID } }));
}

async function refreshGroupBindingsAfterCancel(planID: number): Promise<void> {
  try {
    const persisted = await readPersistedGroupSelection(planID);
    updateRenderedGroupBindings(planID, persisted, []);
    window.setTimeout(() => {
      const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
      if (notice) {
        notice.hidden = false;
        notice.textContent = "已关闭群聊选择；已保存的群聊不会因取消而撤销。";
      }
    }, 0);
  } catch (error) {
    const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
    if (notice) {
      notice.hidden = false;
      notice.textContent = `已关闭群聊选择；无法读回实际绑定，请重新打开页面查看：${errorMessage(error, "读取失败")}`;
    }
  }
}

function commandForStep(step: GroupSelectionStep, revision: number): { url: string; method: string; body: Json } {
  if (!step.body) step.body = step.kind === "add"
    ? { expected_revision: revision, asset_reference: step.reference }
    : { expected_revision: revision };
  return {
    url: step.kind === "add"
      ? `${base}/plans/${currentSelectionPlanID(step)}/groups`
      : `${base}/plans/${currentSelectionPlanID(step)}/groups/${encodeURIComponent(step.reference)}`,
    method: step.kind === "add" ? "POST" : "DELETE",
    body: step.body,
  };
}

// The plan ID is not part of a receipt key, but command URLs are. Attach it
// once when an operation is created so a later explicit retry sends the exact
// same full command body and endpoint.
function currentSelectionPlanID(step: GroupSelectionStep): number {
  const planID = Number(step.planID);
  if (!Number.isSafeInteger(planID) || planID < 1) throw new Error("群聊保存步骤缺少计划标识。");
  return planID;
}

function explicitCASConflict(error: unknown): boolean {
  return (error as { status?: unknown })?.status === 409;
}

async function saveGroupSelection(planID: number, selected: GroupPickerRecord[], added: GroupPickerRecord[], removed: GroupPickerRecord[]): Promise<void> {
  const operation = selectionOperation(planID, added, removed);
  let persisted = await readPersistedGroupSelection(planID);
  const confirmed: GroupSelectionStep[] = [];
  for (const step of operation.steps.values()) {
    if (stepReached(persisted, step)) continue;
    try {
      const command = commandForStep(step, persisted.revision);
      const value = await nativeRequest(command.url, {
        method: command.method,
        idempotencyKey: step.idempotencyKey,
        body: command.body,
      });
      updateGroupRevision(planID, value);
      persisted = await readPersistedGroupSelection(planID);
      if (!stepReached(persisted, step)) throw new Error("群聊保存后读回未达到原选择，请刷新后检查。");
      confirmed.push(step);
    } catch (cause) {
      // A lost response is outcome-unknown. Read the Owner fact first; only a
      // matching receipt replay with the frozen command may prove completion.
      try {
        persisted = await readPersistedGroupSelection(planID);
        if (stepReached(persisted, step)) {
          confirmed.push(step);
          continue;
        }
      } catch {
        // Keep the original cause; a failed readback is not permission to guess.
      }
      if (explicitCASConflict(cause)) {
        // HTTP 409 proves this exact command did not mutate. Drop only this
        // operation after preserving the draft so the next explicit confirm
        // can make a new intent from the current revision and new keys.
        groupSelectionOperations.delete(planID);
        throw new Error(`${partialSaveError(cause, confirmed).message} 计划版本已变化；该步骤未提交，请再次确认后创建新的保存意图。`);
      }
      throw partialSaveError(cause, confirmed);
    }
  }
  updateRenderedGroupBindings(planID, persisted, selected);
  groupSelectionOperations.delete(planID);
}

function installGroupPickerBridge(): void {
  document.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest<HTMLButtonElement>("#group-ops-app button[data-action='open-group-picker']") : null;
    if (!target) return;
    const app = document.getElementById("group-ops-app");
    const planID = Number(app?.dataset.planId);
    if (!Number.isSafeInteger(planID) || planID < 1) return;
    if (openingGroupPicker || activeGroupPickerPlan === planID || document.querySelector('[data-v3-selection-session="group"]')) return;
    // Capture before the frozen donor's click listener. The standard picker
    // remains untouched; this V3 overlay has no legacy raw chat_id channel.
    event.preventDefault();
    event.stopImmediatePropagation();
    openingGroupPicker = true;
    target.disabled = true;
    void (async () => {
      try {
        const initial = await selectedGroupRecords(planID);
        activeGroupPickerPlan = planID;
        openGroupPicker({
          source: `groupops-plan-${planID}`,
          scope: "group_ops.plan_group_assets",
          selectedRecords: initial.records,
          readonlyReason: initial.status === "draft" ? undefined : initial.status === "archived"
            ? "计划已归档，不能修改群聊。"
            : "当前计划状态不允许修改群聊；仅草稿计划可编辑。",
          loadPage: async ({ query, cursor, signal }) => {
            const offset = Number(cursor || "0");
            if (!Number.isSafeInteger(offset) || offset < 0) throw new Error("群目录分页标记无效，请重新打开选择器。");
            const params = new URLSearchParams({ limit: "50", offset: String(offset) });
            // The Owner port scopes this local projection before pagination.
            // Client-side disabled text remains a defensive presentation of
            // any historic/cached record, not a substitute for server scope.
            if (initial.ownerStaffID) params.set("owner_userid", String(initial.ownerStaffID));
            if (query.trim()) params.set("q", query.trim());
            const page = await nativeRequest(`${base}/groups?${params.toString()}`, { signal });
            if (signal.aborted) throw new DOMException("群目录读取已替换", "AbortError");
            const items = Array.isArray(page.items) ? page.items : [];
            const records = items.flatMap((entry: Json) => {
              const record = groupRecord({ chat_reference: entry.chat_reference }, entry);
              if (!record.chat_reference) return [];
              // Cache the raw directory record before adding picker-only
              // disabled text, so the plan renderer keeps actual owner/counts.
              const directoryViews = planGroupDirectoryViews.get(planID) || new Map<string, GroupPickerRecord>();
              directoryViews.set(record.chat_reference, { ...record });
              planGroupDirectoryViews.set(planID, directoryViews);
              if (!initial.ownerStaffID) record.unavailable_reason = "计划尚未配置负责人，不能选择新群。";
              else if (record.owner_staff_id !== initial.ownerStaffID) record.unavailable_reason = "当前负责人不可管理此群。";
              return [record];
            });
            return {
              items: records,
              nextCursor: page.has_more === true && items.length ? String(offset + items.length) : undefined,
            };
          },
          accessLossMessage: (error) => { const status = (error as { status?: unknown }).status; return status === 401 || status === 403 ? '群目录权限已失效；已绑定群仍可查看，请取消后重新登录。' : undefined; },
          onCommit: async ({ selected, added, removed }) => {
            await saveGroupSelection(planID, selected, added, removed);
            activeGroupPickerPlan = undefined;
          },
          onCancel: ({ saveAttempted }) => {
            activeGroupPickerPlan = undefined;
            // A pure draft cancel never contacted the Owner, so do not imply a
            // rollback or a persisted binding. After any save attempt, however,
            // only Owner readback can state what remains real.
            if (saveAttempted) void refreshGroupBindingsAfterCancel(planID);
          },
        });
      } catch (error) {
        activeGroupPickerPlan = undefined;
        const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
        if (notice) { notice.hidden = false; notice.textContent = `群聊选择器无法打开：${errorMessage(error, "请重试")}`; }
      } finally {
        openingGroupPicker = false;
        target.disabled = false;
      }
    })();
  }, true);
}
installGroupPickerBridge();
installStaffPickerAdapter();

installSaveFailureFeedback();
// @ts-expect-error The standard donor script is intentionally JavaScript.
void import("./groupOpsStandard.js");
export {};
