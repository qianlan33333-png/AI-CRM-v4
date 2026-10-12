// Shared by the all-question and one-question Survey H5.
export function showSurveyFieldDialog(message: string, questionID?: number): void {
  document.querySelector('[data-survey-field-dialog]')?.remove();
  const dialog = document.createElement('dialog');
  dialog.dataset.surveyFieldDialog = '';
  dialog.className = 'survey-field-dialog';
  dialog.setAttribute('aria-label', '暂时无法提交');
  const heading = document.createElement('h2'); heading.textContent = '暂时无法提交';
  const content = document.createElement('p'); content.textContent = message;
  const close = document.createElement('button'); close.type = 'button'; close.textContent = '返回修改';
  const dismiss = (): void => {
    dialog.remove();
    if (Number.isSafeInteger(questionID)) {
      const field = document.querySelector<HTMLElement>(`[data-question-id="${questionID}"] input, [data-question-id="${questionID}"] textarea`);
      field?.focus();
      field?.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
    }
  };
  close.onclick = dismiss;
  dialog.addEventListener('cancel', event => { event.preventDefault(); dismiss(); });
  dialog.append(heading, content, close); document.body.append(dialog);
  if (typeof dialog.showModal === 'function') dialog.showModal();
  else dialog.setAttribute('open', '');
  close.focus();
}
