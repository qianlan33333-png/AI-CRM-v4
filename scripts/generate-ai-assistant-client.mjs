#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const openapi = fs.readFileSync(path.join(repository, 'api', 'openapi.yaml'), 'utf8');
const outputPath = path.join(repository, 'web', 'v3', 'generated', 'aiAssistantClient.ts');
const requiredOperations = [
  'listAIAssistantPlans', 'getAIAssistantPlan', 'listAIAssistantRecipients', 'getAIAssistantRecipient',
  'reviewAIAssistantRecipient', 'updateAIAssistantRecipientContent', 'previewAIAssistantPlanApproval',
  'approveAIAssistantPlan', 'rejectAIAssistantPlan',
];
for (const operation of requiredOperations) {
  if (!openapi.includes(`operationId: ${operation}`)) throw new Error(`OpenAPI operation missing: ${operation}`);
}
const generated = fs.readFileSync(outputPath, 'utf8');
for (const operation of requiredOperations) {
  if (!generated.includes(`function ${operation}(`)) throw new Error(`generated AI Assistant client is stale: ${operation}`);
}
if (!generated.startsWith('/**\n * Generated from api/openapi.yaml')) throw new Error('AI Assistant client lacks generated-file marker');
console.log(`AI Assistant OpenAPI client: ${requiredOperations.length} operations OK`);
