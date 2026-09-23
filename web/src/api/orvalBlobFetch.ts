export async function orvalBlobFetch<T>(url: string, options: RequestInit): Promise<T> {
  const response = await fetch(url, options);
  const contentType = response.headers.get('content-type') || '';
  const data = response.ok && contentType.startsWith('image/')
    ? await response.blob()
    : await response.json();
  return { data, status: response.status, headers: response.headers } as T;
}
