import { openConfirmationDialog, type ConfirmationDialogOptions, type ConfirmationDialogResult } from './shared/ui/confirmationDialog';

declare global {
  interface Window {
    AICRMConfirmation?: { confirm(options: ConfirmationDialogOptions): Promise<ConfirmationDialogResult> };
  }
}

// A narrow browser bridge for existing V3-owned static admin adapters. It does
// not observe requests or perform any business action; callers own their HTTP.
if (!window.AICRMConfirmation) {
  window.AICRMConfirmation = { confirm: openConfirmationDialog };
}
