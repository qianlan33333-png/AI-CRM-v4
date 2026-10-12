// Applies only to new manual uploads; existing catalogue records remain usable.
export const manualImageUploadMaxBytes = 1_048_576;
export const imageUploadSizeMessage = '单张图片不能超过 1MB，请压缩文件后重新选择';

export function imageUploadSizeError(file: Pick<File, 'name' | 'size'>): string | undefined {
  return file.size > manualImageUploadMaxBytes ? `图片“${file.name}”超过 1MB，未上传，请压缩文件后重新选择` : undefined;
}

export function requireManualImageSize(file: File): void {
  const error = imageUploadSizeError(file);
  if (error) throw new Error(error);
}
