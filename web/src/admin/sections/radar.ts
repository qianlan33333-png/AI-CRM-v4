/**
 * 内容雷达 —— 富交互模块（方向稿 radar.html 的 TypeScript 移植）
 * 视图：
 *  - radar.html       列表（搜索 / 类型 / 状态筛选 + 分享浮窗 + 启停）
 *  - radarDetail.html 详情（统计卡 + 访问明细筛选 + CSV 导出 + 复制链接）
 *  - radarForm.html   新建 / 编辑（类型卡片 / 开关 / 链接或素材分支 + 校验）
 * 数据经 AdminApi 读写（mock=sessionStorage 写穿，上线切 HttpApi）。
 */
import type { AdminApi } from '../../shared/api/client';
import type { RadarLink, RadarLinkInput, RadarMedia, RadarType } from '../../shared/api/types';
import { exportRadarEventsCsv, readRadarEvents } from '../../api/admin';
import { toast } from '../../shared/ui/feedback';
import { openPicker } from '../../shared/ui/picker';
import { downloadCsv } from '../../shared/ui/download';
import { esc, copyText } from './util';
import { downloadRadarQr, radarShareUrl, renderRadarQr } from './qr';

export interface RadarMountOpts {
  view: 'list' | 'detail' | 'form';
  /** detail / form(edit) 的链接 id */
  id?: number;
}

const TL: Record<RadarType, string> = { link: '链接', image: '图片', pdf: 'PDF' };
const PSL: Record<string, string> = { pending: '未处理', processing: '处理中', ready: '可预览', failed: '处理失败' };

const TYPE_CHIP: Record<RadarType, string> = { link: 'blue', image: 'ok', pdf: 'red' };

/* ================= 入口 ================= */
export async function mountRadar(root: HTMLElement, api: AdminApi, opts: RadarMountOpts): Promise<void> {
  const page = opts.view === 'list' ? 'radar' : opts.view === 'detail' ? 'radarDetail' : 'radarForm';
  const db = await api.loadDb({ page, id: opts.id == null ? undefined : String(opts.id) });
  const links = db.radarLinks;
  root.className = 'labs sec-radar';

  if (opts.view === 'list') renderList(root, api, links);
  else if (opts.view === 'detail') void renderDetail(root, api, links, opts.id);
  else renderForm(root, api, links, opts.id);
}

/* ================= 列表 ================= */
function renderList(root: HTMLElement, api: AdminApi, links: RadarLink[]): void {
  root.innerHTML = `
    <div class="crumb">客户管理后台 / 运营 / <b>内容雷达</b></div>
    <div class="page-head">
      <div><div class="page-title">内容雷达</div><div class="page-desc">给外部内容套上可追踪的中转链接，授权后记录谁看过 · 逻辑与生产环境一致</div></div>
      <button class="btn primary" id="btnNew">＋ 新建雷达链接</button>
    </div>
    <div class="card toolbar">
      <div class="filters">
        <div class="field"><label>搜索内容</label><input class="input" id="fKeyword" placeholder="按名称、链接、文件名搜索" style="width:220px"></div>
        <div class="field"><label>类型</label><select class="select" id="fType"><option value="all">全部</option><option value="link">链接</option><option value="image">图片</option><option value="pdf">PDF</option></select></div>
        <div class="field"><label>状态</label><select class="select" id="fStatus"><option value="all">全部</option><option value="enabled">启用</option><option value="disabled">停用</option></select></div>
      </div>
      <button class="btn" id="fRefresh">刷新</button>
    </div>
    <div class="card">
      <table class="tbl">
        <thead><tr><th>内容名称</th><th>类型</th><th>状态</th><th>PV</th><th>UV</th><th>查看</th><th>最近查看</th><th style="width:230px">操作</th></tr></thead>
        <tbody id="listRows"></tbody>
      </table>
    </div>
    <div class="mask" id="shareMask">
      <div class="modal">
        <div class="modal-head"><span>分享内容雷达</span><button class="modal-x" data-close>×</button></div>
        <div class="modal-body">
          <div><div style="font-size:12px;color:#646A73;margin-bottom:8px;font-weight:500">雷达链接</div>
            <div style="display:flex;gap:8px"><input class="input" id="shareUrl" readonly disabled style="flex:1"><button class="btn" id="shareCopy" disabled>复制链接</button></div></div>
          <div><div style="font-size:12px;color:#646A73;margin-bottom:8px;font-weight:500">二维码</div>
            <div class="qr" id="shareQr" role="status" style="display:grid;place-items:center;padding:16px;text-align:center;line-height:20px;color:#8F5A16">等待服务端分享投影</div>
            <button class="btn" id="shareQrDownload" disabled style="margin-top:10px;width:100%">下载二维码</button></div>
        </div>
      </div>
    </div>`;

  const $ = <T extends HTMLElement>(s: string): T => root.querySelector(s) as T;

  let shareLink = '';
  async function openShare(id: number): Promise<void> {
    const it = links.find((x) => x.id === id);
    if (!it) return;
    shareLink = '';
    ($('#shareUrl') as HTMLInputElement).value = '';
    ($('#shareUrl') as HTMLInputElement).disabled = true;
    ($('#shareCopy') as HTMLButtonElement).disabled = true;
    ($('#shareQrDownload') as HTMLButtonElement).disabled = true;
    $('#shareQr').textContent = '正在读取服务端分享投影…';
    $('#shareMask').classList.add('open');
    if (api.mode !== 'http') {
      $('#shareQr').innerHTML = '<strong>backend_blocked</strong>：测试/本地模式不使用 Mock 分享路径。';
      return;
    }
    try {
      const path = await api.getRadarSharePath(id);
      shareLink = radarShareUrl(path);
      ($('#shareUrl') as HTMLInputElement).value = shareLink;
      ($('#shareUrl') as HTMLInputElement).disabled = false;
      ($('#shareCopy') as HTMLButtonElement).disabled = false;
      renderRadarQr($('#shareQr'), shareLink);
      ($('#shareQrDownload') as HTMLButtonElement).disabled = false;
    } catch (error) {
      $('#shareQr').innerHTML = `<strong>backend_blocked</strong>：${esc(error instanceof Error ? error.message : '服务端分享投影不可用')}`;
      throw error;
    }
  }

  function paint(): void {
    const kw = ($('#fKeyword') as HTMLInputElement).value.trim().toLowerCase();
    const tp = ($('#fType') as HTMLSelectElement).value;
    const st = ($('#fStatus') as HTMLSelectElement).value;
    const list = links.filter((i) => {
      if (tp !== 'all' && i.target_type !== tp) return false;
      if (st === 'enabled' && !i.enabled) return false;
      if (st === 'disabled' && i.enabled) return false;
      if (!kw) return true;
      return (
        (i.title || '').toLowerCase().includes(kw) ||
        (i.original_url || '').toLowerCase().includes(kw) ||
        (i.file_name_snapshot || '').toLowerCase().includes(kw)
      );
    });
    $('#listRows').innerHTML = list.length
      ? list
          .map(
            (i) => `
      <tr class="${i.enabled ? '' : 'row-off'}">
        <td><div class="row-title">${esc(i.title)}</div>
            <div class="row-sub">${esc(i.file_name_snapshot || i.original_url || i.media_item_id || '-')}</div>
            ${i.target_type === 'pdf' ? `<div class="row-sub">PDF 状态：${PSL[i.pdf_processing_status || 'pending']}${i.pdf_page_count ? ' · ' + i.pdf_page_count + ' 页' : ''}</div>` : ''}</td>
        <td><span class="chip ${TYPE_CHIP[i.target_type]}">${TL[i.target_type]}</span></td>
        <td><span class="chip ${i.enabled ? 'ok' : 'gray'}">${i.enabled ? '启用' : '停用'}</span></td>
        <td class="num">${i.total_landings.toLocaleString()}</td>
        <td class="num">${i.authorized_users.toLocaleString()}</td>
        <td class="num">${i.view_count.toLocaleString()}</td>
        <td class="muted">${esc(i.last_viewed_at.slice(5))}</td>
        <td><div class="row-actions">
          <button class="link-btn" data-share="${i.id}">分享</button>
          <button class="link-btn" data-detail="${i.id}">详情</button>
          <button class="link-btn" data-edit="${i.id}">编辑</button>
          <button class="link-btn ${i.enabled ? 'red' : ''}" data-toggle="${i.id}">${i.enabled ? '停用' : '启用'}</button>
        </div></td>
      </tr>`,
          )
          .join('')
      : `<tr><td colspan="8" style="text-align:center;padding:40px;color:#8F959E">没有匹配的内容雷达</td></tr>`;
  }

  ['#fKeyword', '#fType', '#fStatus'].forEach((s) => $(s).addEventListener('input', paint));
  $('#fRefresh').addEventListener('click', () => {
    paint();
    toast('已刷新');
  });
  $('#btnNew').addEventListener('click', () => {
    location.href = 'radarForm.html';
  });
  $('#shareCopy').addEventListener('click', () => { if (shareLink) copyText(shareLink, toast); });
  $('#shareQrDownload').addEventListener('click', () => {
    if (!shareLink) return;
    try {
      downloadRadarQr(shareLink);
      toast('二维码已下载');
    } catch (error) {
      toast(error instanceof Error ? error.message : '二维码下载失败', true);
    }
  });
  root.querySelectorAll('[data-close]').forEach((b) =>
    b.addEventListener('click', () => (b as HTMLElement).closest('.mask')!.classList.remove('open')),
  );
  $('#shareMask').addEventListener('click', (e) => {
    if (e.target === $('#shareMask')) $('#shareMask').classList.remove('open');
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') $('#shareMask').classList.remove('open');
  });

  root.addEventListener('click', (e) => {
    const t = e.target as HTMLElement;
    const d = t.closest('[data-detail]') as HTMLElement | null;
    const s = t.closest('[data-share]') as HTMLElement | null;
    const ed = t.closest('[data-edit]') as HTMLElement | null;
    const tg = t.closest('[data-toggle]') as HTMLElement | null;
    if (d) location.href = 'radarDetail.html?id=' + d.dataset.detail;
    if (s) void openShare(Number(s.dataset.share)).catch((error) => toast(error instanceof Error ? error.message : '分享路径读取失败', true));
    if (ed) location.href = 'radarForm.html?id=' + ed.dataset.edit;
    if (tg) {
      const it = links.find((x) => x.id === Number(tg.dataset.toggle));
      if (it) {
        const next = !it.enabled;
        void api.toggleRadarLink(it.id, next).then(() => {
          it.enabled = next;
          paint();
          toast(next ? '已启用' : '已停用');
        });
      }
    }
  });

  paint();
}

/* ================= 详情 ================= */
async function renderDetail(root: HTMLElement, api: AdminApi, links: RadarLink[], id?: number): Promise<void> {
  const it = links.find((x) => x.id === id) || links[0];
  if (!it) {
    root.innerHTML = '<div class="card" style="padding:40px;text-align:center;color:#8F959E">雷达链接不存在</div>';
    return;
  }
  let events = api.mode === 'http' ? await readRadarEvents(it.id) : await api.listRadarEvents(it.id);
  let url = '';
  let shareError = '';
  if (api.mode === 'http') {
    try { url = radarShareUrl(await api.getRadarSharePath(it.id)); }
    catch (error) { shareError = error instanceof Error ? error.message : '分享路径读取失败'; }
  } else {
    shareError = '测试/本地模式不使用 Mock 分享路径';
  }
  const shareNotice = url
    ? `<span id="dUrl">${esc(url)}</span><button class="link-btn" id="dCopyInline">复制</button>`
    : `<span class="muted"><strong>backend_blocked</strong>：${esc(shareError)}</span><button class="link-btn" id="dCopyInline" disabled>复制</button>`;

  root.innerHTML = `
    <div class="crumb">客户管理后台 / 运营 / <a href="radar.html">内容雷达</a> / <b>${esc(it.title)}</b></div>
    <div class="page-head"><div class="page-title">雷达详情</div></div>

    <div class="card detail-hero">
      <div style="min-width:0">
        <div class="hero-name"><span>${esc(it.title)}</span><span class="chip ${TYPE_CHIP[it.target_type]}">${TL[it.target_type]}</span><span class="chip ${it.enabled ? 'ok' : 'gray'}">${it.enabled ? '启用' : '停用'}</span></div>
        <div class="hero-meta">
          <span>内容：<b style="font-weight:500;color:#1F2329">${esc(it.file_name_snapshot || it.original_url || it.media_item_id || '-')}</b></span>
          <span>需要授权：<b style="font-weight:500;color:#1F2329">${it.auth_required ? '是' : '否'}</b></span>
          <span>创建人：<b style="font-weight:500;color:#1F2329">${esc(it.staff_id || '-')}</b></span>
        </div>
        <div class="hero-url">${shareNotice}</div>
      </div>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <button class="btn" id="dBack">返回列表</button>
        <button class="btn" id="dCopy"${url ? '' : ' disabled'}>复制链接</button>
        <button class="btn" id="dExport">导出 CSV</button>
        <button class="btn primary" id="dEdit">编辑</button>
      </div>
    </div>

    <div class="stat-row">
      <div class="card stat"><div class="stat-l">PV · 中转页到达</div><div class="stat-v">${it.total_landings.toLocaleString()}</div><div class="stat-s">wrapper 页加载次数</div></div>
      <div class="card stat"><div class="stat-l">UV · 授权用户</div><div class="stat-v">${it.authorized_users.toLocaleString()}</div><div class="stat-s">完成微信授权的去重人数</div></div>
      <div class="card stat"><div class="stat-l">查看次数</div><div class="stat-v">${it.view_count.toLocaleString()}</div><div class="stat-s">授权后实际查看内容次数</div></div>
      <div class="card stat"><div class="stat-l">授权转化率</div><div class="stat-v">${it.total_landings ? Math.round((it.authorized_users / it.total_landings) * 100) + '%' : '0%'}</div><div class="stat-s">UV / PV</div></div>
    </div>

    <div class="card filter-bar">
      <div class="field" style="flex:1;min-width:200px"><label>搜索事件</label><input class="input" id="dKeyword" placeholder="回执 ID / 事件阶段"></div>
      <div class="field"><label>开始时间</label><input class="input" id="dStart" type="datetime-local"></div>
      <div class="field"><label>结束时间</label><input class="input" id="dEnd" type="datetime-local"></div>
      <button class="btn" id="dRefresh">刷新</button>
    </div>

    <div class="card">
      <table class="tbl">
        <thead><tr><th>回执 ID</th><th>事件阶段</th><th>发生时间</th></tr></thead>
        <tbody id="dRows"></tbody>
      </table>
    </div>`;

  const $ = <T extends HTMLElement>(s: string): T => root.querySelector(s) as T;

  function currentTimeFilters(): { startAt?: string; endAt?: string } {
    return {
      startAt: ($('#dStart') as HTMLInputElement).value || undefined,
      endAt: ($('#dEnd') as HTMLInputElement).value || undefined,
    };
  }

  function filteredEvents() {
    const kw = ($('#dKeyword') as HTMLInputElement).value.trim().toLowerCase();
    const start = ($('#dStart') as HTMLInputElement).value;
    const end = ($('#dEnd') as HTMLInputElement).value;
    return events.filter((e) => {
      if (kw && !(e.external_userid || '').toLowerCase().includes(kw) && !(e.unionid_masked || '').toLowerCase().includes(kw)) return false;
      const occurredAt = new Date(e.created_at).getTime();
      if (start && occurredAt < new Date(start).getTime()) return false;
      if (end && occurredAt > new Date(end).getTime()) return false;
      return true;
    });
  }

  function paintRows(): void {
    const list = filteredEvents();
    $('#dRows').innerHTML = list.length
      ? list.map((e) => `<tr><td class="mono">${esc(e.unionid_masked)}</td><td class="mono">${esc(e.external_userid)}</td><td>${esc(e.created_at)}</td></tr>`).join('')
      : `<tr><td colspan="3" style="text-align:center;padding:36px;color:#8F959E">暂无本地 Radar 事件</td></tr>`;
  }

  ['#dKeyword', '#dStart', '#dEnd'].forEach((s) => $(s).addEventListener('input', paintRows));
  $('#dRefresh').addEventListener('click', () => {
    const button = $('#dRefresh') as HTMLButtonElement;
    button.disabled = true;
    const next = api.mode === 'http' ? readRadarEvents(it.id, currentTimeFilters()) : api.listRadarEvents(it.id);
    void next.then((result) => {
      events = result;
      paintRows();
      toast('已按当前时间条件刷新');
    }).catch((error) => toast(error instanceof Error ? error.message : '雷达事件刷新失败', true)).finally(() => {
      button.disabled = false;
    });
  });
  $('#dBack').addEventListener('click', () => {
    location.href = 'radar.html';
  });
  $('#dEdit').addEventListener('click', () => {
    location.href = 'radarForm.html?id=' + it.id;
  });
  $('#dCopy').addEventListener('click', () => { if (url) copyText(url, toast); });
  $('#dCopyInline').addEventListener('click', () => { if (url) copyText(url, toast); });
  $('#dExport').addEventListener('click', () => {
    if (api.mode === 'http') {
      const button = $('#dExport') as HTMLButtonElement;
      button.disabled = true;
      void exportRadarEventsCsv(it.id, currentTimeFilters()).then((csv) => {
        const link = document.createElement('a');
        link.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }));
        link.download = 'radar-events.csv';
        link.click();
        setTimeout(() => URL.revokeObjectURL(link.href), 1000);
        toast('已导出 CSV');
      }).catch((error) => toast(error instanceof Error ? error.message : '雷达事件导出失败', true)).finally(() => {
        button.disabled = false;
      });
      return;
    }
    downloadCsv(
      'radar-events.csv',
      ['回执 ID', '事件阶段', '时间'],
      filteredEvents().map((e) => [e.unionid_masked, e.external_userid, e.created_at]),
    );
    toast('已导出 CSV');
  });

  paintRows();
}

/* ================= 新建 / 编辑 ================= */
function renderForm(root: HTMLElement, api: AdminApi, links: RadarLink[], id?: number): void {
  const editing = id !== undefined ? links.find((x) => x.id === id) || null : null;

  interface FormState {
    type: RadarType;
    enabled: boolean;
    auth: boolean;
    media: RadarMedia | null;
  }
  const form: FormState = {
    type: editing?.target_type || 'link',
    enabled: editing ? editing.enabled !== false : true,
    auth: editing ? editing.auth_required !== false : true,
    media:
      editing && editing.media_item_id
        ? {
            id: Number(editing.media_item_id),
            name: editing.file_name_snapshot || editing.media_item_id,
            meta: (editing.target_type === 'pdf' ? 'application/pdf' : 'image/png') + ' · 已存在素材',
          }
        : null,
  };

  root.innerHTML = `
    <div class="crumb">客户管理后台 / 运营 / <a href="radar.html">内容雷达</a> / <b>${editing ? '编辑' : '新建'}</b></div>
    <div class="page-head"><div class="page-title">${editing ? '编辑雷达链接' : '新建雷达链接'}</div></div>

    <div class="form-wrap">
      <div class="card">
        <div class="panel-head"><h2>基础信息</h2><p>保存后生成 /r/{code} 中转链接；图片和 PDF 的存储、扫描与访问状态以服务端回执为准。</p></div>
        <div class="panel-body">
          <div class="field"><label>内容名称 *</label><input class="input" id="fName" placeholder="例如：课程介绍 PDF" value="${esc(editing?.title || '')}"></div>
          <div class="field"><label>内容类型 *</label>
            <div class="type-cards" id="typeCards">
              <div class="type-card" data-t="link"><b>外部链接</b><span>跳转到任意 http/https 页面，到达即记录</span></div>
              <div class="type-card" data-t="image"><b>图片预览</b><span>由服务端访问状态控制，JPG / PNG / WEBP ≤ 10MB</span></div>
              <div class="type-card" data-t="pdf"><b>PDF 预览</b><span>由服务端访问状态控制，≤ 10MB，使用分片上传</span></div>
            </div>
          </div>
          <div class="grid-2">
            <div class="sw-row"><div><b>启用</b><span>停用的雷达链接访问时提示已失效</span></div><div class="sw" id="swEnabled"></div></div>
            <div class="sw-row"><div><b>需要授权</b><span>访问者需微信授权后才可查看，用于记录 UV</span></div><div class="sw" id="swAuth"></div></div>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="panel-head"><h2>内容配置</h2></div>
        <div class="panel-body">
          <div class="field" id="cfgUrl"><label id="fUrlLabel">HTTPS 目标地址 *</label><input class="input" id="fUrl" placeholder="https://example.com/page" value="${esc(editing?.original_url || '')}"><div class="help" id="fUrlHelp">OpenAPI 对所有 Radar 类型都要求独立 HTTPS destination_url。</div></div>
          <div class="field" id="cfgMedia" hidden><label id="fMediaLabel">素材 *</label>
            <div class="media-box">
              <div class="media-picked" id="mediaPicked" hidden>
                <div class="media-thumb" id="mediaThumb">📄</div>
                <div style="min-width:0;flex:1"><b id="mediaName" style="font-size:13px">—</b><div class="muted" id="mediaMeta" style="font-size:12px;margin-top:2px">—</div></div>
                <button class="link-btn red" id="mediaRemove">移除</button>
              </div>
              <div style="display:flex;gap:10px;flex-wrap:wrap">
                <button class="btn" id="btnPick">从素材库选择</button>
                <button class="btn" id="btnUpload">上传新素材</button>
                <input type="file" id="fileInput" hidden>
              </div>
              <div class="help" id="mediaHelp"></div>
            </div>
          </div>
        </div>
        <div class="form-actions">
          <button class="btn" id="fCancel">返回列表</button>
          <button class="btn primary" id="fSave">保存内容雷达</button>
        </div>
      </div>
    </div>`;

  const $ = <T extends HTMLElement>(s: string): T => root.querySelector(s) as T;

  function sync(): void {
    root.querySelectorAll('#typeCards .type-card').forEach((c) => {
      c.classList.toggle('on', (c as HTMLElement).dataset.t === form.type);
    });
    $('#swEnabled').classList.toggle('on', form.enabled);
    $('#swAuth').classList.toggle('on', form.auth);
    ($('#cfgUrl') as HTMLElement).hidden = false;
    $('#fUrlLabel').textContent = form.type === 'link' ? '外部链接 *' : '授权后的 HTTPS 目标地址 *';
    $('#fUrlHelp').textContent = form.type === 'link' ? '仅允许 HTTPS 外部链接。' : '素材 ID 与目标地址是两个独立契约字段，均须提供。';
    ($('#cfgMedia') as HTMLElement).hidden = form.type === 'link';
    $('#fMediaLabel').textContent = form.type === 'image' ? '图片素材 *' : 'PDF 素材 *';
    $('#mediaHelp').textContent =
      form.type === 'image'
        ? '可从图片素材库选择，或上传 JPG/PNG/WEBP，最大 10MB。'
        : '可从附件素材库选择 PDF，或上传 PDF，最大 10MB，使用分片上传。';
    ($('#fileInput') as HTMLInputElement).accept = form.type === 'image' ? 'image/jpeg,image/png,image/webp' : 'application/pdf';
    const m = form.media;
    ($('#mediaPicked') as HTMLElement).hidden = !m;
    if (m) {
      $('#mediaName').textContent = m.name;
      $('#mediaMeta').textContent = m.meta;
      $('#mediaThumb').textContent = form.type === 'image' ? '🖼️' : '📄';
    }
  }

  root.querySelectorAll('#typeCards .type-card').forEach((c) =>
    c.addEventListener('click', () => {
      form.type = (c as HTMLElement).dataset.t as RadarType;
      sync();
    }),
  );
  $('#swEnabled').addEventListener('click', () => {
    form.enabled = !form.enabled;
    sync();
  });
  $('#swAuth').addEventListener('click', () => {
    form.auth = !form.auth;
    sync();
  });
  $('#btnPick').addEventListener('click', () => {
    const isImg = form.type === 'image';
    void openPicker(api, { kind: isImg ? 'image' : 'attach', multi: false, max: 1, title: isImg ? '选择图片素材' : '选择 PDF 附件' }).then((r) => {
      if (!r || !r.length) return;
      const m = r[0];
      form.media = { id: Number(m.id), name: m.name, meta: (m.sub || '素材库') + ' · 来自素材库' };
      sync();
      toast('已选择素材');
    });
  });
  $('#btnUpload').addEventListener('click', () => {
    ($('#fileInput') as HTMLInputElement).click();
  });
  ($('#fileInput') as HTMLInputElement).addEventListener('change', (e) => {
    const input = e.target as HTMLInputElement;
    const file = input.files && input.files[0];
    if (!file) return;
    const up = form.type === 'image' ? api.uploadRadarImage(file) : api.uploadRadarPdf(file);
    void up.then((m) => {
      form.media = m;
      sync();
    }).catch((error) => toast(error instanceof Error ? error.message : '素材上传失败', true));
    input.value = '';
  });
  $('#mediaRemove').addEventListener('click', () => {
    form.media = null;
    sync();
  });
  $('#fCancel').addEventListener('click', () => {
    location.href = 'radar.html';
  });

  let saving = false;
  $('#fSave').addEventListener('click', () => {
    if (saving) return;
    const name = ($('#fName') as HTMLInputElement).value.trim();
    if (!name) return toast('请输入内容名称', true);
    const urlVal = ($('#fUrl') as HTMLInputElement).value.trim();
    if (!/^https:\/\//i.test(urlVal)) return toast('请输入合法 HTTPS 目标地址', true);
    if (form.type !== 'link' && !form.media) return toast('请选择或上传素材', true);
    if (form.type !== 'link' && (!Number.isInteger(form.media?.id) || Number(form.media?.id) < 1)) return toast('所选素材缺少服务端 ID，请重新选择或上传', true);

    const input: RadarLinkInput = {
      id: editing?.id,
      title: name,
      target_type: form.type,
      original_url: urlVal,
      file_name_snapshot: form.media?.name || editing?.file_name_snapshot || '',
      media_item_id: form.media?.id == null ? editing?.media_item_id || '' : String(form.media.id),
      enabled: form.enabled,
      auth_required: form.auth,
    };
    saving = true;
    const btn = $('#fSave') as HTMLButtonElement;
    btn.disabled = true;
    btn.textContent = '⏳ 保存中…';
    void api.saveRadarLink(input).then(() => {
      toast('已保存内容雷达');
      location.href = 'radar.html';
    }).catch((error) => {
      saving = false;
      btn.disabled = false;
      btn.textContent = '保存';
      toast(error instanceof Error ? error.message : '保存失败', true);
    });
  });

  sync();
}
