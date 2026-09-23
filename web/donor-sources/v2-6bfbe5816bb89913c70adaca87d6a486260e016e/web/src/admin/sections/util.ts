/** 富交互页（sections/*）共享小工具 */

/** HTML 转义 */
export function esc(v: unknown): string {
  return String(v ?? '').replace(
    /[&<>"']/g,
    (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string,
  );
}

/** 复制文本并 toast（降级为 prompt 展示） */
export function copyText(text: string, toast: (msg: string, err?: boolean) => void): void {
  const done = (): void => toast('已复制链接');
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done, () => window.prompt('请复制以下链接', text));
  } else {
    window.prompt('请复制以下链接', text);
  }
}

/** 伪二维码 SVG（种子稳定），渲染到 el 内 */
export function renderFakeQr(el: HTMLElement, seed: string): void {
  let h = 0;
  for (const c of seed) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  const n = 21;
  const sz = 180 / n;
  let cells = '';
  for (let y = 0; y < n; y++) {
    for (let x = 0; x < n; x++) {
      h = (h * 1103515245 + 12345) >>> 0;
      const finder = (x < 7 && y < 7) || (x > n - 8 && y < 7) || (x < 7 && y > n - 8);
      if (finder) {
        const inOuter =
          x === 0 || x === 6 || y === 0 || y === 6 ||
          (x >= n - 7 && (x === n - 7 || x === n - 1)) ||
          (y >= n - 7 && (y === n - 7 || y === n - 1));
        const inner =
          (x >= 2 && x <= 4 && y >= 2 && y <= 4) ||
          (x >= n - 5 && x <= n - 3 && y >= 2 && y <= 4) ||
          (x >= 2 && x <= 4 && y >= n - 5 && y <= n - 3);
        if (inOuter || inner) cells += `<rect x="${x * sz}" y="${y * sz}" width="${sz}" height="${sz}"/>`;
      } else if ((h >> 3) % 3 === 0) {
        cells += `<rect x="${x * sz}" y="${y * sz}" width="${sz}" height="${sz}"/>`;
      }
    }
  }
  el.innerHTML = `<svg viewBox="0 0 180 180" width="100%" height="100%" fill="#1F2329">${cells}</svg>`;
}
