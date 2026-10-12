// Browser download events belong to the browser root CDP session. Keep this
// socket separate from the page session used for DOM, cookies and networking.
export async function openBrowserDownloadSession(origin, tracker, {
  fetchImpl = fetch, WebSocketImpl = WebSocket,
} = {}) {
  const response = await fetchImpl(origin + '/json/version');
  if (!response.ok) throw new Error('Browser debugging version endpoint is unavailable');
  const info = await response.json();
  const endpoint = new URL(info.webSocketDebuggerUrl);
  if (!endpoint.pathname.startsWith('/devtools/browser/')) throw new Error('Download events require a browser-target CDP endpoint');
  const socket = new WebSocketImpl(endpoint.href);
  const pending = new Map();
  let sequence = 0;
  let connectionState = 'connecting';
  socket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data));
    if (message.id) {
      const request = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) request?.reject(new Error(message.error.message));
      else request?.resolve(message.result);
    } else tracker.observe(message);
  });
  socket.addEventListener('close', () => {
    connectionState = 'closed';
    for (const request of pending.values()) request.reject(new Error('Browser download CDP session closed'));
    pending.clear();
  });
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params }));
  });
  try {
    await new Promise((resolve, reject) => {
      socket.addEventListener('open', () => { connectionState = 'open'; resolve(); }, { once: true });
      socket.addEventListener('error', () => reject(new Error('Browser download CDP connection failed')), { once: true });
      socket.addEventListener('close', () => reject(new Error('Browser download CDP closed before connection')), { once: true });
    });
    await tracker.configure(call);
    return { call, close: () => socket.close(), browserVersion: String(info.Browser || ''), targetKind: 'browser',
      get connectionState() { return connectionState; } };
  } catch (error) {
    socket.close();
    throw error;
  }
}
