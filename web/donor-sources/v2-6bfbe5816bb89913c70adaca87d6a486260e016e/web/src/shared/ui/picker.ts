/**
 * 通用选择器（AI-CRM 视觉重做说明书 · 通用选择器板块的 TypeScript 实现）
 * 四个跨页面复用组件统一收口：
 *  - 选标签 tags     ↔ wecom_tag_picker：左组单选 / 右标签多选 / 搜索 / 已选 chips / 三态
 *  - 选渠道码 channel ↔ 商品·问卷内联 channel select：搜索 / 行单选 / 首项「不配置」
 *  - 选客服 members   ↔ operation_member_picker：部门单选 / 成员多选 / 已选比例 / 上限 5 人
 *  - 选素材/群聊 image|mp|attach|group ↔ material_picker / group_chat_picker：卡片多选 / 上限 9
 *
 * 自挂载 overlay（与 feedback.ts 同级，z-index 90），Promise 化：
 *   const r = await openPicker(api, { kind: 'image', max: 9 });
 *   r === null → 用户取消；否则为选中的 PickerItem[]（channel 单选返回 0 或 1 项）。
 */
import type { AdminApi } from '../api/client';
import type { AdminDb } from '../api/types';
import { toast } from './feedback';

export type PickerKind = 'tags' | 'channel' | 'members' | 'image' | 'mp' | 'attach' | 'group';

export interface PickerItem {
  id: string;
  name: string;
  url?: string;
  /** 副信息行（尺寸 / 编码·人数 / 剩余人数…） */
  sub?: string;
  /** 右侧 chip（类型） */
  chip?: string;
  /** 素材卡渐变背景 */
  bg?: string;
  uid?: string;
  dept?: string;
  /** 素材归类（image/mp/attach/group），调用方合并已选时用 */
  kind?: string;
}

export interface PickerOpts {
  kind: PickerKind;
  title?: string;
  subtitle?: string;
  /** channel 默认单选（点行即确定），其余默认多选 */
  multi?: boolean;
  /** 多选上限（members 5 / 素材·群 9）；0 = 不限 */
  max?: number;
  /** 预选 id 列表 */
  selected?: string[];
  /** channel 专用：首项「不配置引流渠道码」文案；传入后点该项返回 [] */
  noneOption?: string;
  /** 已按页面范围读取的真实数据；提供时不再触发无范围全量读取。 */
  db?: AdminDb;
}

const ACCENT = '#3370ff';
const TICK =
  "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'%3E%3Cpath d='M3.4 8.4l3 3 6.2-6.6' fill='none' stroke='%23fff' stroke-width='2.2' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E\")";

const KIND_TITLE: Record<PickerKind, string> = {
  tags: '选择标签',
  channel: '选择渠道码',
  members: '选择客服',
  image: '选择图片素材',
  mp: '选择小程序卡片',
  attach: '选择附件素材',
  group: '选择客户群',
};

interface SideGroup {
  id: string;
  name: string;
  count?: number;
}

export async function openPicker(api: AdminApi, opts: PickerOpts): Promise<PickerItem[] | null> {
  const db = opts.db || await api.loadDb();
  const kind = opts.kind;
  const multi = opts.multi ?? kind !== 'channel';
  const max = opts.max ?? (kind === 'members' ? 5 : kind === 'tags' || kind === 'channel' ? 0 : 9);

  /* ---------- 数据装配 ---------- */
  let items: PickerItem[] = [];
  let sideGroups: SideGroup[] = [];
  if (kind === 'tags') {
    sideGroups = db.tagGroups.map((g) => ({
      id: String(g.id),
      name: g.name,
      count: db.wecomTags.filter((t) => t.groupId === g.id).length,
    }));
    items = db.wecomTags.map((t) => ({ id: String(t.id), name: t.name, dept: String(t.groupId) }));
  } else if (kind === 'channel') {
    items = db.rows.channels
      .filter((c) => c.status === 'active' && typeof c.resourceId === 'number' && Number.isSafeInteger(c.resourceId) && c.resourceId > 0)
      .map((c) => ({ id: String(c.resourceId), name: c.name, sub: c.code + ' · ' + c.users + ' 人', chip: c.type }));
  } else if (kind === 'members') {
    const depts = Array.from(new Set(db.staff.map((s) => s.dept)));
    sideGroups = [{ id: '', name: '全部成员' }, ...depts.map((d) => ({ id: d, name: d }))];
    items = db.staff.map((s) => ({ id: s.uid, name: s.name, uid: s.uid, dept: s.dept }));
  } else if (kind === 'image') {
    items = db.rows.images
      .filter((m) => m.enabled && !!m.resourceId && /^[1-9]\d*$/.test(m.resourceId) && !!m.originalUrl)
      .map((m) => ({ id: String(m.resourceId), name: m.name, url: m.originalUrl || '', sub: '图片 · ' + m.size.split(' · ')[0], bg: m.bg }));
  } else if (kind === 'mp') {
    items = db.rows.mpItems
      .filter((m) => m.enabled)
      .map((m) => ({ id: String(m.resourceId), name: m.name, sub: '小程序 · ' + m.cardTitle, bg: m.bg }));
  } else if (kind === 'attach') {
    items = db.rows.attachItems
      .filter((a) => a.enabled)
      .map((a) => ({ id: a.resourceId || a.name, name: a.name, sub: a.type + ' · ' + a.size, chip: a.type }));
  } else {
    items = db.groupChats.map((g) => ({ id: g.name, name: g.name, sub: '客户群邀请 · 剩余 ' + g.left + ' 人', chip: g.size + ' 人群' }));
  }

  return new Promise<PickerItem[] | null>((resolve) => {
    /* ---------- 状态 ---------- */
    const selected: PickerItem[] = (opts.selected || [])
      .map((id) => items.find((i) => i.id === id))
      .filter((i): i is PickerItem => !!i);
    let curGroup = sideGroups.length ? sideGroups[0].id : null;
    let q = '';
    let done = false;

    /* ---------- 骨架 ---------- */
    document.querySelectorAll('.pk-mask').forEach((m) => m.remove());
    const mask = document.createElement('div');
    mask.className = 'pk-mask';
    mask.style.cssText =
      'position:fixed;inset:0;background:rgba(15,23,42,.34);z-index:90;display:flex;align-items:center;justify-content:center;padding:24px';
    const widthMap: Record<PickerKind, number> = { tags: 660, channel: 460, members: 780, image: 740, mp: 740, attach: 620, group: 560 };
    const card = document.createElement('div');
    card.style.cssText = `width:min(${widthMap[kind]}px,100%);max-height:86vh;background:#fff;border-radius:12px;box-shadow:0 24px 64px rgba(15,23,42,.22);overflow:hidden;display:flex;flex-direction:column`;
    mask.appendChild(card);
    document.body.appendChild(mask);

    const finish = (r: PickerItem[] | null): void => {
      if (done) return;
      done = true;
      document.removeEventListener('keydown', onKey);
      mask.remove();
      resolve(r);
    };
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') finish(null);
    };
    document.addEventListener('keydown', onKey);
    mask.addEventListener('click', (e) => {
      if (e.target === mask) finish(null);
    });

    /* ---------- 样式片段 ---------- */
    const cboxCss = (on: boolean): string =>
      `width:16px;height:16px;border-radius:4px;flex:none;border:1px solid ${on ? ACCENT : '#C4C7CC'};background:${
        on ? ACCENT + ' ' + TICK + ' center/12px 12px no-repeat' : '#fff'
      }`;
    const sideCss = (on: boolean): string =>
      `display:flex;align-items:center;justify-content:space-between;gap:8px;height:34px;padding:0 10px;border-radius:6px;font-size:13px;cursor:pointer;background:${
        on ? '#EFF4FF' : 'transparent'
      };color:${on ? ACCENT : '#1F2329'};font-weight:${on ? 600 : 400}`;
    const isSel = (id: string): boolean => selected.some((i) => i.id === id);

    /* ---------- 头部 ---------- */
    const showSync = kind === 'tags';
    card.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;padding:14px 18px;border-bottom:1px solid #EFF0F1;flex:none">
        <div><div style="font-size:15px;font-weight:600;color:#1F2329">${opts.title || KIND_TITLE[kind]}</div>
        ${opts.subtitle ? `<div style="font-size:12px;color:#8F959E;margin-top:2px">${opts.subtitle}</div>` : ''}</div>
        <div style="display:flex;align-items:center;gap:8px">
          ${showSync ? `<button data-pk="sync" style="height:30px;padding:0 12px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;font-size:12px;color:#646A73;cursor:pointer">${kind === 'tags' ? '同步企微标签' : '同步通讯录'}</button>` : ''}
          <button data-pk="x" style="width:28px;height:28px;border:0;border-radius:6px;background:#F2F3F5;color:#646A73;cursor:pointer;font-size:14px">×</button>
        </div>
      </div>
      <div style="padding:12px 18px;border-bottom:1px solid #EFF0F1;flex:none">
        <input data-pk="q" placeholder="${
          kind === 'tags' ? '搜索标签名' : kind === 'channel' ? '搜索渠道名称 / 编码' : kind === 'members' ? '搜索姓名 / userid' : '搜索名称'
        }" style="height:34px;width:100%;border:1px solid #DEE0E3;border-radius:6px;padding:0 10px;font-size:13px;background:#fff;box-sizing:border-box">
      </div>
      <div data-pk="body" style="flex:1;min-height:0;overflow:auto"></div>
      <div data-pk="foot" style="flex:none"></div>`;

    const body = card.querySelector('[data-pk="body"]') as HTMLElement;
    const foot = card.querySelector('[data-pk="foot"]') as HTMLElement;

    /* ---------- 选中操作 ---------- */
    const toggle = (it: PickerItem): void => {
      const at = selected.findIndex((i) => i.id === it.id);
      if (at >= 0) {
        selected.splice(at, 1);
      } else {
        if (!multi) {
          selected.splice(0, selected.length, it);
        } else {
          if (max > 0 && selected.length >= max) {
            toast('最多可选 ' + max + (kind === 'members' ? ' 人' : ' 个'), true);
            return;
          }
          selected.push(it);
        }
      }
      renderBody();
      renderFoot();
    };

    const visibleItems = (): PickerItem[] =>
      items.filter((i) => {
        if (curGroup !== null && kind === 'tags' && i.dept !== curGroup) return false;
        if (curGroup && kind === 'members' && i.dept !== curGroup) return false;
        if (!q) return true;
        return (i.name + ' ' + (i.sub || '') + ' ' + (i.uid || '')).toLowerCase().includes(q);
      });

    /* ---------- body 渲染 ---------- */
    function renderBody(): void {
      const list = visibleItems();
      const empty =
        '<div style="padding:36px 18px;text-align:center;font-size:13px;color:#A6AAB0">没有匹配的结果</div>';

      if (kind === 'channel') {
        const rows: string[] = [];
        if (opts.noneOption) {
          rows.push(
            `<div data-pk-none style="display:flex;align-items:center;gap:10px;padding:11px 14px;border-bottom:1px solid #F5F6F7;font-size:13px;color:#646A73;cursor:pointer">${opts.noneOption}</div>`,
          );
        }
        rows.push(
          ...list.map(
            (c) => `<div data-pk-id="${c.id}" style="display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 14px;border-bottom:1px solid #F5F6F7;cursor:pointer;background:${
              isSel(c.id) ? '#F5F9FF' : '#fff'
            }">
              <div style="min-width:0"><div style="font-size:13px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${c.name}</div>
              <div style="font-size:12px;color:#A6AAB0;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;margin-top:2px">${c.sub || ''}</div></div>
              <span style="display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:4px;background:#EFF4FF;color:#245BDB;font-size:12px;flex:none">${c.chip || ''}</span>
            </div>`,
          ),
        );
        body.innerHTML = rows.join('') || empty;
        body.querySelector('[data-pk-none]')?.addEventListener('click', () => finish([]));
        body.querySelectorAll('[data-pk-id]').forEach((el) =>
          el.addEventListener('click', () => {
            const it = items.find((i) => i.id === (el as HTMLElement).dataset.pkId);
            if (it) finish([it]);
          }),
        );
        return;
      }

      const listHtml = (arr: PickerItem[]): string => {
        if (kind === 'image' || kind === 'mp') {
          return `<div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;padding:14px 16px">${arr
            .map(
              (m) => `<div data-pk-id="${m.id}" style="border:1px solid ${isSel(m.id) ? ACCENT : '#DEE0E3'};border-radius:8px;overflow:hidden;cursor:pointer;background:${
                isSel(m.id) ? '#F5F9FF' : '#fff'
              };position:relative">
                <div style="height:76px;background:${m.bg || '#EFF0F1'}"></div>
                <div style="padding:8px 10px"><div style="font-size:12px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${m.name}</div>
                <div style="font-size:11px;color:#A6AAB0;margin-top:2px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${m.sub || ''}</div></div>
                <span style="position:absolute;top:6px;right:6px;${cboxCss(isSel(m.id))}"></span>
              </div>`,
            )
            .join('')}</div>`;
        }
        const twoCol = kind === 'tags' ? 'grid-template-columns:1fr 1fr;' : '';
        return `<div style="display:grid;${twoCol}gap:8px;padding:14px 16px">${arr
          .map((m) => {
            const on = isSel(m.id);
            const avatar =
              kind === 'members'
                ? `<span style="width:26px;height:26px;border-radius:50%;background:#F2F3F5;color:#646A73;display:flex;align-items:center;justify-content:center;font-size:12px;flex:none">${m.name.slice(0, 1)}</span>`
                : kind === 'group'
                  ? `<span style="width:26px;height:26px;border-radius:6px;background:#EBF9EC;color:#2EA121;display:flex;align-items:center;justify-content:center;font-size:12px;flex:none">群</span>`
                  : kind === 'attach'
                    ? `<span style="width:26px;height:26px;border-radius:6px;background:#FFF7ED;color:#C2410C;display:flex;align-items:center;justify-content:center;font-size:10px;font-weight:700;flex:none">${m.chip || 'FILE'}</span>`
                    : '';
            return `<label data-pk-id="${m.id}" style="display:flex;align-items:center;gap:9px;padding:9px 11px;border-radius:8px;cursor:pointer;border:1px solid ${
              on ? ACCENT : '#DEE0E3'
            };background:${on ? '#F5F9FF' : '#fff'}">
              <span style="${cboxCss(on)}"></span>${avatar}
              <span style="min-width:0;flex:1"><span style="display:block;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${m.name}</span>${
                m.sub || m.uid
                  ? `<span style="display:block;font-size:12px;color:#A6AAB0;font-family:ui-monospace,SFMono-Regular,Menlo,monospace">${m.uid || m.sub}</span>`
                  : ''
              }</span>${kind !== 'tags' && kind !== 'members' && m.chip ? `<span style="display:inline-flex;align-items:center;height:20px;padding:0 7px;border-radius:4px;background:#F2F3F5;color:#646A73;font-size:11px;flex:none">${m.chip}</span>` : ''}
            </label>`;
          })
          .join('')}</div>`;
      };

      if (sideGroups.length) {
        /* 双栏 / 三栏布局 */
        const sideCol = `<div style="border-right:1px solid #EFF0F1;padding:8px">${sideGroups
          .map(
            (g) =>
              `<div data-pk-g="${g.id}" style="${sideCss(g.id === curGroup)}"><span>${g.name}</span>${
                g.count != null ? `<span style="font-size:12px;color:${g.id === curGroup ? ACCENT : '#A6AAB0'}">${g.count}</span>` : ''
              }</div>`,
          )
          .join('')}</div>`;
        const mainCol = `<div style="min-width:0;overflow:auto">${list.length ? listHtml(list) : empty}</div>`;
        if (kind === 'members') {
          const selCol = `<div style="border-left:1px solid #EFF0F1;padding:12px 14px;overflow:auto">
            <div style="font-size:12px;color:#8F959E;margin-bottom:10px">已选 ${selected.length}${max ? ' / ' + max : ''}</div>
            <div style="display:grid;gap:8px">${selected
              .map(
                (s) => `<div style="display:flex;align-items:center;gap:8px;padding:8px 10px;border:1px solid #DEE0E3;border-radius:8px">
                  <span style="width:22px;height:22px;border-radius:50%;background:#EFF4FF;color:#245BDB;display:flex;align-items:center;justify-content:center;font-size:11px;flex:none">${s.name.slice(0, 1)}</span>
                  <span style="flex:1;font-size:13px;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${s.name}</span>
                  <span style="font-size:12px;color:#8F959E">${Math.round(100 / Math.max(selected.length, 1))}%</span>
                  <span data-pk-rm="${s.id}" style="font-size:12px;color:#A6AAB0;cursor:pointer">×</span>
                </div>`,
              )
              .join('')}</div>
            <div style="margin-top:10px;font-size:12px;color:#A6AAB0;line-height:19px">比例按均分自动计算，保存后可在渠道码内微调；合计需等于 100%。</div>
          </div>`;
          body.innerHTML = `<div style="display:grid;grid-template-columns:160px minmax(0,1fr) 220px;min-height:280px">${sideCol}${mainCol}${selCol}</div>`;
        } else {
          body.innerHTML = `<div style="display:grid;grid-template-columns:180px minmax(0,1fr);min-height:260px">${sideCol}${mainCol}</div>`;
        }
        body.querySelectorAll('[data-pk-g]').forEach((el) =>
          el.addEventListener('click', () => {
            curGroup = (el as HTMLElement).dataset.pkG || '';
            renderBody();
          }),
        );
      } else {
        body.innerHTML = list.length ? listHtml(list) : empty;
      }

      body.querySelectorAll('[data-pk-id]').forEach((el) =>
        el.addEventListener('click', (e) => {
          e.preventDefault();
          const it = items.find((i) => i.id === (el as HTMLElement).dataset.pkId);
          if (it) toggle(it);
        }),
      );
      body.querySelectorAll('[data-pk-rm]').forEach((el) =>
        el.addEventListener('click', (e) => {
          e.stopPropagation();
          const at = selected.findIndex((i) => i.id === (el as HTMLElement).dataset.pkRm);
          if (at >= 0) selected.splice(at, 1);
          renderBody();
          renderFoot();
        }),
      );
    }

    /* ---------- foot 渲染 ---------- */
    function renderFoot(): void {
      if (kind === 'channel') {
        foot.innerHTML = `<div style="padding:10px 14px;border-top:1px solid #EFF0F1;background:#FAFBFC;font-size:12px;color:#8F959E">共 ${items.length} 个渠道 · 已停用的不出现在列表里</div>`;
        return;
      }
      const chips = selected
        .map(
          (s) =>
            `<span style="display:inline-flex;align-items:center;gap:6px;height:24px;padding:0 8px;border-radius:4px;background:#EFF4FF;color:#245BDB;font-size:12px">${s.name}<span data-pk-rm="${s.id}" style="color:#A9BEF0;cursor:pointer">×</span></span>`,
        )
        .join('');
      const hint =
        kind === 'members' ? `最多可选 ${max} 人` : max ? `已选 ${selected.length} / ${max} 个` : `已选 ${selected.length} 个`;
      foot.innerHTML = `<div style="display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px 18px;border-top:1px solid #EFF0F1;background:#FAFBFC">
        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;min-width:0"><span style="font-size:12px;color:#8F959E;flex:none">${hint}</span>${chips}</div>
        <div style="display:flex;gap:8px;flex:none">
          <button data-pk="cancel" style="height:32px;padding:0 14px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;font-size:13px;cursor:pointer">取消</button>
          <button data-pk="ok" style="height:32px;padding:0 16px;border:0;border-radius:6px;background:${ACCENT};color:#fff;font-size:13px;cursor:pointer">确定</button>
        </div>
      </div>`;
      foot.querySelectorAll('[data-pk-rm]').forEach((el) =>
        el.addEventListener('click', () => {
          const at = selected.findIndex((i) => i.id === (el as HTMLElement).dataset.pkRm);
          if (at >= 0) selected.splice(at, 1);
          renderBody();
          renderFoot();
        }),
      );
      (foot.querySelector('[data-pk="cancel"]') as HTMLElement).addEventListener('click', () => finish(null));
      (foot.querySelector('[data-pk="ok"]') as HTMLElement).addEventListener('click', () => finish([...selected]));
    }

    /* ---------- 头部事件 ---------- */
    (card.querySelector('[data-pk="x"]') as HTMLElement).addEventListener('click', () => finish(null));
    card.querySelector('[data-pk="sync"]')?.addEventListener('click', () =>
      toast(kind === 'tags' ? '已排队刷新标签目录；未证明企微同步' : '已排队刷新通讯录目录；未证明企微同步'),
    );
    (card.querySelector('[data-pk="q"]') as HTMLInputElement).addEventListener('input', (e) => {
      q = (e.target as HTMLInputElement).value.trim().toLowerCase();
      renderBody();
    });

    renderBody();
    renderFoot();
  });
}
