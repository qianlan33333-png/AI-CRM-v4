import qrcode from "qrcode-generator";

// Browser-local PNG export; neither URL generation nor domain reads belong here.
export async function downloadQrPng(
  payload: string,
  filename: string,
): Promise<void> {
  const qr = qrcode(0, "M");
  qr.addData(payload, "Byte");
  qr.make();
  const modules = qr.getModuleCount();
  const scale = Math.max(1, Math.floor(1024 / (modules + 8)));
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = (modules + 8) * scale;
  const context = canvas.getContext("2d");
  if (!context) throw new Error("二维码导出失败，请重试。");
  context.fillStyle = "#fff";
  context.fillRect(0, 0, canvas.width, canvas.height);
  context.fillStyle = "#000";
  for (let row = 0; row < modules; row++) {
    for (let column = 0; column < modules; column++) {
      if (qr.isDark(row, column))
        context.fillRect((column + 4) * scale, (row + 4) * scale, scale, scale);
    }
  }
  const blob = await new Promise<Blob>((resolve, reject) =>
    canvas.toBlob(
      (value) =>
        value ? resolve(value) : reject(new Error("二维码导出失败，请重试。")),
      "image/png",
    ),
  );
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
