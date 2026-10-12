// A thumbnail is presentation-only. Keep the original File for submission;
// neither this data URL nor the decoded image leaves the browser.
export const thumbnailPixels = 96;

async function readImage(file: File): Promise<HTMLImageElement> {
  const dataURL = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => typeof reader.result === 'string' ? resolve(reader.result) : reject(new Error('image read failed'));
    reader.onerror = () => reject(new Error('image read failed'));
    reader.readAsDataURL(file);
  });
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error('image decode failed'));
    image.src = dataURL;
  });
}

export async function localImageThumbnail(file: File): Promise<string> {
  const image = typeof createImageBitmap === 'function' ? await createImageBitmap(file) : await readImage(file);
  try {
    const width = 'naturalWidth' in image ? image.naturalWidth : image.width;
    const height = 'naturalHeight' in image ? image.naturalHeight : image.height;
    if (!width || !height) throw new Error('empty image');
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = thumbnailPixels;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('thumbnail unavailable');
    const crop = Math.min(width, height);
    context.fillStyle = '#fff';
    context.fillRect(0, 0, thumbnailPixels, thumbnailPixels);
    context.imageSmoothingQuality = 'low';
    context.drawImage(image, (width - crop) / 2, (height - crop) / 2, crop, crop, 0, 0, thumbnailPixels, thumbnailPixels);
    const result = canvas.toDataURL('image/jpeg', 0.5);
    if (!result.startsWith('data:image/jpeg;')) throw new Error('thumbnail encoding failed');
    return result;
  } finally {
    if ('close' in image) image.close();
    else image.src = '';
  }
}
