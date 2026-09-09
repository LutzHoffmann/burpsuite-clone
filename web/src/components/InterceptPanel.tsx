import { useEffect, useState } from 'react';
import type { InterceptItem } from '../types';

type InterceptPanelProps = {
  enabled: boolean;
  items: InterceptItem[];
  phase?: 'request' | 'response';
  disabled?: boolean;
  onEnabledChange: (enabled: boolean) => void;
  onForward: (item: InterceptItem) => void | Promise<void>;
  onDrop: (id: string) => void | Promise<void>;
};

const bodySize = (body: string) => new Blob([body]).size;
const formatHeaders = (headers: Record<string, string[]>) =>
  Object.entries(headers).flatMap(([name, values]) => values.map((value) => `${name}: ${value}`)).join('\n');
const parseHeaders = (text: string) => text.split('\n').reduce<Record<string, string[]>>((headers, line) => {
  if (!line.trim()) return headers;
  const separator = line.indexOf(':');
  if (separator < 1 || /\r/.test(line)) throw new Error('Use one valid Name: value header per line.');
  const name = line.slice(0, separator).trim();
  const value = line.slice(separator + 1).trim();
  if (name) headers[name] = [...(headers[name] ?? []), value];
  return headers;
}, {});

function InterceptEditor({ item, onForward, onDrop, phase }: Pick<InterceptPanelProps, 'onForward' | 'onDrop' | 'phase'> & { item: InterceptItem }) {
  const response = phase === 'response';
  const prefix = response ? 'Response' : 'Intercept';
  const [statusCode, setStatusCode] = useState(String(item.statusCode ?? 200));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [method, setMethod] = useState(item.method);
  const [url, setURL] = useState(item.url);
  const [headers, setHeaders] = useState(formatHeaders(item.headers));
  const [body, setBody] = useState(item.body);

  useEffect(() => {
    setMethod(item.method);
    setURL(item.url);
    setHeaders(formatHeaders(item.headers));
    setBody(item.body);
    setStatusCode(String(item.statusCode ?? 200));
  }, [item.id]);

  const act = async (drop: boolean) => {
    if (pending) return;
    setError('');
    setPending(true);
    try {
      if (drop) await onDrop(item.id);
      else {
        if (response && (!/^\d{3}$/.test(statusCode) || Number(statusCode) < 200 || Number(statusCode) > 599)) {
          throw new Error('Response status must be an integer from 200 to 599.');
        }
        await onForward({ ...item, method, url, headers: parseHeaders(headers), body: item.bodyEditable ? body : item.body,
          ...(response ? { statusCode: Number(statusCode), phase: 'response' as const } : {}) });
      }
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Action failed');
    } finally {
      setPending(false);
    }
  };

  return (
    <article className="intercept-item">
      <div className="intercept-request">
        {response ? <>
          <span>{item.method} {item.url}</span>
          <label>Status<input aria-label={`Response status ${item.id}`} type="number" min="200" max="599" value={statusCode} onChange={(event) => setStatusCode(event.target.value)} /></label>
        </> : <>
        <label>Method<input aria-label={`Intercept method ${item.id}`} value={method} onChange={(event) => setMethod(event.target.value)} /></label>
        <label>URL<input aria-label={`Intercept URL ${item.id}`} value={url} onChange={(event) => setURL(event.target.value)} /></label>
        </>}
      </div>
      <label className="repeater-field">Headers<textarea aria-label={`${prefix} headers ${item.id}`} value={headers} onChange={(event) => setHeaders(event.target.value)} /></label>
      <label className="repeater-field">Body <small>{item.bodyEditable ? 'Text-safe editing' : 'Body is view-only: binary, encoded, streaming or over the editing limit. Original bytes are preserved.'}</small>
        <textarea aria-label={`${prefix} body ${item.id}`} disabled={!item.bodyEditable} value={body} onChange={(event) => setBody(event.target.value)} />
      </label>
      <div className="intercept-meta">{Object.keys(item.headers).length} headers · {bodySize(body)} B body{item.bodyTruncated ? ' · capture truncated' : ''}</div>
      <div className="intercept-actions">
        <button type="button" disabled={pending} aria-label={`Forward ${response ? 'response ' : ''}${item.id}`} onClick={() => void act(false)}>Forward</button>
        <button className="danger-button" type="button" disabled={pending} aria-label={`Drop ${response ? 'response ' : ''}${item.id}`} onClick={() => void act(true)}>Drop</button>
      </div>
      {error && <p className="api-error" role="alert">{error}</p>}
    </article>
  );
}

export function InterceptPanel({ enabled, items, onEnabledChange, onForward, onDrop, phase = 'request', disabled = false }: InterceptPanelProps) {
  const response = phase === 'response';
  return (
    <section className="intercept-panel" aria-label={response ? 'Response Intercept Queue' : 'Intercept Queue'}>
      <div className="utility-heading">
        {response ? 'Response Intercept Queue' : 'Intercept Queue'} <span>{items.length}</span>
        <label className="intercept-toggle"><input type="checkbox" disabled={disabled} checked={enabled} onChange={(event) => onEnabledChange(event.target.checked)} /> Pause matching {response ? 'responses' : 'requests'}</label>
      </div>
      {items.length === 0 ? (
        <div className="intercept-empty">
          <p>No {response ? 'responses' : 'requests'} are waiting for action.</p>
          <div className="intercept-actions">
            <button type="button" aria-label={response ? 'Forward response' : undefined} disabled>Forward</button>
            <button type="button" aria-label={response ? 'Drop response' : undefined} disabled>Drop</button>
          </div>
        </div>
      ) : items.map((item) => <InterceptEditor item={item} phase={phase} key={item.id} onForward={onForward} onDrop={onDrop} />)}
    </section>
  );
}
