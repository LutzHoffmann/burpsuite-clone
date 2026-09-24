import { useEffect, useState } from 'react';
import { connectEvents } from '../api/events';

interface FindingSummary {
  type: string;
  host: string;
  subject: string;
  count: number;
  latestExchangeId: number;
  latestPath: string;
}

interface FindingsPage {
  items: FindingSummary[];
  nextOffset: number;
  snapshotId: number;
}

const titles: Record<string, string> = {
  hsts_missing: 'HSTS header not observed',
  csp_missing: 'CSP header not observed',
  cookie_secure_missing: 'Cookie without Secure attribute',
};

export function FindingsWorkspace({ onOpenHistory }: { onOpenHistory: (id: number) => void }) {
  const [hostInput, setHostInput] = useState('');
  const [typeInput, setTypeInput] = useState('');
  const [host, setHost] = useState('');
  const [type, setType] = useState('');
  const [offset, setOffset] = useState(0);
  const [snapshot, setSnapshot] = useState(0);
  const [page, setPage] = useState<FindingsPage | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const disconnect = connectEvents((event) => {
      if (event.type !== 'history.entry.created') return;
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => { setOffset(0); setSnapshot(0); setRefresh((value) => value + 1); }, 500);
    });
    return () => { if (timer) clearTimeout(timer); disconnect(); };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const query = new URLSearchParams();
    if (host) query.set('host', host);
    if (type) query.set('type', type);
    if (offset) { query.set('offset', String(offset)); query.set('snapshotId', String(snapshot)); }
    setLoading(true);
    setError('');
    void fetch(`/api/findings?${query}`, { signal: controller.signal, cache: 'no-store' }).then(async (response) => {
      if (!response.ok) throw new Error(`Findings unavailable (${response.status})`);
      return response.json() as Promise<FindingsPage>;
    }).then((next) => { if (!controller.signal.aborted) { setPage(next); setSnapshot(next.snapshotId); } })
      .catch((cause) => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Findings unavailable'); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [host, type, offset, refresh]);

  const apply = () => { setOffset(0); setSnapshot(0); setHost(hostInput.trim()); setType(typeInput); setRefresh((value) => value + 1); };
  return <section className="findings-workspace" aria-label="Passive findings">
    <header className="findings-header"><span className="eyebrow">Captured traffic only</span><h1>Passive findings</h1><p>Grouped observations from in-scope History responses. No requests are sent. These are not confirmed vulnerabilities.</p></header>
    <form className="findings-controls" onSubmit={(event) => { event.preventDefault(); apply(); }}>
      <label>Exact host<input value={hostInput} onChange={(event) => setHostInput(event.target.value)} placeholder="All hosts" /></label>
      <label>Finding type<select value={typeInput} onChange={(event) => setTypeInput(event.target.value)}><option value="">All types</option><option value="hsts_missing">HSTS not observed</option><option value="csp_missing">CSP not observed</option><option value="cookie_secure_missing">Cookie without Secure</option></select></label>
      <button type="submit" className="quiet-button">Apply filters</button>
      <button type="button" className="quiet-button" onClick={() => { setOffset(0); setSnapshot(0); setRefresh((value) => value + 1); }}>Refresh</button>
    </form>
    {error && <p className="api-error" role="alert">{error}</p>}
    {loading && <p className="findings-message">Loading observations...</p>}
    {!loading && page && page.items.length === 0 && <p className="findings-message">No observations for these filters.</p>}
    {!loading && page && <div className="findings-list">{page.items.map((item) => <article key={`${item.host}:${item.type}:${item.subject}`} className="finding-card">
      <div><span className="eyebrow">{item.host}</span><h2>{titles[item.type] ?? item.type}</h2><p>{item.subject ? `Cookie: ${item.subject} · ` : ''}{item.count} captured {item.count === 1 ? 'response' : 'responses'} · latest path {item.latestPath || '/'}</p></div>
      <button type="button" className="quiet-button" onClick={() => onOpenHistory(item.latestExchangeId)}>Open History #{item.latestExchangeId}</button>
    </article>)}</div>}
    <div className="findings-pagination"><button type="button" className="quiet-button" disabled={loading || offset === 0} onClick={() => setOffset(Math.max(0, offset - 100))}>Previous</button><span>Groups {page && page.items.length ? offset + 1 : 0}-{offset + (page?.items.length ?? 0)}</span><button type="button" className="quiet-button" disabled={loading || !page?.nextOffset} onClick={() => setOffset(page?.nextOffset ?? 0)}>Next</button></div>
  </section>;
}
