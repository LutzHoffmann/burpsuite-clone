import { useEffect, useState } from 'react';

interface CrawlRun { id: number; state: string; pageCount: number }
interface Observation { url: string; source: string; parameter: string; status: number; found: boolean; context: string; partial: boolean; error?: string }
interface CheckRun { id: number; crawlId: number; state: string; reason?: string; observationCount: number; observations?: Observation[] }

export function ActiveChecksWorkspace() {
  const [crawls, setCrawls] = useState<CrawlRun[]>([]);
  const [crawlId, setCrawlId] = useState(0);
  const [runs, setRuns] = useState<CheckRun[]>([]);
  const [selected, setSelected] = useState<CheckRun | null>(null);
  const [starting, setStarting] = useState(false);
  const [cookie, setCookie] = useState('');
  const [authorization, setAuthorization] = useState('');
  const [error, setError] = useState('');
  const load = async (signal?: AbortSignal) => {
    const [crawlResponse, checkResponse] = await Promise.all([
      fetch('/api/crawl/runs', { cache: 'no-store', signal }),
      fetch('/api/active-checks/runs', { cache: 'no-store', signal }),
    ]);
    if (!crawlResponse.ok || !checkResponse.ok) throw new Error('Scan history unavailable');
    const crawlItems = await crawlResponse.json() as CrawlRun[];
    const checkItems = await checkResponse.json() as CheckRun[];
    if (!signal?.aborted) { setCrawls(crawlItems); setRuns(checkItems); }
  };
  const openRun = async (id: number, signal?: AbortSignal) => {
    const response = await fetch(`/api/active-checks/runs/${id}`, { cache: 'no-store', signal });
    if (!response.ok) throw new Error(`Active checks #${id} unavailable (${response.status})`);
    const run = await response.json() as CheckRun;
    if (!signal?.aborted) setSelected(run);
  };
  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    return () => controller.abort();
  }, []);
  useEffect(() => {
    if (selected?.state !== 'running') return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void openRun(selected.id, controller.signal).then(() => load(controller.signal)).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    }, 750);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [selected]);
  const start = async () => {
    if (!crawlId || starting) return;
    if (!window.confirm(`Run up to 100 synthetic GET checks from crawl #${crawlId}? ${cookie || authorization ? 'The session headers you entered will be sent to the crawl origin. ' : ''}Only test systems you are authorized to assess. A reflection is not confirmed XSS.`)) return;
    setStarting(true); setError('');
    try {
      const response = await fetch('/api/active-checks/runs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ crawlId, acknowledge: true, session: { cookie, authorization } }), cache: 'no-store' });
      if (!response.ok) throw new Error(`Checks rejected (${response.status}): ${(await response.text()).trim()}`);
      setCookie(''); setAuthorization('');
      const report = await response.json() as { runId: number };
      await openRun(report.runId);
      await load();
    } catch (cause) { setError(String(cause)); } finally { setStarting(false); }
  };
  const cancel = async () => {
    if (!selected) return;
    try {
      const response = await fetch(`/api/active-checks/runs/${selected.id}/cancel`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}', cache: 'no-store' });
      if (!response.ok) throw new Error(`Cancel failed (${response.status})`);
      await openRun(selected.id);
    } catch (cause) { setError(String(cause)); }
  };
  const remove = async (id: number) => {
    if (!window.confirm(`Delete active checks #${id}? Crawl and History remain.`)) return;
    try {
      const response = await fetch(`/api/active-checks/runs/${id}`, { method: 'DELETE', cache: 'no-store' });
      if (!response.ok) throw new Error(`Delete failed (${response.status})`);
      setRuns((items) => items.filter((item) => item.id !== id));
      setSelected((item) => item?.id === id ? null : item);
    } catch (cause) { setError(String(cause)); }
  };
  return <section className="scanner-history" aria-label="Active checks">
    <h2>Active checks</h2><p>Synthetic GET reflection checks on discovered inputs. Observations are not confirmed vulnerabilities.</p>
    <div className="scanner-controls"><label>Crawl run<select value={crawlId} onChange={(event) => setCrawlId(Number(event.target.value))}><option value={0}>Select completed crawl</option>{crawls.filter((run) => run.state === 'completed').map((run) => <option key={run.id} value={run.id}>Crawl #{run.id} · {run.pageCount} pages</option>)}</select></label><button type="button" className="quiet-button" onClick={() => void load().catch((cause) => setError(String(cause)))}>Refresh crawls</button><button type="button" className="quiet-button" disabled={!crawlId || starting} onClick={() => void start()}>{starting ? 'Starting...' : 'Start active checks'}</button>{selected?.state === 'running' && <button type="button" className="quiet-button" onClick={() => void cancel()}>Cancel active checks</button>}</div>
    <div className="scanner-controls"><label>Session cookie<input type="password" autoComplete="off" value={cookie} onChange={(event) => setCookie(event.target.value)} maxLength={4096} /></label><label>Authorization header<input type="password" autoComplete="off" value={authorization} onChange={(event) => setAuthorization(event.target.value)} maxLength={4096} /></label></div>
    <p className="scanner-notice">At most 100 requests, two per second, same origin and current scope. Original query values are not sent. Optional explicit session headers are used for this run only and never saved in results.</p>
    {error && <p className="api-error" role="alert">{error}</p>}
    {selected && <div className="scanner-report"><h2>Active checks #{selected.id} · Crawl #{selected.crawlId} · {selected.state} · {selected.observationCount} observations</h2>{selected.state !== 'running' && <p><a href={`/api/active-checks/runs/${selected.id}/report.html`} download={`active-checks-${selected.id}.html`}>Download HTML report</a></p>}{selected.reason && <p>{selected.reason}</p>}{selected.observations?.map((item) => <article key={`${item.source}-${item.url}-${item.parameter}`}><strong>{item.source}: {item.parameter} · {item.url}</strong><span>{item.error || `HTTP ${item.status} · ${item.found ? `${item.context} reflection observation, not confirmed XSS` : 'no exact reflection observed'}${item.partial ? ' · partial response' : ''}`}</span></article>)}</div>}
    <h3>Saved active checks</h3>{runs.map((run) => <div className="scanner-history-row" key={run.id}><button type="button" className="quiet-button" onClick={() => void openRun(run.id).catch((cause) => setError(String(cause)))}>Open active checks #{run.id}</button><span>{run.state} · {run.observationCount} observations</span><button type="button" className="quiet-button" disabled={run.state === 'running'} onClick={() => void remove(run.id)}>Delete active checks #{run.id}</button></div>)}
  </section>;
}
