import {
  createWeComCustomerAcquisitionLink as createGeneratedLink,
  deleteWeComCustomerAcquisitionLink as deleteGeneratedLink,
  getWeComCustomerAcquisitionLink as getGeneratedLink,
  listWeComCustomerAcquisitionLinks as listGeneratedLinks,
  reconcileWeComCustomerAcquisitionLink as reconcileGeneratedLink,
  updateWeComCustomerAcquisitionLink as updateGeneratedLink,
} from "./generated/p4-channel/p4-channel";
import {
  type CustomerAcquisitionLink,
  type CustomerAcquisitionLinkInput,
  type CustomerAcquisitionLinkReceipt,
  type CustomerAcquisitionLinkReceiptResolution,
  type CustomerAcquisitionLinkReceiptState,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from "./transport";

export type WeComAcquisitionLinkPage = {
  items: Array<{ link_id: string }>;
  next_cursor: string;
};
export type WeComAcquisitionLinkOutcome =
  "pending" | "applied" | "failed" | "unknown" | "not_applied";
export type WeComAcquisitionLinkWriteResult = {
  receipt: CustomerAcquisitionLinkReceipt;
  outcome: WeComAcquisitionLinkOutcome;
  canReconcile: boolean;
};
export type { CustomerAcquisitionLink, CustomerAcquisitionLinkInput };

type Row = Record<string, unknown>;
type Operation = "create" | "update" | "delete" | "reconcile";
const states = new Set<CustomerAcquisitionLinkReceiptState>([
  "accepted",
  "attempted",
  "executed",
  "final_failed",
  "outcome_unknown",
  "reconciled",
]);
const resolutions = new Set<
  Exclude<CustomerAcquisitionLinkReceiptResolution, null>
>(["provider_applied", "provider_not_applied"]);
const digestPattern = /^[a-f0-9]{64}$/;
const encoder = new TextEncoder();

const invalid = (message: string): never => {
  throw new Error(`企微获客链接${message}，已拒绝渲染或提交`);
};
const integer = (value: unknown, minimum: number): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= minimum;
const boolean = (value: unknown): value is boolean =>
  typeof value === "boolean";

function record(
  value: unknown,
  required: readonly string[],
  optional: readonly string[] = [],
): Row {
  if (!value || typeof value !== "object" || Array.isArray(value))
    invalid("响应不完整");
  const row = value as Row;
  const keys = Object.keys(row);
  if (
    required.some((key) => !(key in row)) ||
    keys.some((key) => !required.includes(key) && !optional.includes(key))
  )
    invalid("响应不完整");
  return row;
}

function text(
  value: unknown,
  minimum: number,
  maximum: number,
): value is string {
  return (
    typeof value === "string" &&
    encoder.encode(value).length >= minimum &&
    encoder.encode(value).length <= maximum &&
    value.trim() === value &&
    !/[\u0000-\u001f\u007f]/u.test(value)
  );
}

function linkID(value: unknown): value is string {
  return text(value, 1, 1024) && !/[\\/?#]/u.test(value);
}

function idempotencyKey(value: string): string {
  if (!text(value, 16, 128)) invalid("幂等键无效");
  return value;
}

function input(
  value: CustomerAcquisitionLinkInput,
): CustomerAcquisitionLinkInput {
  const row = record(value, [
    "link_name",
    "user_ids",
    "department_ids",
    "skip_verify",
  ]);
  if (
    !text(row.link_name, 1, 120) ||
    [...row.link_name].length > 30 ||
    !Array.isArray(row.user_ids) ||
    !Array.isArray(row.department_ids) ||
    !boolean(row.skip_verify)
  )
    invalid("提交内容无效");
  const userIDs = row.user_ids as unknown[];
  const departmentIDs = row.department_ids as unknown[];
  if (
    userIDs.length > 500 ||
    departmentIDs.length > 500 ||
    userIDs.length + departmentIDs.length === 0 ||
    userIDs.some((item) => !text(item, 1, 1024)) ||
    new Set(userIDs).size !== userIDs.length ||
    departmentIDs.some((item) => !integer(item, 1)) ||
    new Set(departmentIDs).size !== departmentIDs.length
  )
    invalid("提交内容无效");
  return {
    link_name: row.link_name as string,
    user_ids: [...userIDs] as string[],
    department_ids: [...departmentIDs] as number[],
    skip_verify: row.skip_verify as boolean,
  };
}

function link(value: unknown, expectedID?: string): CustomerAcquisitionLink {
  const row = record(value, [
    "link_id",
    "link_name",
    "url",
    "user_ids",
    "department_ids",
    "skip_verify",
  ]);
  if (
    !linkID(row.link_id) ||
    (expectedID !== undefined && row.link_id !== expectedID) ||
    !text(row.link_name, 1, 120) ||
    [...row.link_name].length > 30 ||
    !text(row.url, 1, 10000) ||
    !String(row.url).startsWith("https://") ||
    !Array.isArray(row.user_ids) ||
    !Array.isArray(row.department_ids) ||
    !boolean(row.skip_verify)
  )
    invalid("响应链接不完整");
  const userIDs = row.user_ids as unknown[];
  const departmentIDs = row.department_ids as unknown[];
  if (
    userIDs.length > 500 ||
    departmentIDs.length > 500 ||
    userIDs.some((item) => !text(item, 1, 1024)) ||
    new Set(userIDs).size !== userIDs.length ||
    departmentIDs.some((item) => !integer(item, 1)) ||
    new Set(departmentIDs).size !== departmentIDs.length
  )
    invalid("响应链接不完整");
  return row as unknown as CustomerAcquisitionLink;
}

function receipt(
  value: unknown,
  operation: Operation,
  expectedLinkID?: string,
  expectedReceiptID?: number,
): WeComAcquisitionLinkWriteResult {
  const row = record(
    value,
    [
      "receipt_id",
      "state",
      "business_endpoint_dispatched",
      "real_external_call_executed",
    ],
    ["link", "resolution", "outcome_digest"],
  );
  if (
    !integer(row.receipt_id, 1) ||
    (expectedReceiptID !== undefined && row.receipt_id !== expectedReceiptID) ||
    typeof row.state !== "string" ||
    !states.has(row.state as CustomerAcquisitionLinkReceiptState) ||
    !boolean(row.business_endpoint_dispatched) ||
    !boolean(row.real_external_call_executed)
  )
    invalid("回执不完整");
  const state = row.state as CustomerAcquisitionLinkReceiptState;
  const hasDigest =
    typeof row.outcome_digest === "string" &&
    digestPattern.test(row.outcome_digest);
  const hasResolution =
    typeof row.resolution === "string" &&
    resolutions.has(
      row.resolution as Exclude<CustomerAcquisitionLinkReceiptResolution, null>,
    );
  const responseLink =
    row.link === undefined ? undefined : link(row.link, expectedLinkID);

  if (
    (state === "accepted" || state === "attempted") &&
    (row.business_endpoint_dispatched ||
      row.real_external_call_executed ||
      row.link !== undefined ||
      row.resolution !== undefined ||
      row.outcome_digest !== undefined)
  )
    invalid("尚未执行回执却声称成功");
  if (
    state === "executed" &&
    (!row.business_endpoint_dispatched ||
      !row.real_external_call_executed ||
      !hasDigest ||
      row.resolution !== undefined ||
      (operation !== "delete" && responseLink === undefined) ||
      (operation === "delete" && responseLink !== undefined))
  )
    invalid("执行回执缺少外部事实");
  if (
    state === "final_failed" &&
    (!hasDigest ||
      row.business_endpoint_dispatched !== row.real_external_call_executed ||
      row.resolution !== undefined ||
      responseLink !== undefined)
  )
    invalid("最终失败回执不一致");
  if (
    state === "outcome_unknown" &&
    (!row.business_endpoint_dispatched ||
      !hasDigest ||
      row.resolution !== undefined ||
      responseLink !== undefined)
  )
    invalid("未知结果回执不一致");
  if (
    state === "reconciled" &&
    (!row.business_endpoint_dispatched ||
      !row.real_external_call_executed ||
      !hasDigest ||
      !hasResolution)
  )
    invalid("对账回执不完整");

  const outcome: WeComAcquisitionLinkOutcome =
    state === "executed" ||
    (state === "reconciled" && row.resolution === "provider_applied")
      ? "applied"
      : state === "final_failed"
        ? "failed"
        : state === "outcome_unknown"
          ? "unknown"
          : state === "reconciled"
            ? "not_applied"
            : "pending";
  return {
    receipt: {
      ...row,
      ...(responseLink === undefined ? {} : { link: responseLink }),
    } as unknown as CustomerAcquisitionLinkReceipt,
    outcome,
    canReconcile: state === "outcome_unknown",
  };
}

function mutationOptions(key: string): RequestInit {
  return apiRequestOptions({
    headers: { "Idempotency-Key": idempotencyKey(key) },
  });
}

export async function listWeComAcquisitionLinks(
  cursor = "",
  limit = 100,
): Promise<WeComAcquisitionLinkPage> {
  if (!text(cursor, 0, 1024) || !integer(limit, 1) || limit > 100)
    invalid("分页条件无效");
  const value = record(
    unwrapGenerated(
      await listGeneratedLinks(
        { ...(cursor ? { cursor } : {}), limit },
        apiRequestOptions(),
      ),
    ),
    ["items", "next_cursor"],
  );
  if (
    !Array.isArray(value.items) ||
    !text(value.next_cursor, 0, 1024) ||
    value.items.length > limit
  )
    invalid("列表响应不完整");
  const items = (value.items as unknown[]).map((item) => {
    const row = record(item, ["link_id"]);
    if (!linkID(row.link_id)) invalid("列表链接 ID 无效");
    return row as { link_id: string };
  });
  if (new Set(items.map((item) => item.link_id)).size !== items.length)
    invalid("列表链接 ID 重复");
  return { items, next_cursor: value.next_cursor as string };
}

export async function getWeComAcquisitionLink(
  linkId: string,
): Promise<CustomerAcquisitionLink> {
  if (!linkID(linkId)) invalid(" ID 无效");
  return link(
    unwrapGenerated(await getGeneratedLink(linkId, apiRequestOptions())),
    linkId,
  );
}

export async function createWeComAcquisitionLink(
  value: CustomerAcquisitionLinkInput,
  key: string,
): Promise<WeComAcquisitionLinkWriteResult> {
  return receipt(
    unwrapGenerated(
      await createGeneratedLink(input(value), mutationOptions(key)),
    ),
    "create",
  );
}

export async function updateWeComAcquisitionLink(
  linkId: string,
  value: CustomerAcquisitionLinkInput,
  key: string,
): Promise<WeComAcquisitionLinkWriteResult> {
  if (!linkID(linkId)) invalid(" ID 无效");
  return receipt(
    unwrapGenerated(
      await updateGeneratedLink(linkId, input(value), mutationOptions(key)),
    ),
    "update",
    linkId,
  );
}

export async function deleteWeComAcquisitionLink(
  linkId: string,
  key: string,
): Promise<WeComAcquisitionLinkWriteResult> {
  if (!linkID(linkId)) invalid(" ID 无效");
  return receipt(
    unwrapGenerated(await deleteGeneratedLink(linkId, mutationOptions(key))),
    "delete",
    linkId,
  );
}

export async function reconcileWeComAcquisitionLink(
  linkId: string,
  receiptId: number,
  resolution: Exclude<CustomerAcquisitionLinkReceiptResolution, null>,
  evidenceDigest: string,
  key: string,
): Promise<WeComAcquisitionLinkWriteResult> {
  if (
    !linkID(linkId) ||
    !integer(receiptId, 1) ||
    !resolutions.has(resolution) ||
    !digestPattern.test(evidenceDigest)
  )
    invalid("对账参数无效");
  return receipt(
    unwrapGenerated(
      await reconcileGeneratedLink(
        linkId,
        { receipt_id: receiptId, resolution, evidence_digest: evidenceDigest },
        mutationOptions(key),
      ),
    ),
    "reconcile",
    linkId,
    receiptId,
  );
}
