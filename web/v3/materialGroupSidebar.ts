// Shared presentation for Media facets. The owning page keeps query and writes.
export type MaterialGroupOption = { value: string; label: string; count?: number };
export function materialGroupLayout(): HTMLDivElement {
  if (!document.querySelector('#material-group-sidebar-style')) {
    const style = document.createElement('style');
    style.id = 'material-group-sidebar-style';
    style.textContent = `
      .material-group-layout{display:grid;grid-template-columns:240px minmax(0,1fr);gap:16px;min-height:0;flex:1;padding:16px 20px;overflow:auto;align-items:start}
      .material-group-sidebar{background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:14px;position:sticky;top:0;max-height:calc(100vh - 200px);overflow:auto}
      .material-group-sidebar h2{font-size:14px;margin:0 0 14px;font-weight:600}
      .material-group-sidebar nav{display:grid;gap:8px}
      .material-group-sidebar button[data-material-group-value]{display:flex;justify-content:space-between;gap:12px;width:100%;text-align:left;align-items:center;padding:12px;border:1px solid #DEE0E3;border-radius:6px;background:white;color:#1F2329;cursor:pointer;font:inherit;overflow-wrap:anywhere}
      .material-group-sidebar button[aria-pressed=true]{background:#EFF4FF;border-color:#3370ff;color:#245BDB;font-weight:600}
      .material-group-sidebar button:focus-visible{outline:2px solid #3370ff;outline-offset:2px}
      .material-group-sidebar [data-group-count]{color:#8F959E;flex:none}
      .material-group-content{min-width:0;min-height:0;display:flex;flex-direction:column}
      @media(max-width:760px){.material-group-layout{grid-template-columns:150px minmax(0,1fr);gap:8px;padding:12px 8px}.material-group-sidebar{padding:8px}}
    `;
    document.head.append(style);
  }
  const layout = document.createElement('div');
  layout.className = 'material-group-layout';
  return layout;
}
export class MaterialGroupSidebar {
  readonly element = document.createElement('aside');
  private readonly nav = document.createElement('nav');
  private readonly status = document.createElement('p');
  constructor(private readonly choose: (value: string) => void) {
    this.element.className = 'material-group-sidebar';
    this.element.dataset.materialGroups = 'true';
    const title = document.createElement('h2'); title.textContent = '素材分组';
    this.nav.setAttribute('aria-label', '素材分组');
    this.status.setAttribute('role', 'status'); this.status.hidden = true;
    this.element.append(title, this.nav, this.status);
  }
  render(options: MaterialGroupOption[], selected: string): void {
    this.nav.replaceChildren();
    for (const option of options) {
      const button = document.createElement('button'); button.type = 'button';
      button.dataset.materialGroupValue = option.value;
      button.setAttribute('aria-label', option.label);
      button.setAttribute('aria-pressed', String(option.value === selected));
      const label = document.createElement('span'); label.textContent = option.label;
      button.append(label);
      if (option.count !== undefined) {
        const count = document.createElement('span'); count.dataset.groupCount = 'true'; count.textContent = String(option.count); button.append(count);
      }
      button.addEventListener('click', () => this.choose(option.value));
      this.nav.append(button);
    }
  }
  select(selected: string): void {
    for (const button of this.nav.querySelectorAll<HTMLButtonElement>('button')) button.setAttribute('aria-pressed', String(button.dataset.materialGroupValue === selected));
  }
  message(text: string): void { this.status.textContent = text; this.status.hidden = !text; }
}
