import { useEffect, useRef, useState } from 'react';
import type { RefObject } from 'react';
import { getWSRepeaterDraft, sendWSRepeater } from '../api/client';
import type { WSRepeatRequest, WSRepeatResult } from '../types';

export type WSSource = { connectionId: number; messageId: number; revision: number };
export type WSLeaveGuard = RefObject<() => boolean>;
const empty = (): WSRepeatRequest => ({ url: '', type: 'text', payload: '', payloadFormat: 'text', headers: {}, subprotocols: [] });
const discardPrompt = 'Discard WebSocket repeater edits and results and cancel any active send?';

function parseHeaders(text: string): Record<string, string[]> {
  const headers: Record<string, string[]> = Object.create(null);
  for (const line of text.split('\n')) {
    if (!line.trim()) continue;
    const colon = line.indexOf(':');
    if (colon < 1) throw new Error('Handshake headers need one Name: value per line.');
    const name = line.slice(0, colon).trim();
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name)) throw new Error('Invalid handshake header name.');
    const value = line.slice(colon + 1).trim();
    (headers[name] ??= []).push(value);
  }
  return headers;
}

function validatePayload(request: WSRepeatRequest) {
  let bytes: Uint8Array;
  if (request.payloadFormat === 'hex') {
    if (!/^(?:[0-9a-fA-F]{2})*$/.test(request.payload)) throw new Error('Hex payload needs pairs of hexadecimal digits without spaces.');
    bytes = Uint8Array.from(request.payload.match(/../g) ?? [], (pair) => Number.parseInt(pair, 16));
    if (request.type === 'text') {
      try { new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
      catch { throw new Error('Text messages require valid UTF-8. Choose binary for other bytes.'); }
    }
  } else {
    if (/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(request.payload)) throw new Error('Payload contains invalid Unicode.');
    bytes = new TextEncoder().encode(request.payload);
  }
  if (bytes.length > 1 << 20) throw new Error('Message exceeds the 1 MiB send limit.');
}

export function WSRepeater({ source, leaveGuard }: { source?: WSSource; leaveGuard?: WSLeaveGuard }) {
  const [draft, setDraft] = useState(empty);
  const [headers, setHeaders] = useState('');
  const [protocols, setProtocols] = useState('');
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<WSRepeatResult | null>(null);
  const [error, setError] = useState('');
  const active = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const hasWork = useRef(false);
  hasWork.current = dirty || busy || loading || result !== null;

  useEffect(() => {
    const confirmLeave = () => !hasWork.current || window.confirm(discardPrompt);
    if (leaveGuard) leaveGuard.current = confirmLeave;
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (hasWork.current) { event.preventDefault(); event.returnValue = ''; }
    };
    window.addEventListener('beforeunload', beforeUnload);
    return () => {
      ++generation.current; active.current?.abort();
      if (leaveGuard) leaveGuard.current = () => true;
      window.removeEventListener('beforeunload', beforeUnload);
    };
  }, [leaveGuard]);

  useEffect(() => {
    if (!source) return;
    if (hasWork.current && !window.confirm(discardPrompt)) return;
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    const revision = ++generation.current;
    setLoading(true); setBusy(false); setError('');
    void getWSRepeaterDraft(source.connectionId, source.messageId, controller.signal).then((next) => {
      if (revision !== generation.current || controller.signal.aborted) return;
      setDraft(next); setHeaders(''); setProtocols(''); setResult(null); setDirty(true);
    }).catch(() => {
      if (revision === generation.current && !controller.signal.aborted) setError('Cannot load this capture as a send draft. Only complete, uncompressed client data messages are eligible.');
    }).finally(() => {
      if (revision === generation.current) setLoading(false);
    });
    // Source changes are explicit user actions; unsent edits must not retrigger loading.
  }, [source]);

  const edit = (patch: Partial<WSRepeatRequest>) => { setDraft((current) => ({ ...current, ...patch })); setDirty(true); };
  const cancel = () => {
    ++generation.current; active.current?.abort();
    setBusy(false); setLoading(false); setError('Canceled. A message already sent cannot be undone.');
  };
  const send = async () => {
    if (busy || loading) return;
    setError('');
    let request: WSRepeatRequest;
    try {
      request = { ...draft, headers: parseHeaders(headers), subprotocols: protocols.split(',').map((value) => value.trim()).filter(Boolean) };
      const url = new URL(request.url);
      if (!['ws:', 'wss:'].includes(url.protocol) || url.username || url.password || url.hash) throw new Error('Use a ws:// or wss:// URL without embedded credentials or a fragment.');
      validatePayload(request);
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Invalid message draft.'); return; }
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    const revision = ++generation.current;
    setBusy(true); setResult(null);
    try {
      const next = await sendWSRepeater(request, controller.signal);
      if (revision !== generation.current || controller.signal.aborted) return;
      setResult(next);
      if (next.sent) setDirty(false);
    } catch (cause) {
      if (revision === generation.current && !controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Send failed.');
    } finally { if (revision === generation.current) setBusy(false); }
  };

  return <section className="ws-repeater" aria-label="WebSocket repeater">
    <header><span className="eyebrow">Independent connection</span><h2>WebSocket Repeater</h2></header>
    <p className="ws-notice">One send opens a new connection. Incoming messages are collected for 5 seconds, up to 20 messages / 1 MiB. They may be unsolicited, not replies. Only current in-scope targets are allowed.</p>
    <p className="ws-notice">Credentials must be entered explicitly. Compression is disabled. Results and credentials are not saved; multi-step login protocols need a future session-based repeater.</p>
    <fieldset disabled={busy || loading}>
      <label>WebSocket URL<input aria-label="WebSocket URL" value={draft.url} maxLength={65536} onChange={(event) => edit({ url: event.target.value })} placeholder="wss://authorized.example/socket" /></label>
      <div className="ws-repeater-options">
        <label>Message type<select aria-label="Message type" value={draft.type} onChange={(event) => edit({ type: event.target.value as WSRepeatRequest['type'] })}><option value="text">Text</option><option value="binary">Binary</option></select></label>
        <label>Payload format<select aria-label="Payload format" value={draft.payloadFormat} onChange={(event) => edit({ payloadFormat: event.target.value as WSRepeatRequest['payloadFormat'] })}><option value="text">Text (UTF-8)</option><option value="hex">Hex</option></select></label>
      </div>
      <label>Message payload<textarea aria-label="Message payload" rows={6} value={draft.payload} maxLength={2 << 20} spellCheck={false} onChange={(event) => edit({ payload: event.target.value })} /></label>
      <label>Handshake headers<textarea aria-label="Handshake headers" rows={3} value={headers} maxLength={16384} autoComplete="off" spellCheck={false} placeholder="Authorization: Bearer ..." onChange={(event) => { setHeaders(event.target.value); setDirty(true); }} /></label>
      <label>Subprotocols<input aria-label="Subprotocols" value={protocols} maxLength={4096} onChange={(event) => { setProtocols(event.target.value); setDirty(true); }} placeholder="chat, other-protocol" /></label>
    </fieldset>
    <p className="ws-notice">Maximum outgoing message: 1 MiB or the smaller configured body limit. Changing format reinterprets the entered payload; it does not convert it.</p>
    <div className="ws-repeater-actions">
      <button className="quiet-button" type="button" disabled={busy || loading} onClick={() => void send()}>Send WebSocket message</button>
      {(busy || loading) && <button className="quiet-button" type="button" onClick={cancel}>Cancel send</button>}
    </div>
    {loading && <p>Loading send draft...</p>}
    {busy && <p>Sending and collecting messages...</p>}
    {error && <p className="api-error" role="status">{error}</p>}
    {result && <section aria-label="WebSocket repeater results">
      <h3>{result.sent ? 'Message sent' : 'Message not sent'}</h3>
      <p>Outcome: {result.outcome} / {result.durationMs} ms{result.subprotocol && <> / Subprotocol: {result.subprotocol}</>}</p>
      <p>Sent means written to the connection, not accepted by the application. No automatic retry.</p>
      {result.messages.length === 0 && <p>No incoming data messages received.</p>}
      {result.messages.map((message, index) => <article className="ws-payload" key={index}>
        <h4>Received {index + 1}: {message.type} / {message.size} bytes observed</h4>
        <p>{message.payloadFormat}{message.truncated && ' / Truncated or incomplete'}</p>
        <pre>{message.payload}</pre>
      </article>)}
    </section>}
  </section>;
}
