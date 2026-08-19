import { useEffect, useState } from 'react';
import type { InterceptItem } from '../types';

type InterceptPanelProps = {
  enabled: boolean;
  items: InterceptItem[];
  onEnabledChange: (enabled: boolean) => void;
  onForward: (item: InterceptItem) => void;
  onDrop: (id: string) => void;
};

const bodySize = (body: string) => new Blob([body]).size;
const formatHeaders = (headers: Record<string, string[]>) =>
  Object.entries(headers).flatMap(([name, values]) => values.map((value) => `${name}: ${value}`)).join('\n');
const parseHeaders = (text: string) => text.split('\n').reduce<Record<string, string[]>>((headers, line) => {
  const separator = line.indexOf(':');
  if (separator < 1) return headers;
  const name = line.slice(0, separator).trim();
  const value = line.slice(separator + 1).trim();
  if (name) headers[name] = [...(headers[name] ?? []), value];
  return headers;
}, {});

function InterceptEditor({ item, onForward, onDrop }: Pick<InterceptPanelProps, 'onForward' | 'onDrop'> & { item: InterceptItem }) {
  const [method, setMethod] = useState(item.method);
  const [url, setURL] = useState(item.url);
  const [headers, setHeaders] = useState(formatHeaders(item.headers));
  const [body, setBody] = useState(item.body);

  useEffect(() => {
    setMethod(item.method);
    setURL(item.url);
    setHeaders(formatHeaders(item.headers));
    setBody(item.body);
  }, [item]);

  return (
    <article className="intercept-item">
      <div className="intercept-request">
        <label>Method<input aria-label={`Intercept method ${item.id}`} value={method} onChange={(event) => setMethod(event.target.value)} /></label>
        <label>URL<input aria-label={`Intercept URL ${item.id}`} value={url} onChange={(event) => setURL(event.target.value)} /></label>
      </div>
      <label className="repeater-field">Headers<textarea aria-label={`Intercept headers ${item.id}`} value={headers} onChange={(event) => setHeaders(event.target.value)} /></label>
      <label className="repeater-field">Body <small>{item.bodyEditable ? 'Text-safe editing' : 'Binary body is view-only'}</small>
        <textarea aria-label={`Intercept body ${item.id}`} disabled={!item.bodyEditable} value={body} onChange={(event) => setBody(event.target.value)} />
      </label>
      <div className="intercept-meta">{Object.keys(item.headers).length} headers · {bodySize(body)} B body{item.bodyTruncated ? ' · capture truncated' : ''}</div>
      <div className="intercept-actions">
        <button type="button" aria-label={`Forward ${item.id}`} onClick={() => onForward({ ...item, method, url, headers: parseHeaders(headers), body })}>Forward</button>
        <button className="danger-button" type="button" aria-label={`Drop ${item.id}`} onClick={() => onDrop(item.id)}>Drop</button>
      </div>
    </article>
  );
}

export function InterceptPanel({ enabled, items, onEnabledChange, onForward, onDrop }: InterceptPanelProps) {
  return (
    <section className="intercept-panel" aria-label="Intercept Queue">
      <div className="utility-heading">
        Intercept Queue <span>{items.length}</span>
        <label className="intercept-toggle"><input type="checkbox" checked={enabled} onChange={(event) => onEnabledChange(event.target.checked)} /> Pause matching requests</label>
      </div>
      {items.length === 0 ? (
        <div className="intercept-empty">
          <p>No requests are waiting for action.</p>
          <div className="intercept-actions">
            <button type="button" disabled>Forward</button>
            <button type="button" disabled>Drop</button>
          </div>
        </div>
      ) : items.map((item) => <InterceptEditor item={item} key={item.id} onForward={onForward} onDrop={onDrop} />)}
    </section>
  );
}
