import { useEffect, useState } from 'react';
import type { Exchange } from '../types';

type Seed = Pick<Exchange, 'id' | 'method' | 'inScope' | 'error' | 'scheme' | 'host' | 'path'>;
interface Page { url: string; status: number; depth: number; contentType: string; truncated: boolean; error?: string }
interface Field { name: string; type: string }
interface Form { pageUrl: string; actionUrl: string; method: string; fields: Field[] }
interface Run { id: number; state: string; pageCount: number; reason?: string; pages?: Page[]; forms?: Form[] }

export function CrawlWorkspace({ exchange }: { exchange: Seed | null }) {
  const [maxPages, setMaxPages] = useState(10);
  const [maxDepth, setMaxDepth] = useState(2);
  const [runs, setRuns] = useState<Run[]>([]);
  const [selected, setSelected] = useState<Run | null>(null);
  const [error, setError] = useState('');
  const [starting, setStarting] = useState(false);
  const eligible = !!exchange && exchange.method === 'GET' && exchange.inScope && !exchange.error;

  const loadRuns = async (signal?: AbortSignal) => {
    const response = await fetch('/api/crawl/runs', { cache: 'no-store', signal });
    if (!response.ok) throw new Error(`Crawl history unavailable (${response.status})`);
    const items = await response.json() as Run[];
    if (!signal?.aborted) setRuns(items);
  };
  const openRun = async (id: number, signal?: AbortSignal) => {
    const response = await fetch(`/api/crawl/runs/${id}`, { cache: 'no-store', signal });
    if (!response.ok) throw new Error(`Crawl #${id} unavailable (${response.status})`);
    const item = await response.json() as Run;
    if (!signal?.aborted) setSelected(item);
  };
  useEffect(() => {
    const controller = new AbortController();
    void loadRuns(controller.signal).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    return () => controller.abort();
  }, []);
  useEffect(() => {
    if (!selected || selected.state !== 'running') return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void openRun(selected.id, controller.signal).then(() => loadRuns(controller.signal)).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    }, 750);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [selected]);
  const start = async () => {
    if (!eligible || !exchange || starting) return;
    if (!window.confirm(`Crawl up to ${maxPages} pages on ${exchange.scheme}://${exchange.host}? GET requests can have side effects. Only test systems you are authorized to assess.`)) return;
    setStarting(true); setError('');
    try {
      const response = await fetch('/api/crawl/runs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ historyId: exchange.id, maxPages, maxDepth, acknowledge: true }), cache: 'no-store' });
      if (!response.ok) throw new Error(`Crawl rejected (${response.status}): ${(await response.text()).trim()}`);
      const report = await response.json() as { runId: number };
      await openRun(report.runId);
      await loadRuns();
    } catch (cause) { setError(String(cause)); } finally { setStarting(false); }
  };
  const cancel = async () => {
    if (!selected) return;
    try {
      const response = await fetch(`/api/crawl/runs/${selected.id}/cancel`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}', cache: 'no-store' });
      if (!response.ok) throw new Error(`Cancel failed (${response.status})`);
      await openRun(selected.id);
    } catch (cause) { setError(String(cause)); }
  };
  const remove = async (id: number) => {
    if (!window.confirm(`Delete crawl #${id}? The source History entry will remain.`)) return;
    try {
      const response = await fetch(`/api/crawl/runs/${id}`, { method: 'DELETE', cache: 'no-store' });
      if (!response.ok) throw new Error(`Delete failed (${response.status})`);
      setRuns((items) => items.filter((item) => item.id !== id));
      setSelected((item) => item?.id === id ? null : item);
    } catch (cause) { setError(String(cause)); }
  };

  return <section className="scanner-history" aria-label="Crawler">
    <h2>Site crawler</h2><p>Discovers pages and form fields only. It does not submit forms or verify vulnerabilities.</p>
    <div className="scanner-controls"><label>Maximum pages<select value={maxPages} onChange={(event) => setMaxPages(Number(event.target.value))}>{[1, 5, 10, 25].map((value) => <option key={value}>{value}</option>)}</select></label><label>Maximum depth<select value={maxDepth} onChange={(event) => setMaxDepth(Number(event.target.value))}>{[0, 1, 2, 3].map((value) => <option key={value}>{value}</option>)}</select></label><button type="button" className="quiet-button" disabled={!eligible || starting} onClick={() => void start()}>{starting ? 'Starting...' : 'Start crawl'}</button>{selected?.state === 'running' && <button type="button" className="quiet-button" onClick={() => void cancel()}>Cancel crawl</button>}</div>
    {error && <p className="api-error" role="alert">{error}</p>}
    {selected && <div className="scanner-report"><h2>Crawl #{selected.id} · {selected.state} · {selected.pageCount} pages</h2>{selected.reason && <p>{selected.reason}</p>}{selected.pages?.map((page) => <article key={page.url}><strong>{page.url}</strong><span>{page.error || `HTTP ${page.status} · depth ${page.depth}${page.truncated ? ' · partial' : ''}`}</span></article>)}{selected.forms?.map((form, index) => <article key={`${form.pageUrl}-${index}`}><strong>{form.method} form → {form.actionUrl}</strong><span>{form.fields.map((field) => `${field.name} (${field.type})`).join(', ') || 'No named fields'}</span></article>)}</div>}
    <h3>Saved crawls</h3>{runs.map((run) => <div className="scanner-history-row" key={run.id}><button type="button" className="quiet-button" onClick={() => void openRun(run.id).catch((cause) => setError(String(cause)))}>Open crawl #{run.id}</button><span>{run.state} · {run.pageCount} pages</span><button type="button" className="quiet-button" disabled={run.state === 'running'} onClick={() => void remove(run.id)}>Delete crawl #{run.id}</button></div>)}
  </section>;
}
