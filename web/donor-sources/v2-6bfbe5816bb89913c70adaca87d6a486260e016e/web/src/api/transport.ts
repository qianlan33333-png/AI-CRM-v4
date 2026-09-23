/**
 * 生成客户端的统一浏览器 transport。
 *
 * Orval 的 fetch client 会保留每个 OpenAPI operation 的 URL、HTTP 方法和 DTO；
 * 本文件只补充同源会话、安全头和一致的失败语义，绝不拼接业务路由。
 */
export type ApiFailureKind = 'unauthenticated' | 'forbidden' | 'http' | 'network';

export class ApiError extends Error {
  constructor(
    readonly kind: ApiFailureKind,
    readonly status: number,
    readonly details: unknown,
  ) {
    super(
      kind === 'unauthenticated'
        ? '登录状态已失效，请重新登录'
        : kind === 'forbidden'
          ? '当前账号无权执行此操作'
          : `请求失败（HTTP ${status}）`,
    );
    this.name = 'ApiError';
  }
}

function csrfToken(cookie: string): string | undefined {
  const items = cookie.split(';').map((part) => part.trim());
  for (const name of ['aicrm_csrf', 'csrf_token']) {
    const prefix = `${name}=`;
    const item = items.find((part) => part.startsWith(prefix));
    if (item) return decodeURIComponent(item.slice(prefix.length));
  }
  return undefined;
}

/** 返回传给 Orval operation 的 RequestInit；所有页面共享同一个入口。 */
export function apiRequestOptions(init: RequestInit = {}, cookie = typeof document === 'undefined' ? '' : document.cookie): RequestInit {
  const headers = new Headers(init.headers);
  const token = csrfToken(cookie);
  if (token && !headers.has('X-CSRF-Token')) headers.set('X-CSRF-Token', token);
  // Orval 的 fetch 模板通过 `{ ...options?.headers }` 合并 headers；Headers
  // 实例没有可枚举字段，会导致 CSRF/幂等键静默丢失，因此必须返回普通记录。
  return { ...init, headers: Object.fromEntries(headers.entries()), credentials: 'include' };
}

/**
 * Orval 对非 2xx 响应返回带 status 的联合类型。所有 Adapter 必须先经这里
 * 解包，避免控制器把 401/403/409 当作成功结果渲染或 toast 成功。
 */
export function unwrapGenerated<T extends { status: number; data: unknown }>(response: T): T['data'] {
  if (response.status >= 200 && response.status < 300) return response.data;
  const kind: ApiFailureKind = response.status === 401 ? 'unauthenticated' : response.status === 403 ? 'forbidden' : 'http';
  throw new ApiError(kind, response.status, response.data);
}

/** 用于直接 fetch 的下载/上传辅助；统一处理同源 cookie、CSRF 和失败。 */
export async function request(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  try {
    const response = await fetch(input, apiRequestOptions(init));
    if (response.ok) return response;
    let details: unknown;
    try { details = await response.clone().json(); } catch { details = await response.text(); }
    const kind: ApiFailureKind = response.status === 401 ? 'unauthenticated' : response.status === 403 ? 'forbidden' : 'http';
    throw new ApiError(kind, response.status, details);
  } catch (error) {
    if (error instanceof ApiError) throw error;
    throw new ApiError('network', 0, error instanceof Error ? error.message : error);
  }
}
