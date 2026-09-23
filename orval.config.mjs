import { defineConfig } from 'orval';

export default defineConfig({
  hxcDashboard: {
    input: {
      target: './api/openapi.yaml',
      filters: {
        mode: 'include',
        tags: ['HXCDashboard'],
        schemas: [/^HXC/, /^PositiveID$/, /^ErrorResponse$/, /^UnavailableResponse$/, /^OpenPlatformOAuthError$/, /^OpenPlatformV1Error$/, /^OpenPlatformV1Failure$/, /^GroupOpsWebhookError$/],
      },
    },
    output: {
      target: './web/v3/generated/hxc-dashboard.ts',
      client: 'fetch',
      // The generated directory also contains the separately generated
      // AI Assistant client. Cleaning the whole directory here makes the two
      // generators delete each other's output.
      clean: false,
      formatter: 'prettier',
      override: { aliasCombinedTypes: true },
    },
  },
  surveyPublic: {
    input: {
      target: './api/openapi.yaml',
      filters: {
        mode: 'include',
        tags: ['SurveyPublic'],
        // Orval retains global response aliases while filtering operations.
        // Include their direct dependencies so the generated standalone client
        // never points at omitted types.
        schemas: [/^SurveyPublic/, /^SurveySafeRedirectURL$/, /^CompletionAction$/, /^PositiveID$/, /^ErrorResponse$/, /^GroupOpsWebhookError$/, /^OpenPlatformOAuthError$/, /^OpenPlatformV1Error$/, /^OpenPlatformV1Failure$/],
      },
    },
    output: {
      target: './web/v3/generated/survey-public.ts',
      client: 'fetch',
      clean: false,
      formatter: 'prettier',
      override: { aliasCombinedTypes: true },
    },
  },
});
