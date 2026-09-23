import { useEffect, useRef, useState } from 'react';
import { connectEvents } from '../api/events';
import {
  controlIntruderJob, createIntruderJob, deleteIntruderJob, getIntruderJob, listIntruderJobs,
  listIntruderResults, updateIntruderJob,
} from '../api/intruder';
import type { AttackType, IntruderConfig, IntruderJob, IntruderJobSummary, IntruderPosition, IntruderResult } from '../api/intruder';

function encodeBytes(bytes: Uint8Array): string {
  let binary = '';
  for (let index = 0; index < bytes.length; index += 8192) binary += String.fromCharCode(...bytes.subarray(index, index + 8192));
  return btoa(binary);
}
const encodeText = (value: string) => encodeBytes(new TextEncoder().encode(value));
function decodeText(value: string): string {
  return new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(atob(value), (character) => character.charCodeAt(0)));
}

const canonical = (value: string) => value.replace(/\r?\n/g, '\r\n');
const defaultRaw = 'GET / HTTP/1.1\nHost: 127.0.0.1\n\n';
const emptyConfig: IntruderConfig = {
  attack: 'sniper', template: { method: 'GET', url: 'http://127.0.0.1/', raw: encodeText(canonical(defaultRaw)) },
  positions: [], payloadSets: [], requestLimit: 1000, concurrency: 2, ratePerSecond: 5, timeoutMs: 30000,
};

export function IntruderWorkspace({ leaveGuard }: { leaveGuard?: { current: () => boolean } }) {
  const [jobs, setJobs] = useState<IntruderJobSummary[]>([]);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [job, setJob] = useState<IntruderJob | null>(null);
  const [config, setConfig] = useState<IntruderConfig>(emptyConfig);
  const [raw, setRaw] = useState(defaultRaw);
  const [payloads, setPayloads] = useState<Record<string, string>>({});
  const [results, setResults] = useState<IntruderResult[]>([]);
  const [cursor, setCursor] = useState<number | null>(null);
  const [nextCursor, setNextCursor] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState('');
  const [refresh, setRefresh] = useState(0);
  const rawRef = useRef<HTMLTextAreaElement>(null);
  const editorLoadedID = useRef<string | null>(null);

  if (leaveGuard) leaveGuard.current = () => !dirty || window.confirm('Discard unsaved Intruder draft changes?');

  useEffect(() => connectEvents((event) => {
    if (event.type.startsWith('intruder.')) setRefresh((value) => value + 1);
  }), []);

  useEffect(() => {
    const controller = new AbortController();
    void listIntruderJobs(controller.signal).then((items) => setJobs(items)).catch((cause) => {
      if (!controller.signal.aborted) setError(String(cause));
    });
    return () => controller.abort();
  }, [refresh]);

  useEffect(() => {
    if (!selectedID) return;
    const controller = new AbortController();
    void getIntruderJob(selectedID, controller.signal).then((next) => {
      if (controller.signal.aborted) return;
      setJob(next);
      if (editorLoadedID.current !== selectedID) {
        editorLoadedID.current = selectedID;
        setDirty(false);
        setConfig(next.config);
        try { setRaw(decodeText(next.config.template.raw).replace(/\r\n/g, '\n')); } catch { setRaw(''); }
        const text: Record<string, string> = {};
        for (const set of next.config.payloadSets) {
          try { text[set.ID] = set.Payloads.map(decodeText).join('\n'); } catch { text[set.ID] = ''; }
        }
        setPayloads(text);
      }
    }).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    return () => controller.abort();
  }, [selectedID, refresh]);

  useEffect(() => {
    if (!selectedID) return;
    const controller = new AbortController();
    void listIntruderResults(selectedID, cursor ?? undefined, controller.signal).then((page) => {
      if (controller.signal.aborted) return;
      setResults(page.Results);
      setNextCursor(page.NextBeforeSequence);
    }).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    return () => controller.abort();
  }, [selectedID, cursor, refresh]);

  const run = async (task: () => Promise<IntruderJob | void>) => {
    setBusy(true);
    setError('');
    try {
      const next = await task();
      if (next) { setSelectedID(next.id); setJob(next); }
      setRefresh((value) => value + 1);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Request failed');
    } finally { setBusy(false); }
  };

  const addPosition = () => {
    const element = rawRef.current;
    if (!element || element.selectionStart === element.selectionEnd) { setError('Select request bytes first.'); return; }
    const start = new TextEncoder().encode(canonical(raw.slice(0, element.selectionStart))).length;
    const end = new TextEncoder().encode(canonical(raw.slice(0, element.selectionEnd))).length;
    if (config.positions.some((position) => start < position.End && end > position.Start)) { setError('Positions must not overlap.'); return; }
    const id = `p${config.positions.length + 1}`;
    const position: IntruderPosition = { ID: id, Start: start, End: end, PayloadSetID: id };
    setConfig((current) => ({ ...current, positions: [...current.positions, position] }));
    setDirty(true);
    setPayloads((current) => ({ ...current, [id]: '' }));
    setError('');
  };

  const currentConfig = (): IntruderConfig => ({
    ...config,
    template: { ...config.template, method: raw.match(/^[A-Z]+(?= )/)?.[0] ?? config.template.method, raw: encodeText(canonical(raw)) },
    payloadSets: config.positions.map((position) => ({
      ID: position.PayloadSetID,
      Payloads: (payloads[position.PayloadSetID] ?? '').split('\n').map(encodeText),
    })),
  });

  const save = () => run(async () => {
    const next = job?.state === 'draft' ? await updateIntruderJob(job, currentConfig()) : await createIntruderJob(currentConfig());
    setDirty(false);
    return next;
  });
  const control = (action: 'start' | 'pause' | 'resume' | 'abort') => {
    if (job) void run(() => controlIntruderJob(job, action));
  };

  return <section className="intruder-workspace" aria-label="Intruder workspace">
    <header className="intruder-header"><span className="eyebrow">Authorized targets only</span><h1>Intruder</h1><p>Payload-driven HTTP testing. Every generated request must remain in Target Scope.</p></header>
    <aside className="intruder-jobs"><div className="intruder-heading"><h2>Jobs</h2><button type="button" className="quiet-button" onClick={() => { editorLoadedID.current = null; setSelectedID(null); setJob(null); setDirty(false); setConfig(emptyConfig); setRaw(defaultRaw); setPayloads({}); setResults([]); }}>New</button></div>
      {jobs.length === 0 && <p>No jobs yet.</p>}
      {jobs.map((item) => <button key={item.ID} type="button" className={`intruder-job ${selectedID === item.ID ? 'selected' : ''}`} onClick={() => { setSelectedID(item.ID); setCursor(null); }}><strong>{item.Attack}</strong><span>{item.State} · {item.CompletedCount}/{item.TotalRequests}</span><small>{item.ID.slice(0, 12)}</small></button>)}
    </aside>
    <div className="intruder-editor"><div className="intruder-heading"><h2>{job ? `Job ${job.id.slice(0, 12)}` : 'New job'}</h2><span>{job?.state ?? 'draft'}</span></div>
      {error && <p className="api-error" role="alert">{error}</p>}
      <div className="intruder-fields">
        <label>Destination URL<input value={config.template.url} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setConfig({ ...config, template: { ...config.template, url: event.target.value } }); }} /></label>
        <label>Attack<select value={config.attack} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setConfig({ ...config, attack: event.target.value as AttackType }); }}><option value="sniper">Sniper</option><option value="battering_ram">Battering Ram</option><option value="pitchfork">Pitchfork</option><option value="cluster_bomb">Cluster Bomb</option></select></label>
        <label>Rate / second<input type="number" min="0.1" max="100" step="0.1" value={config.ratePerSecond} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setConfig({ ...config, ratePerSecond: Number(event.target.value) }); }} /></label>
        <label>Concurrency<input type="number" min="1" max="20" value={config.concurrency} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setConfig({ ...config, concurrency: Number(event.target.value) }); }} /></label>
        <label>Request limit<input type="number" min="1" max="100000" value={config.requestLimit} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setConfig({ ...config, requestLimit: Number(event.target.value) }); }} /></label>
        <label>Timeout (ms)<input type="number" min="1000" max="120000" value={config.timeoutMs} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setConfig({ ...config, timeoutMs: Number(event.target.value) }); }} /></label>
      </div>
      <label className="intruder-raw-label">Raw HTTP request<textarea ref={rawRef} spellCheck={false} value={raw} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setRaw(event.target.value); setConfig((current) => ({ ...current, positions: [] })); setPayloads({}); }} /></label>
      <button type="button" className="quiet-button" disabled={!!job && job.state !== 'draft'} onClick={addPosition}>Mark selected bytes as position</button>
      <div className="intruder-positions">{config.positions.map((position) => <div key={position.ID}><div className="intruder-heading"><strong>{position.ID} · bytes {position.Start}-{position.End}</strong><button type="button" className="quiet-button" disabled={!!job && job.state !== 'draft'} onClick={() => { setDirty(true); setConfig({ ...config, positions: config.positions.filter((candidate) => candidate.ID !== position.ID) }); }}>Remove</button></div><label>Payloads, one per line<textarea value={payloads[position.PayloadSetID] ?? ''} disabled={!!job && job.state !== 'draft'} onChange={(event) => { setDirty(true); setPayloads({ ...payloads, [position.PayloadSetID]: event.target.value }); }} /></label></div>)}</div>
      <div className="intruder-actions"><button type="button" className="quiet-button" disabled={busy || (!!job && job.state !== 'draft')} onClick={() => void save()}>{job?.state === 'draft' ? 'Save draft' : 'Create draft'}</button>
        {job?.state === 'draft' && <button type="button" className="quiet-button" disabled={busy || dirty} onClick={() => control('start')}>Start</button>}
        {job?.state === 'running' && <><button type="button" className="quiet-button" disabled={busy} onClick={() => control('pause')}>Pause</button><button type="button" className="quiet-button" disabled={busy} onClick={() => control('abort')}>Abort</button></>}
        {job?.state === 'paused' && <><button type="button" className="quiet-button" disabled={busy} onClick={() => control('resume')}>Resume</button><button type="button" className="quiet-button" disabled={busy} onClick={() => control('abort')}>Abort</button></>}
        {job && ['draft', 'aborted', 'completed', 'failed'].includes(job.state) && <button type="button" className="quiet-button" disabled={busy} onClick={() => void run(async () => { await deleteIntruderJob(job.id); setSelectedID(null); setJob(null); })}>Delete</button>}
      </div>
      {job && <p className="intruder-progress">{job.completedCount} / {job.totalRequests} requests · {job.errorCount} errors {job.stateReason && `· ${job.stateReason}`}</p>}
    </div>
    <section className="intruder-results"><div className="intruder-heading"><h2>Results</h2><span>100 per page</span></div><div className="intruder-result-list">{results.map((result) => <div className="intruder-result" key={result.Sequence}><strong>#{result.Sequence}</strong><span>{result.Status || result.ErrorCategory}</span><span>{result.ResponseSize} B</span><span>{result.MIMEType}</span><small>{result.URL}</small></div>)}</div>{selectedID && <div className="intruder-actions"><button type="button" className="quiet-button" disabled={cursor === null} onClick={() => setCursor(null)}>First page</button><button type="button" className="quiet-button" disabled={nextCursor === null} onClick={() => setCursor(nextCursor)}>Older</button></div>}</section>
  </section>;
}
