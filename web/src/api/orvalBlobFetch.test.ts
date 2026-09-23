import { orvalBlobFetch } from './orvalBlobFetch';

const assert = (value: unknown, message: string): void => {
  if (!value) throw new Error(message);
};

export async function runOrvalBlobFetchTests(): Promise<void> {
  const previousFetch = globalThis.fetch;
  const calls: Array<{ url: string; init: RequestInit }> = [];
  try {
    globalThis.fetch = async (request, init = {}) => {
      calls.push({ url: String(request), init });
      return new Response(new Uint8Array([0xff, 0xd8, 0xff, 0xd9]), { status: 200, headers: { 'Content-Type': 'image/jpeg' } });
    };
    const image = await orvalBlobFetch<{ data: Blob; status: number }>('/api/admin/channels/7/qrcode/download', { method: 'GET', credentials: 'include' });
    assert(image.status === 200 && image.data instanceof Blob && image.data.type === 'image/jpeg' && image.data.size === 4, 'JPEG success must remain a Blob');
    assert(calls[0].url.endsWith('/api/admin/channels/7/qrcode/download') && calls[0].init.credentials === 'include', 'request options must reach fetch');

    globalThis.fetch = async () => new Response(JSON.stringify({ message: 'not ready' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
    const error = await orvalBlobFetch<{ data: { message: string }; status: number }>('/api/admin/channels/7/qrcode/download', { method: 'GET' });
    assert(error.status === 503 && error.data.message === 'not ready', 'JSON failures must remain structured errors');
  } finally {
    globalThis.fetch = previousFetch;
  }
}
