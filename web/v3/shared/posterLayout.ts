import template from "../../../internal/referral/port/poster_template.json";

export type PosterQRLayout = { x: number; y: number; size: number };
export const referencePosterTemplate = template;

export function parsePosterQRLayout(value: unknown): PosterQRLayout | undefined {
  if (!value || typeof value !== "object") return undefined;
  const row = value as Record<string, unknown>;
  if (![row.x, row.y, row.size].every(Number.isSafeInteger)) return undefined;
  return { x: row.x as number, y: row.y as number, size: row.size as number };
}

export function defaultPosterQRLayout(width: number, height: number): PosterQRLayout {
  if (width === template.width && height === template.height) return { ...template.qr_layout };
  const size = Math.round(Math.min(width * .25, height * .19));
  return { x: width - size - Math.round(width * .06), y: height - size - Math.round(height * .06), size };
}

export function posterLayoutFits(layout: PosterQRLayout, width: number, height: number): boolean {
  return [layout.x, layout.y, layout.size].every(Number.isSafeInteger) && layout.x >= 0 && layout.y >= 0 && layout.size > 0 && layout.size <= width && layout.size <= height && layout.x <= width - layout.size && layout.y <= height - layout.size;
}
