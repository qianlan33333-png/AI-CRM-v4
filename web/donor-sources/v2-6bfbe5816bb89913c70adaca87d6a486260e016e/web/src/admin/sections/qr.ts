import qrcode from 'qrcode-generator';

const RADAR_SHARE_PATH = /^\/r\/rd_[A-Za-z0-9_-]{22}$/;

/** Convert the server-owned relative Radar path into a same-origin URL. */
export function radarShareUrl(path: string, origin = location.origin): string {
  if (!RADAR_SHARE_PATH.test(path)) throw new Error('Radar 分享路径不是当前站点公开路由契约');
  const url = new URL(path, origin);
  if (url.origin !== origin || url.pathname !== path || url.search || url.hash) throw new Error('Radar 分享 URL 必须是当前站点的公开路径');
  return url.toString();
}

/** Generate a real QR SVG from the absolute URL, not a visual placeholder. */
export function qrSvg(payload: string): string {
  const url = new URL(payload);
  if (!/^https?:$/.test(url.protocol)) throw new Error('二维码内容必须是绝对 HTTP(S) URL');
  const qr = qrcode(0, 'M');
  qr.addData(payload, 'Byte');
  qr.make();
  return qr.createSvgTag({ scalable: true, margin: 4 });
}

export function renderQr(el: HTMLElement, payload: string, label = '分享'): void {
  const svg = qrSvg(payload);
  el.innerHTML = svg;
  const node = el.querySelector('svg');
  if (!node) throw new Error('二维码 SVG 生成失败');
  node.setAttribute('width', '100%');
  node.setAttribute('height', '100%');
  node.setAttribute('role', 'img');
  node.setAttribute('aria-label', `${label}二维码：${payload}`);
  node.setAttribute('data-qr-payload', payload);
  el.dataset.qrPayload = payload;
}

export function downloadQr(payload: string, filename: string): void {
  const blob = new Blob([qrSvg(payload)], { type: 'image/svg+xml;charset=utf-8' });
  const objectUrl = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = objectUrl;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
}

export const radarQrSvg = qrSvg;
export function renderRadarQr(el: HTMLElement, payload: string): void { renderQr(el, payload, 'Radar 分享'); }
export function downloadRadarQr(payload: string, filename = 'radar-share-qr.svg'): void { downloadQr(payload, filename); }
