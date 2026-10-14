import qrcode from "qrcode-generator";
import { defaultPosterQRLayout, posterLayoutFits, type PosterQRLayout } from "./posterLayout";

const outputTypes = new WeakMap<HTMLCanvasElement, string>();

function checkCancelled(signal?: AbortSignal): void {
  if (signal?.aborted) throw new DOMException("海报生成已取消", "AbortError");
}

function readPosterBlob(blob: Blob, signal?: AbortSignal): Promise<string> {
  checkCancelled(signal);
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    const cleanup = () => signal?.removeEventListener("abort", abort);
    const abort = () => { cleanup(); reader.abort(); reject(new DOMException("海报生成已取消", "AbortError")); };
    reader.onload = () => { cleanup(); resolve(String(reader.result)); };
    reader.onerror = () => { cleanup(); reject(new Error("海报图片读取失败，请稍后重试。")); };
    signal?.addEventListener("abort", abort, { once: true });
    reader.readAsDataURL(blob);
  });
}

type GraphemeSegmenter = { segment(value: string): Iterable<{ segment: string }> };

function drawPosterNickname(ctx: CanvasRenderingContext2D, value: string, width: number): void {
  const nickname = value.replace(/\s+/gu, " ").trim();
  if (!nickname) return;
  const scale = width / 1250;
  ctx.save();
  try {
    ctx.font = `400 ${30 * scale}px system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif`;
    ctx.textAlign = "right";
    ctx.textBaseline = "top";
    const maxWidth = width * .24;
    let text = nickname;
    if (ctx.measureText(text).width > maxWidth) {
      const Segmenter = (Intl as unknown as { Segmenter?: new (locale: string, options: { granularity: string }) => GraphemeSegmenter }).Segmenter;
      // Older browsers may show short names; never split an emoji sequence
      // just to fit a long name when grapheme segmentation is unavailable.
      if (!Segmenter) return;
      const segments = Array.from(new Segmenter("zh-CN", { granularity: "grapheme" }).segment(text), item => item.segment);
      while (segments.length && ctx.measureText(segments.join("") + "…").width > maxWidth) segments.pop();
      text = segments.join("") + "…";
    }
    ctx.fillStyle = "#F7F0DF";
    ctx.shadowColor = "rgba(114, 84, 60, .35)";
    ctx.shadowBlur = 2 * scale;
    ctx.shadowOffsetY = scale;
    ctx.fillText(text, width - 48 * scale, 48 * scale);
  } finally {
    ctx.restore();
  }
}

export async function composeReferralPoster(imageURL: string, payload: string, layout?: PosterQRLayout, expectedWidth = 0, expectedHeight = 0, signal?: AbortSignal, nickname = ""): Promise<HTMLCanvasElement> {
  let response: Response;
  try { response = await fetch(imageURL, { credentials: "same-origin", signal }); }
  catch { checkCancelled(signal); throw new Error("海报加载失败，请稍后重试。"); }
  if (!response.ok) {
    if (response.status === 503) throw new Error("海报生成繁忙，请稍后重新打开。");
    if (response.status === 409) throw new Error("海报图片刚刚更新，请重新打开海报。");
    throw new Error("海报加载失败，请稍后重试。");
  }
  let blob: Blob;
  try { blob = await response.blob(); }
  catch { checkCancelled(signal); throw new Error("海报加载失败，请稍后重试。"); }
  const source = new Image();
  source.decoding = "async";
  source.src = await readPosterBlob(blob, signal);
  try { await source.decode(); } catch { throw new Error("海报加载失败，请稍后重试。"); }
  // A late decode must never allocate or draw for a closed/changed selection.
  checkCancelled(signal);
  const canvas = document.createElement("canvas");
  canvas.width = source.naturalWidth;
  canvas.height = source.naturalHeight;
  const ctx = canvas.getContext("2d");
  if (!ctx || canvas.width < 600 || canvas.height < 800 || (expectedWidth > 0 && expectedWidth !== canvas.width) || (expectedHeight > 0 && expectedHeight !== canvas.height)) throw new Error("海报图片刚刚更新，请重新打开海报。");
  const box = layout || defaultPosterQRLayout(canvas.width, canvas.height);
  if (!posterLayoutFits(box, canvas.width, canvas.height)) throw new Error("二维码位置不可用，请联系管理员调整。");
  const qr = qrcode(0, "M");
  qr.addData(payload, "Byte");
  qr.make();
  const modules = qr.getModuleCount();
  // Reserve four modules, then distribute integer pixel boundaries across the
  // full symbol. Flooring every cell separately used to waste white space.
  const cell = box.size / (modules + 8);
  if (cell < 1) throw new Error("二维码区域过小，请联系管理员调整。");
  const quiet = Math.ceil(4 * cell);
  const symbol = box.size - 2 * quiet;
  if (symbol < modules) throw new Error("二维码区域过小，请联系管理员调整。");
  const edge = (index: number) => quiet + Math.round(index * symbol / modules);
  ctx.drawImage(source, 0, 0);
  ctx.fillStyle = "#fff";
  ctx.fillRect(box.x, box.y, box.size, box.size);
  ctx.fillStyle = "#1b1b1b";
  for (let row = 0; row < modules; row++) {
    for (let col = 0; col < modules; col++) {
      if (qr.isDark(row, col)) ctx.fillRect(box.x + edge(col), box.y + edge(row), edge(col + 1) - edge(col), edge(row + 1) - edge(row));
    }
  }
  drawPosterNickname(ctx, nickname, canvas.width);
  outputTypes.set(canvas, blob.type.split(";", 1)[0].trim() === "image/jpeg" ? "image/jpeg" : "image/png");
  return canvas;
}

export async function exportReferralPoster(canvas: HTMLCanvasElement, signal?: AbortSignal): Promise<string> {
  try {
    checkCancelled(signal);
    const type = outputTypes.get(canvas) || "image/jpeg";
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((result) => result ? resolve(result) : reject(new Error("海报生成失败，请稍后重新打开。")), type, 0.95);
    });
    return await readPosterBlob(blob, signal);
  } finally {
    // The displayed image owns the encoded bytes; release the pixel buffer.
    canvas.width = 0;
    canvas.height = 0;
  }
}
