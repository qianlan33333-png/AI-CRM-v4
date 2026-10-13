// Associate existing visible labels without changing frozen markup, layout or
// domain callbacks. Ambiguous wrappers/IDs retain their original semantics.
export function associateVisibleFieldLabels(root: ParentNode | null): void {
  if (!root) return;
  const controls = Array.from(root.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>('input[id],select[id],textarea[id]'));
  const document = controls[0]?.ownerDocument;
  if (!document) return;
  const counts = new Map<string, number>();
  for (const element of document.querySelectorAll('[id]')) counts.set(element.id, (counts.get(element.id) || 0) + 1);
  for (const control of controls) {
    if (control instanceof HTMLInputElement && control.type === 'hidden') continue;
    if (control.labels?.length || counts.get(control.id) !== 1) continue;
    const siblings = Array.from(control.parentElement?.children || []);
    const labels = siblings.filter((element): element is HTMLLabelElement => element.tagName === 'LABEL');
    const fields = siblings.filter(element => ['INPUT', 'SELECT', 'TEXTAREA'].includes(element.tagName)
      && !(element instanceof HTMLInputElement && element.type === 'hidden'));
    if (labels.length !== 1 || fields.length !== 1 || labels[0].htmlFor || !labels[0].textContent?.trim()) continue;
    labels[0].htmlFor = control.id;
  }
}
