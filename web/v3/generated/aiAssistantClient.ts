/**
 * Generated from api/openapi.yaml by scripts/generate-ai-assistant-client.mjs.
 * Do not edit manually.
 */
export type JsonRecord = Record<string, any>;
export type AIAssistantTransport = (url: string, init?: Omit<RequestInit, 'body'> & { body?: any }) => Promise<JsonRecord>;

const path = (planId: string | number) => `/api/admin/ai-assistant/plans/${planId}`;

export function listAIAssistantPlans(transport: AIAssistantTransport, input: { limit: number; keyword?: string; status?: string }): Promise<JsonRecord> {
  const query = new URLSearchParams({ limit: String(input.limit), keyword: input.keyword || '', status: input.status || '' });
  return transport(`/api/admin/ai-assistant/plans?${query}`);
}

export function getAIAssistantPlan(transport: AIAssistantTransport, planId: string | number): Promise<JsonRecord> {
  return transport(path(planId));
}

export function listAIAssistantRecipients(transport: AIAssistantTransport, planId: string | number, input: { limit: number; cursor?: string; status?: string }): Promise<JsonRecord> {
  const query = new URLSearchParams({ limit: String(input.limit), cursor: input.cursor || '', status: input.status || '' });
  return transport(`${path(planId)}/recipients?${query}`);
}

export function getAIAssistantRecipient(transport: AIAssistantTransport, planId: string | number, recipientId: string | number): Promise<JsonRecord> {
  return transport(`${path(planId)}/recipients/${recipientId}`);
}

export function reviewAIAssistantRecipient(transport: AIAssistantTransport, planId: string | number, recipientId: string | number, body: JsonRecord): Promise<JsonRecord> {
  return transport(`${path(planId)}/recipients/${recipientId}/review`, { method: 'POST', body });
}

export function updateAIAssistantRecipientContent(transport: AIAssistantTransport, planId: string | number, recipientId: string | number, body: JsonRecord): Promise<JsonRecord> {
  return transport(`${path(planId)}/recipients/${recipientId}/content`, { method: 'PATCH', body });
}

export function previewAIAssistantPlanApproval(transport: AIAssistantTransport, planId: string | number, body: JsonRecord): Promise<JsonRecord> {
  return transport(`${path(planId)}/preview-approval`, { method: 'POST', body });
}

export function approveAIAssistantPlan(transport: AIAssistantTransport, planId: string | number, body: JsonRecord): Promise<JsonRecord> {
  return transport(`${path(planId)}/approve`, { method: 'POST', body });
}

export function rejectAIAssistantPlan(transport: AIAssistantTransport, planId: string | number, body: JsonRecord): Promise<JsonRecord> {
  return transport(`${path(planId)}/reject`, { method: 'POST', body });
}
