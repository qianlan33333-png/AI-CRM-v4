import qrcode from "qrcode-generator";
import { defaultPosterQRLayout, posterLayoutFits, type PosterQRLayout } from "./posterLayout";

export async function composeReferralPoster(imageURL: string, payload: string, layout?: PosterQRLayout, expectedWidth = 0, expectedHeight = 0): Promise<HTMLCanvasElement> {
  const source = new Image();
  source.decoding = "async";
  source.src = imageURL;
  try { await source.decode(); } catch { throw new Error("海报加载失败，请稍后重试。"); }
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
  // Four blank modules on each side; integer cells avoid fractional blur.
  const cell = Math.floor(box.size / (modules + 8));
  if (cell < 1) throw new Error("二维码区域过小，请联系管理员调整。");
  const quiet = Math.floor((box.size - modules * cell) / 2);
  ctx.drawImage(source, 0, 0);
  ctx.fillStyle = "#fff";
  ctx.fillRect(box.x, box.y, box.size, box.size);
  ctx.fillStyle = "#1b1b1b";
  for (let row = 0; row < modules; row++) {
    for (let col = 0; col < modules; col++) {
      if (qr.isDark(row, col)) ctx.fillRect(box.x + quiet + col * cell, box.y + quiet + row * cell, cell, cell);
    }
  }
  return canvas;
}
