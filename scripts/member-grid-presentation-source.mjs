// Owner: V3 web presentation. Keep the frozen module byte-exact; derive only
// presentation and single-flight reading for the public share Host. Remove
// this seam when memberGridShare becomes an explicitly V3-owned source.
import fs from 'node:fs/promises';
import crypto from 'node:crypto';

export function deriveMemberGridPresentation(source) {
  const digest = crypto.createHash('sha256').update(source).digest('hex');
  if (digest !== '30e7ab94dbed4bf48893ea95a82512ae457a2c9e9ab8906107d4b53e4c213660') {
    throw new Error('member-grid presentation: frozen source digest mismatch');
  }
  const replace = (before, after) => {
    if (source.split(before).length !== 2) throw new Error('member-grid presentation anchor mismatch');
    source = source.replace(before, after);
  };
  replace('  panel.append(title, list, asOf, table);', `  const scroll = document.createElement('div');
  scroll.className = 'v3-share-table-scroll';
  scroll.setAttribute('role', 'region');
  scroll.setAttribute('aria-label', '会员列表，可横向滚动查看全部列');
  scroll.tabIndex = 0;
  scroll.append(table);
  panel.append(title, list, asOf, scroll);
  if (summary.rows.length === 0) {
    const empty = document.createElement('p');
    empty.setAttribute('role', 'status');
    empty.textContent = '暂无成员';
    panel.append(empty);
  }`);
  replace('  let current: Summary | undefined;', `  let current: Summary | undefined;
  let reading = false;`);
  replace("    if (!current) renderLoading(stage);", `    if (reading) return;
    reading = true;
    if (!current) renderLoading(stage);
    stage.setAttribute('aria-busy', 'true');
    stage.querySelector('[data-share-read-error]')?.remove();
    const disabledButtons = Array.from(stage.querySelectorAll<HTMLButtonElement>('button')).map(button => ({ button, disabled: button.disabled, busy: button.getAttribute('aria-busy'), nodes: Array.from(button.childNodes) }));
    for (const { button } of disabledButtons) {
      button.disabled = true;
      button.setAttribute('aria-busy', 'true');
      button.textContent = '加载中…';
      button.classList.add('surface-feedback__control--busy');
    }`);
  replace(`    } catch {
      renderFailure(stage);
    }`, `    } catch {
      // A failed next-page read must not destroy previously loaded rows.
      if (!current) renderFailure(stage);
      const notice = document.createElement('div');
      notice.dataset.shareReadError = 'true';
      notice.setAttribute('role', 'alert');
      const message = document.createElement('p');
      message.textContent = current ? '加载更多失败，已保留当前列表。' : '读取失败，请重试。';
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.textContent = '重试读取';
      // Reuse this closure and its existing token; never re-add it to the URL.
      retry.addEventListener('click', () => { void load(cursor); });
      notice.append(message, retry);
      stage.append(notice);
    } finally {
      reading = false;
      stage.removeAttribute('aria-busy');
      for (const { button, disabled, busy, nodes } of disabledButtons) {
        button.disabled = disabled;
        if (busy === null) button.removeAttribute('aria-busy'); else button.setAttribute('aria-busy', busy);
        button.classList.remove('surface-feedback__control--busy');
        button.replaceChildren(...nodes);
      }
    }`);
  return source;
}

export const memberGridPresentationPlugin = {
  name: 'v3-member-grid-presentation',
  setup(build) {
    build.onLoad({ filter: /[/\\]web[/\\]src[/\\]public[/\\]memberGridShare\.ts$/ }, async ({ path }) => ({
      contents: deriveMemberGridPresentation(await fs.readFile(path, 'utf8')),
      loader: 'ts',
    }));
  },
};
