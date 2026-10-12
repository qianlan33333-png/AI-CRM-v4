import { showSurveyAnswers } from './surveyAnswerDialog';
import { request } from '../src/api/transport';

type CodeRow = { code: string; used_submission_id?: number; used_at?: string };
type CodePage = { items: CodeRow[]; total: number; available: number; used: number };
export function mountRedemptionLibrary(root: HTMLElement, qid: number): void {
  const panel = root.querySelector<HTMLElement>('[data-panel="codes"]')!;
  let offset = 0;
  let total = 0;
  let pending = false;
  let importedFile: File | undefined;
  let importKey = '';
  const message = panel.querySelector<HTMLElement>('[data-code-message]')!;
  const rows = panel.querySelector<HTMLTableSectionElement>('[data-code-rows]')!;
  const previous = panel.querySelector<HTMLButtonElement>('[data-code-previous]')!;
  const next = panel.querySelector<HTMLButtonElement>('[data-code-next]')!;
  const upload = panel.querySelector<HTMLButtonElement>('[data-code-import]')!;
  const input = panel.querySelector<HTMLInputElement>('[data-code-file]')!;
  const path = `/api/admin/questionnaires/${qid}/redemption-codes`;
  const failure = (error: unknown): string => {
    const value = error as { details?: { message?: string }; message?: string };
    return value.details?.message || value.message || '兑换码库读取失败，请重试';
  };
  const busy = (active: boolean): void => {
    pending = active; upload.disabled = active; input.disabled = active;
    previous.disabled = active || offset === 0; next.disabled = active || offset + 100 >= total;
  };
  async function load(): Promise<boolean> {
    if (pending) return false;
    busy(true); message.textContent = '正在读取兑换码库…';
    try {
      const response = await request(`${path}?limit=100&offset=${offset}`);
      const page = await response.json() as CodePage;
      if (!Array.isArray(page.items) || !Number.isSafeInteger(page.total) || !Number.isSafeInteger(page.available) || !Number.isSafeInteger(page.used)) throw new Error('兑换码库响应不完整');
      total = page.total;
      panel.querySelector<HTMLElement>('[data-code-counts]')!.textContent = `共 ${page.total} 个 · 未使用 ${page.available} 个 · 已使用 ${page.used} 个`;
      rows.replaceChildren();
      for (const item of page.items) {
        const row = document.createElement('tr');
        for (const value of [item.code, item.used_submission_id ? '已使用' : '未使用', item.used_at ? new Date(item.used_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) : '—', item.used_submission_id ? String(item.used_submission_id) : '—']) {
          const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
        }
        if (item.used_submission_id) {const cell=row.lastElementChild!;const link=document.createElement('button');link.textContent=String(item.used_submission_id);link.style.cssText='border:0;background:none;color:#3370ff;font:inherit;padding:6px 0;cursor:pointer';link.onclick=()=>void showSurveyAnswers(qid,item.used_submission_id);cell.replaceChildren(link);}
        rows.append(row);
      }
      message.textContent = page.items.length ? '' : '兑换码库为空，请下载模板后导入。';
      return true;
    } catch (error) {
      rows.replaceChildren(); panel.querySelector<HTMLElement>('[data-code-counts]')!.textContent = '';
      message.textContent = failure(error);return false;
    } finally { busy(false); }
  }
  input.onchange = () => { importedFile = input.files?.[0]; importKey = crypto.randomUUID(); message.textContent = ''; };
  upload.onclick = async () => {
    if (pending) return;
    if (!importedFile) { message.textContent = '请选择 .xlsx 兑换码文件'; input.focus(); return; }
    busy(true); message.textContent = '正在导入兑换码…';
    const form = new FormData(); form.append('file', importedFile);
    try {
      const response = await request(`${path}/imports`, { method: 'POST', headers: { 'Idempotency-Key': importKey }, body: form });
      const result = await response.json() as { imported: number; duplicates: number; empty_rows: number };
      if (![result.imported, result.duplicates, result.empty_rows].every(Number.isSafeInteger)) throw new Error('导入结果尚未确认，请用同一文件重试');
      offset = 0; busy(false); if (!await load()) {message.textContent = `导入已接受，但重新读取失败：${message.textContent}，请点击刷新确认。`; return;}
      message.textContent = `新增 ${result.imported} 个，跳过重复 ${result.duplicates} 个、空行 ${result.empty_rows} 行。`;
    } catch (error) { message.textContent = failure(error); }
    finally { busy(false); }
  };
  previous.onclick = () => { offset = Math.max(0, offset - 100); void load(); };
  next.onclick = () => { offset += 100; void load(); };
  panel.querySelector<HTMLButtonElement>('[data-code-refresh]')!.onclick = () => void load();
  panel.querySelector<HTMLAnchorElement>('[data-code-template]')!.href = `${path}/template`;
  root.querySelector<HTMLButtonElement>('[data-tab="codes"]')!.addEventListener('click', () => void load());
}
