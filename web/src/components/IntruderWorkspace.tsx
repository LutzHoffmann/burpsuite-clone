import { useEffect, useRef, useState } from 'react';
import { connectEvents } from '../api/events';
import type { IntruderSource } from '../api/intruderDraft';
import { byteOffsetInRaw, canonicalEditedRequest, displayRawRequest, displayRawHex, hexSelectionOffset, parseRawHex } from '../api/intruderBytes';
import { convertPayloadEditor, displayPayloads, encodePayloadEditor } from '../api/intruderPayloads';
import type { PayloadEditor } from '../api/intruderPayloads';
import {
  controlIntruderJob, createIntruderJob, deleteIntruderJob, getIntruderJob, getIntruderResult, listIntruderJobs,
  listIntruderResults, previewIntruder, setIntruderBaseline, updateIntruderJob,
} from '../api/intruder';
import type { AttackType, IntruderConfig, IntruderJob, IntruderJobSummary, IntruderPosition, IntruderPreview, IntruderResult, IntruderResultFilters } from '../api/intruder';

function encodeBytes(bytes: Uint8Array): string {
  let binary = '';
  for (let index = 0; index < bytes.length; index += 8192) binary += String.fromCharCode(...bytes.subarray(index, index + 8192));
  return btoa(binary);
}
const encodeText = (value: string) => encodeBytes(new TextEncoder().encode(value));
function decodeText(value: string): string {
  return new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(atob(value), (character) => character.charCodeAt(0)));
}

function capturePreview(value: string): string {
  if (!value) return 'No retained body bytes.';
  const bytes = Uint8Array.from(atob(value), (character) => character.charCodeAt(0));
  const prefix = bytes.subarray(0, 64 * 1024);
  let output: string;
  try { output = new TextDecoder('utf-8', { fatal: true }).decode(prefix); }
  catch { output = Array.from(prefix, (byte) => byte.toString(16).padStart(2, '0')).join(' '); }
  return output + (bytes.length > prefix.length ? '\n… preview limited to 64 KiB' : '');
}

const defaultRaw = 'GET / HTTP/1.1\nHost: 127.0.0.1\n\n';
const emptyConfig: IntruderConfig = {
  attack: 'sniper', template: { method: 'GET', url: 'http://127.0.0.1/', raw: encodeText(canonicalEditedRequest(defaultRaw)) },
  positions: [], payloadSets: [], requestLimit: 1000, concurrency: 2, ratePerSecond: 5, timeoutMs: 30000,
};

export function IntruderWorkspace({ leaveGuard, source, onConsumeSource }: { leaveGuard?: { current: () => boolean }; source?: IntruderSource | null; onConsumeSource?: () => void }) {
  const [jobs, setJobs] = useState<IntruderJobSummary[]>([]);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [job, setJob] = useState<IntruderJob | null>(null);
  const [config, setConfig] = useState<IntruderConfig>(emptyConfig);
  const [raw, setRaw] = useState(defaultRaw);
  const [rawModified, setRawModified] = useState(false);
  const [rawMode, setRawMode] = useState<'text' | 'hex'>('text');
  const [preview, setPreview] = useState<IntruderPreview | null>(null);
  const [payloads, setPayloads] = useState<Record<string, PayloadEditor>>({});
  const [results, setResults] = useState<IntruderResult[]>([]);
  const [selectedSequence, setSelectedSequence] = useState<number | null>(null);
  const [detail, setDetail] = useState<IntruderResult | null>(null);
  const [draftFilters, setDraftFilters] = useState<IntruderResultFilters>({});
  const [filters, setFilters] = useState<IntruderResultFilters>({});
  const [cursor, setCursor] = useState<number | null>(null);
  const [nextCursor, setNextCursor] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState('');
  const [refresh, setRefresh] = useState(0);
  const rawRef = useRef<HTMLTextAreaElement>(null);
  const editorLoadedID = useRef<string | null>(null);
  const previewGeneration = useRef(0);
  const markDirty = () => { previewGeneration.current += 1; setDirty(true); setPreview(null); };
  const canDiscardDraft = () => !dirty || window.confirm('Discard unsaved Intruder draft changes?');

  if (leaveGuard) leaveGuard.current = canDiscardDraft;

  useEffect(() => {
    if (!source) return;
    editorLoadedID.current = null;
    setSelectedID(null);
    setJob(null);
    setConfig({ ...emptyConfig, template: { method: source.method, url: source.url, raw: encodeText(source.raw) } });
    setRaw(displayRawRequest(source.raw));
    setRawMode('text'); setPreview(null);
    setRawModified(false);
    setPayloads({});
    setResults([]);
    setSelectedSequence(null);
    setDetail(null);
    setCursor(null);
    markDirty();
    onConsumeSource?.();
  }, [source?.revision]);

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
        try { setRaw(displayRawRequest(decodeText(next.config.template.raw))); setRawMode('text'); }
        catch { setRaw(displayRawHex(Uint8Array.from(atob(next.config.template.raw), (character) => character.charCodeAt(0)))); setRawMode('hex'); }
        previewGeneration.current += 1; setPreview(null);
        setRawModified(false);
        const text: Record<string, PayloadEditor> = {};
        for (const set of next.config.payloadSets) {
          text[set.ID] = displayPayloads(set.Payloads);
        }
        setPayloads(text);
      }
    }).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    return () => controller.abort();
  }, [selectedID, refresh]);

  useEffect(() => {
    if (!selectedID) return;
    const controller = new AbortController();
    void listIntruderResults(selectedID, cursor ?? undefined, controller.signal, filters).then((page) => {
      if (controller.signal.aborted) return;
      setResults(page.Results);
      setNextCursor(page.NextBeforeSequence);
    }).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    return () => controller.abort();
  }, [selectedID, cursor, refresh, filters]);

  useEffect(() => {
    if (!selectedID || selectedSequence === null) { setDetail(null); return; }
    const controller = new AbortController();
    setDetail(null);
    void getIntruderResult(selectedID, selectedSequence, controller.signal).then((item) => {
      if (!controller.signal.aborted) setDetail(item);
    }).catch((cause) => { if (!controller.signal.aborted) setError(String(cause)); });
    return () => controller.abort();
  }, [selectedID, selectedSequence, refresh]);

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
    let start: number;
    let end: number;
    try {
      if (rawMode === 'hex') {
        parseRawHex(raw);
        start = hexSelectionOffset(raw, element.selectionStart);
        end = hexSelectionOffset(raw, element.selectionEnd);
      } else {
        const source = rawModified ? canonicalEditedRequest(raw) : decodeText(config.template.raw);
        start = byteOffsetInRaw(source, element.selectionStart);
        end = byteOffsetInRaw(source, element.selectionEnd);
      }
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Invalid request selection.'); return; }
    if (end <= start) { setError('Select at least one complete request byte.'); return; }
    if (config.positions.some((position) => start < position.End && end > position.Start)) { setError('Positions must not overlap.'); return; }
    let nextID = 1;
    while (config.positions.some((position) => position.ID === `p${nextID}`)) nextID += 1;
    const id = `p${nextID}`;
    const position: IntruderPosition = { ID: id, Start: start, End: end, PayloadSetID: id };
    setConfig((current) => ({ ...current, positions: [...current.positions, position] }));
    markDirty();
    setPayloads((current) => ({ ...current, [id]: { mode: 'text', value: '' } }));
    setError('');
  };

  const currentConfig = (): IntruderConfig => ({
    ...config,
    template: { ...config.template, method: rawMode === 'text' ? raw.match(/^[A-Z]+(?= )/)?.[0] ?? config.template.method : new TextDecoder().decode(parseRawHex(raw).subarray(0, 32)).match(/^[A-Z]+(?= )/)?.[0] ?? config.template.method, raw: rawModified ? rawMode === 'hex' ? encodeBytes(parseRawHex(raw)) : encodeText(canonicalEditedRequest(raw)) : config.template.raw },
    payloadSets: config.positions.map((position) => ({
      ID: position.PayloadSetID,
      Payloads: encodePayloadEditor(payloads[position.PayloadSetID] ?? { mode: 'text', value: '' }),
    })),
  });

  const save = () => run(async () => {
    const next = job?.state === 'draft' ? await updateIntruderJob(job, currentConfig()) : await createIntruderJob(currentConfig());
    setDirty(false);
    return next;
  });
  const showPreview = () => { void run(async () => {
    const generation = previewGeneration.current;
    const checked = await previewIntruder(currentConfig());
    if (generation === previewGeneration.current) setPreview(checked);
  }); };
  const changeRawMode = (mode: 'text' | 'hex') => {
    try {
      const bytes = Uint8Array.from(atob(currentConfig().template.raw), (character) => character.charCodeAt(0));
      setRaw(mode === 'hex' ? displayRawHex(bytes) : displayRawRequest(new TextDecoder('utf-8', { fatal: true }).decode(bytes)));
      setRawMode(mode);
      setRawModified(false);
      setConfig((current) => ({ ...current, template: { ...current.template, raw: encodeBytes(bytes) } }));
      setError('');
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Cannot switch request format.'); }
  };
  const control = (action: 'start' | 'pause' | 'resume' | 'abort') => {
    if (job) void run(async () => {
      if (action === 'start') {
        const checked = await previewIntruder(job.config);
        setPreview(checked);
        if (checked.totalRequests > 10000 && job.config.attack === 'cluster_bomb' && !window.confirm(`Start ${checked.totalRequests} Cluster Bomb requests?`)) return;
      }
      return controlIntruderJob(job, action);
    });
  };

  return <section className="intruder-workspace" aria-label="Intruder workspace">
    <header className="intruder-header"><span className="eyebrow">Authorized targets only</span><h1>Intruder</h1><p>Payload-driven HTTP testing. Every generated request must remain in Target Scope.</p></header>
    <aside className="intruder-jobs"><div className="intruder-heading"><h2>Jobs</h2><button type="button" className="quiet-button" onClick={() => { if (!canDiscardDraft()) return; editorLoadedID.current = null; setSelectedID(null); setSelectedSequence(null); setJob(null); setDirty(false); setConfig(emptyConfig); setRaw(defaultRaw); setRawModified(false); setPayloads({}); setResults([]); }}>New</button></div>
      {jobs.length === 0 && <p>No jobs yet.</p>}
      {jobs.map((item) => <button key={item.ID} type="button" className={`intruder-job ${selectedID === item.ID ? 'selected' : ''}`} onClick={() => { if (item.ID !== selectedID && !canDiscardDraft()) return; setSelectedID(item.ID); setSelectedSequence(null); setCursor(null); }}><strong>{item.Attack}</strong><span>{item.State} · {item.CompletedCount}/{item.TotalRequests}</span><small>{item.ID.slice(0, 12)}</small></button>)}
    </aside>
    <div className="intruder-editor"><div className="intruder-heading"><h2>{job ? `Job ${job.id.slice(0, 12)}` : 'New job'}</h2><span>{job?.state ?? 'draft'}</span></div>
      {error && <p className="api-error" role="alert">{error}</p>}
      <div className="intruder-fields">
        <label>Destination URL<input value={config.template.url} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setConfig({ ...config, template: { ...config.template, url: event.target.value } }); }} /></label>
        <label>Attack<select value={config.attack} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setConfig({ ...config, attack: event.target.value as AttackType }); }}><option value="sniper">Sniper</option><option value="battering_ram">Battering Ram</option><option value="pitchfork">Pitchfork</option><option value="cluster_bomb">Cluster Bomb</option></select></label>
        <label>Rate / second<input type="number" min="0.1" max="100" step="0.1" value={config.ratePerSecond} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setConfig({ ...config, ratePerSecond: Number(event.target.value) }); }} /></label>
        <label>Concurrency<input type="number" min="1" max="20" value={config.concurrency} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setConfig({ ...config, concurrency: Number(event.target.value) }); }} /></label>
        <label>Request limit<input type="number" min="1" max="100000" value={config.requestLimit} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setConfig({ ...config, requestLimit: Number(event.target.value) }); }} /></label>
        <label>Timeout (ms)<input type="number" min="1000" max="120000" value={config.timeoutMs} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setConfig({ ...config, timeoutMs: Number(event.target.value) }); }} /></label>
      </div>
      <label>Request format<select value={rawMode} onChange={(event) => changeRawMode(event.target.value as 'text' | 'hex')}><option value="text">Text / UTF-8</option><option value="hex">Hex bytes</option></select></label>
      <label className="intruder-raw-label">Raw HTTP request<textarea ref={rawRef} spellCheck={false} value={raw} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setRawModified(true); setRaw(event.target.value); setConfig((current) => ({ ...current, positions: [] })); setPayloads({}); }} /></label>
      <button type="button" className="quiet-button" disabled={!!job && job.state !== 'draft'} onClick={addPosition}>Mark selected bytes as position</button>
      <div className="intruder-positions">{config.positions.map((position) => {
        const editor = payloads[position.PayloadSetID] ?? { mode: 'text', value: '' };
        return <div key={position.ID}><div className="intruder-heading"><strong>{position.ID} · bytes {position.Start}-{position.End}</strong><button type="button" className="quiet-button" disabled={!!job && job.state !== 'draft'} onClick={() => { markDirty(); setConfig({ ...config, positions: config.positions.filter((candidate) => candidate.ID !== position.ID) }); }}>Remove</button></div>
          <label>Payload format for {position.ID}<select value={editor.mode} disabled={!!job && job.state !== 'draft'} onChange={(event) => {
            try {
              const converted = convertPayloadEditor(editor as PayloadEditor, event.target.value as PayloadEditor['mode']);
              setPayloads({ ...payloads, [position.PayloadSetID]: converted });
              markDirty();
              setError('');
            } catch (cause) { setError(cause instanceof Error ? cause.message : 'Invalid payload'); }
          }}><option value="text">Text / UTF-8</option><option value="hex">Hex bytes</option></select></label>
          <label>{editor.mode === 'hex' ? 'Hex payloads, one per line' : 'Payloads, one per line'}<textarea value={editor.value} disabled={!!job && job.state !== 'draft'} onChange={(event) => { markDirty(); setPayloads({ ...payloads, [position.PayloadSetID]: { ...editor, value: event.target.value } as PayloadEditor }); }} /></label>
        </div>;
      })}</div>
      <div className="intruder-actions"><button type="button" className="quiet-button" disabled={busy || (!!job && job.state !== 'draft')} onClick={showPreview}>Preflight preview</button><button type="button" className="quiet-button" disabled={busy || (!!job && job.state !== 'draft')} onClick={() => void save()}>{job?.state === 'draft' ? 'Save draft' : 'Create draft'}</button>
        {job?.state === 'draft' && <button type="button" className="quiet-button" disabled={busy || dirty} onClick={() => control('start')}>Start</button>}
        {job?.state === 'running' && <><button type="button" className="quiet-button" disabled={busy} onClick={() => control('pause')}>Pause</button><button type="button" className="quiet-button" disabled={busy} onClick={() => control('abort')}>Abort</button></>}
        {job?.state === 'paused' && <><button type="button" className="quiet-button" disabled={busy} onClick={() => control('resume')}>Resume</button><button type="button" className="quiet-button" disabled={busy} onClick={() => control('abort')}>Abort</button></>}
        {job && ['draft', 'aborted', 'completed', 'failed'].includes(job.state) && <button type="button" className="quiet-button" disabled={busy} onClick={() => void run(async () => { await deleteIntruderJob(job.id); setSelectedID(null); setJob(null); })}>Delete</button>}
      </div>
      {preview && <p className="intruder-progress">Preflight: {preview.totalRequests} requests · first request {preview.sampleMethod} {preview.sampleUrl}{preview.warning && ' · Pitchfork stops at the shortest payload set'}</p>}
      {job && <p className="intruder-progress">{job.completedCount} / {job.totalRequests} requests · {job.errorCount} errors {job.stateReason && `· ${job.stateReason}`}</p>}
    </div>
    <section className="intruder-results"><div className="intruder-heading"><h2>Results</h2><span>100 per page</span></div>
      <div className="intruder-filters">
        <label>Status<input type="number" min="100" max="599" value={draftFilters.status ?? ''} onChange={(event) => setDraftFilters({ ...draftFilters, status: event.target.value ? Number(event.target.value) : undefined })} /></label>
        <label>Error<select value={draftFilters.errorCategory ?? ''} onChange={(event) => setDraftFilters({ ...draftFilters, errorCategory: event.target.value })}><option value="">All</option><option value="network">Network</option><option value="timeout">Timeout</option><option value="scope_revoked">Scope revoked</option><option value="cancelled">Cancelled</option><option value="worker_panic">Worker panic</option></select></label>
        <label>MIME<input value={draftFilters.mimeType ?? ''} onChange={(event) => setDraftFilters({ ...draftFilters, mimeType: event.target.value })} /></label>
        <label>Payload contains<input value={draftFilters.payloadSearch ?? ''} onChange={(event) => setDraftFilters({ ...draftFilters, payloadSearch: event.target.value })} /></label>
        <button className="quiet-button" type="button" onClick={() => { setFilters({ ...draftFilters }); setCursor(null); setSelectedSequence(null); }}>Apply filters</button>
      </div>
      <div className="intruder-result-list">{results.map((result) => <button type="button" className="intruder-result" key={result.Sequence} aria-pressed={selectedSequence === result.Sequence} onClick={() => setSelectedSequence(result.Sequence)}><strong>#{result.Sequence}</strong><span>{result.Status || result.ErrorCategory}</span><span>{result.ResponseSize} B</span><span>{result.MIMEType}</span><small>{result.URL}</small></button>)}</div>{selectedID && <div className="intruder-actions"><button type="button" className="quiet-button" disabled={cursor === null} onClick={() => setCursor(null)}>First page</button><button type="button" className="quiet-button" disabled={nextCursor === null} onClick={() => setCursor(nextCursor)}>Older</button></div>}
      {detail && <div className="intruder-detail"><h3>Result #{detail.Sequence}</h3><p>{detail.URL}</p><p>Status {detail.Status || detail.ErrorCategory} · {detail.ResponseSize} bytes · {detail.MIMEType || 'unknown MIME'}</p>{job && job.baselineSequence !== null && <p>Baseline #{job.baselineSequence} · similarity {detail.SimilarityPartial && !detail.BodyStored ? 'unavailable' : `${(detail.Similarity / 100).toFixed(2)}%`}{detail.SimilarityPartial ? ' (partial capture)' : ''} · length Δ {detail.LengthDelta} B · duration Δ {detail.DurationDelta} ms{detail.StatusDiff ? ' · status differs' : ''}{detail.MIMEDiff ? ' · MIME differs' : ''}</p>}{job && ['completed', 'aborted', 'failed'].includes(job.state) && <button type="button" className="quiet-button" disabled={busy || job.baselineSequence === detail.Sequence} onClick={() => void run(() => setIntruderBaseline(job, detail.Sequence))}>Use as baseline</button>}{detail.StorageStatus && <p className="ws-notice">Storage: {detail.StorageStatus}</p>}{detail.ResponseTruncated && <p className="ws-notice">Response capture truncated.</p>}<h4>Response body</h4><pre>{capturePreview(detail.ResponseCapture)}</pre><h4>Request body</h4><pre>{capturePreview(detail.RequestCapture)}</pre></div>}
    </section>
  </section>;
}
