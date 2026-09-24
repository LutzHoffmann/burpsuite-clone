import { useEffect, useRef, useState } from 'react';
import type { Exchange } from '../types';

interface Probe { parameter: string; status: number; reflected: boolean; partial: boolean; error?: string }
interface Report { historyId: number; probeCount: number; stoppedReason?: string; probes: Probe[] }

export function ActiveScannerWorkspace({ exchange }: { exchange: Exchange | null }) {
  const [maxProbes, setMaxProbes] = useState(2);
  const [busy, setBusy] = useState(false);
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  useEffect(() => {
    controller.current?.abort();
    controller.current = null;
    setReport(null); setError(''); setBusy(false);
    return () => controller.current?.abort();
  }, [exchange?.id]);

  const eligible = !!exchange && exchange.method === 'GET' && exchange.inScope && !exchange.error && !!exchange.query;
  const start = async () => {
    if (!eligible || busy || !exchange) return;
    const target = `${exchange.scheme}://${exchange.host}${exchange.path || '/'}`;
    if (!window.confirm(`Send up to ${maxProbes} unauthenticated GET probes to ${target}? GET endpoints can still have side effects. Only test systems you are authorized to assess.`)) return;
    const current = new AbortController();
    controller.current = current;
    setBusy(true); setReport(null); setError('');
    try {
      const response = await fetch('/api/active-scan', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ historyId: exchange.id, maxProbes, acknowledge: true }), signal: current.signal, cache: 'no-store' });
      if (!response.ok) throw new Error(`Scan rejected (${response.status}): ${(await response.text()).trim()}`);
      const next = await response.json() as Report;
      if (!current.signal.aborted) setReport(next);
    } catch (cause) {
      if (!current.signal.aborted) setError(cause instanceof Error ? cause.message : 'Scan failed');
    } finally { if (!current.signal.aborted) setBusy(false); }
  };

  return <section className="active-scanner-workspace" aria-label="Active scanner">
    <header><span className="eyebrow">Explicit traffic only</span><h1>Active scanner</h1><p>Limited query-reflection probes. A reflected marker is an observation, not proof of XSS or another vulnerability.</p></header>
    {!exchange && <p className="scanner-notice">Select a request in History before opening this tool.</p>}
    {exchange && <div className="scanner-target"><strong>{exchange.method} {exchange.scheme}://{exchange.host}{exchange.path}</strong><span>History #{exchange.id} · {exchange.inScope ? 'captured in scope' : 'captured outside scope'}</span></div>}
    {exchange && !eligible && <p className="scanner-notice">Only in-scope, successful GET requests with query parameters can be scanned.</p>}
    <div className="scanner-controls"><label>Maximum probes<select value={maxProbes} disabled={busy} onChange={(event) => setMaxProbes(Number(event.target.value))}>{[1, 2, 3, 4, 5].map((value) => <option key={value} value={value}>{value}</option>)}</select></label><button type="button" className="quiet-button" disabled={!eligible || busy} onClick={() => void start()}>{busy ? 'Scanning...' : 'Start scan'}</button></div>
    <p className="scanner-notice">Each probe sends one new marker for one query-parameter name. Original query values, cookies, authorization headers, and request bodies are not forwarded. The server checks current scope before every send, limits the rate to two requests per second, and does not follow redirects.</p>
    {error && <p className="api-error" role="alert">{error}</p>}
    {report && <div className="scanner-report"><h2>{report.probeCount} {report.probeCount === 1 ? 'probe' : 'probes'} completed</h2>{report.stoppedReason && <p role="status">Stopped: {report.stoppedReason}</p>}{report.probes.map((probe) => <article key={probe.parameter}><strong>{probe.parameter}</strong><span>{probe.error ? 'Request failed' : `HTTP ${probe.status} · ${probe.reflected ? 'marker reflected' : 'no reflection observed'}${probe.partial ? ' · partial response capture' : ''}`}</span></article>)}</div>}
  </section>;
}
