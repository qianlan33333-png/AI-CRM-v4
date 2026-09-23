const summaryEndpoint = '/api/public/member-grid-shares/summary';
const tokenPattern = /^mgshare1\.[A-Za-z0-9_-]{16,128}\.[A-Za-z0-9_-]{43}$/;
const states = ['active', 'expired', 'removed'] as const;
const sources = ['manual', 'paid_order'] as const;

type MemberState = typeof states[number];
type MemberSource = typeof sources[number];
type Bucket = { state: MemberState; count: number };
type Member = { displayName: string; state: MemberState; source: MemberSource; startsAt: string; expiresAt?: string; updatedAt: string };
type Summary = { buckets: Bucket[]; rows: Member[]; limit: 50; nextCursor: string; hasMore: boolean; asOf: string };

const labels: Record<MemberState, string> = {
  active: '有效',
  expired: '已过期',
  removed: '已移除',
};

function readSummary(value: unknown): Summary | undefined {
  if (value == null || typeof value !== 'object') return undefined;
  const source = value as Record<string, unknown>;
  const topKeys = new Set(['as_of', 'buckets', 'rows', 'limit', 'next_cursor', 'has_more']);
  if (Object.keys(source).some((key) => !topKeys.has(key))) return undefined;
  if (typeof source.as_of !== 'string' || source.as_of.trim() === '' || !Array.isArray(source.buckets) || !Array.isArray(source.rows) || source.rows.length > 50 ||
    source.limit !== 50 || typeof source.next_cursor !== 'string' || typeof source.has_more !== 'boolean' ||
    (source.has_more ? source.next_cursor === '' : source.next_cursor !== '')) return undefined;
  const byState = new Map<MemberState, number>();
  for (const item of source.buckets) {
    if (item == null || typeof item !== 'object') return undefined;
    const bucket = item as Record<string, unknown>;
    if (Object.keys(bucket).some((key) => key !== 'state' && key !== 'count')) return undefined;
    if (!states.includes(bucket.state as MemberState) || !Number.isSafeInteger(bucket.count) || (bucket.count as number) < 0 || byState.has(bucket.state as MemberState)) return undefined;
    byState.set(bucket.state as MemberState, bucket.count as number);
  }
  if (byState.size !== states.length) return undefined;
  const rows: Member[] = [];
  for (const item of source.rows) {
    if (item == null || typeof item !== 'object') return undefined;
    const row = item as Record<string, unknown>;
    const rowKeys = new Set(['display_name', 'state', 'source', 'starts_at', 'expires_at', 'updated_at']);
    if (Object.keys(row).some((key) => !rowKeys.has(key)) || typeof row.display_name !== 'string' || row.display_name.trim() === '' ||
      !states.includes(row.state as MemberState) || !sources.includes(row.source as MemberSource) ||
      typeof row.starts_at !== 'string' || row.starts_at === '' || !(row.expires_at == null || typeof row.expires_at === 'string' && row.expires_at !== '') ||
      typeof row.updated_at !== 'string' || row.updated_at === '') return undefined;
    rows.push({
      displayName: row.display_name,
      state: row.state as MemberState,
      source: row.source as MemberSource,
      startsAt: row.starts_at,
      expiresAt: typeof row.expires_at === 'string' ? row.expires_at : undefined,
      updatedAt: row.updated_at,
    });
  }
  return {
    asOf: source.as_of,
    buckets: states.map((state) => ({ state, count: byState.get(state) || 0 })),
    rows,
    limit: 50,
    nextCursor: source.next_cursor,
    hasMore: source.has_more,
  };
}

function renderFailure(stage: HTMLElement): void {
  const document = stage.ownerDocument;
  const panel = document.createElement('main');
  const title = document.createElement('h1');
  title.textContent = 'Member Grid 公开会员网格';
  const message = document.createElement('p');
  message.textContent = '暂时无法读取分享网格。';
  panel.append(title, message);
  stage.replaceChildren(panel);
}

function renderLoading(stage: HTMLElement): void {
  const document = stage.ownerDocument;
  const panel = document.createElement('main');
  const title = document.createElement('h1');
  title.textContent = 'Member Grid 公开会员网格';
  const message = document.createElement('p');
  message.textContent = '正在读取汇总…';
  panel.append(title, message);
  stage.replaceChildren(panel);
}

function renderSummary(stage: HTMLElement, summary: Summary, loadMore: () => void): void {
  const document = stage.ownerDocument;
  const panel = document.createElement('main');
  const title = document.createElement('h1');
  title.textContent = 'Member Grid 公开会员网格';
  const list = document.createElement('dl');
  for (const bucket of summary.buckets) {
    const state = document.createElement('dt');
    state.textContent = labels[bucket.state];
    const count = document.createElement('dd');
    count.textContent = String(bucket.count);
    list.append(state, count);
  }
  const asOf = document.createElement('p');
  asOf.textContent = `汇总截至：${summary.asOf}`;
  const table = document.createElement('table');
  const head = document.createElement('thead');
  const header = document.createElement('tr');
  for (const label of ['成员', '状态', '来源', '开始时间', '到期时间', '更新时间']) {
    const cell = document.createElement('th');
    cell.textContent = label;
    header.append(cell);
  }
  head.append(header);
  const body = document.createElement('tbody');
  for (const member of summary.rows) {
    const row = document.createElement('tr');
    for (const value of [member.displayName, labels[member.state], member.source === 'manual' ? '手工' : '付费订单', member.startsAt, member.expiresAt || '—', member.updatedAt]) {
      const cell = document.createElement('td');
      cell.textContent = value;
      row.append(cell);
    }
    body.append(row);
  }
  table.append(head, body);
  panel.append(title, list, asOf, table);
  if (summary.hasMore) {
    const more = document.createElement('button');
    more.type = 'button';
    more.textContent = '加载更多';
    more.addEventListener('click', loadMore);
    panel.append(more);
  }
  stage.replaceChildren(panel);
}

export async function mountMemberGridShare(stage: HTMLElement): Promise<void> {
  const token = window.location.hash.slice(1);
  window.history.replaceState(null, '', window.location.pathname + window.location.search);
  if (!tokenPattern.test(token)) {
    renderFailure(stage);
    return;
  }

  let current: Summary | undefined;
  const load = async (cursor = ''): Promise<void> => {
    if (!current) renderLoading(stage);
    try {
      const response = await fetch(summaryEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(cursor === '' ? { token } : { token, cursor }),
        credentials: 'omit',
        cache: 'no-store',
      });
      const page = response.ok ? readSummary(await response.json()) : undefined;
      if (!page) throw new Error('summary unavailable');
      current = current ? { ...page, rows: [...current.rows, ...page.rows] } : page;
      renderSummary(stage, current, () => { void load(current?.nextCursor || ''); });
    } catch {
      renderFailure(stage);
    }
  };
  await load();
}
