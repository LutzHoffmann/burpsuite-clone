import { useEffect, useState } from 'react';
import type { SendRequest, SendResult } from '../types';

type RepeaterProps = {
  initialRequest: SendRequest;
  result: SendResult | null;
  onSend: (request: SendRequest) => void;
};

const formatHeaders = (headers: Record<string, string[]>) =>
  Object.entries(headers).flatMap(([name, values]) => values.map((value) => `${name}: ${value}`)).join('\n');

const parseHeaders = (text: string) => text.split('\n').reduce<Record<string, string[]>>((headers, line) => {
  const separator = line.indexOf(':');
  if (separator < 1) return headers;
  const name = line.slice(0, separator).trim();
  const value = line.slice(separator + 1).trim();
  if (name && value) headers[name] = [...(headers[name] ?? []), value];
  return headers;
}, {});

export function Repeater({ initialRequest, result, onSend }: RepeaterProps) {
  const [method, setMethod] = useState(initialRequest.method);
  const [url, setUrl] = useState(initialRequest.url);
  const [headers, setHeaders] = useState(formatHeaders(initialRequest.headers));
  const [body, setBody] = useState(initialRequest.body);

  useEffect(() => {
    setMethod(initialRequest.method);
    setUrl(initialRequest.url);
    setHeaders(formatHeaders(initialRequest.headers));
    setBody(initialRequest.body);
  }, [initialRequest]);

  const send = () => onSend({ method, url, headers: parseHeaders(headers), body });

  return (
    <section className="repeater-panel" aria-label="Repeater">
      <div className="repeater-heading"><div><span className="eyebrow">Single Tab</span><h2>Request Editor</h2></div><button className="send-button" type="button" onClick={send}>Send</button></div>
      <div className="repeater-request-line">
        <label aria-label="Method">Verb<select value={method} onChange={(event) => setMethod(event.target.value)}><option>GET</option><option>POST</option><option>PUT</option><option>PATCH</option><option>DELETE</option></select></label>
        <label>URL<input value={url} onChange={(event) => setUrl(event.target.value)} /></label>
      </div>
      <label className="repeater-field">Headers<textarea value={headers} onChange={(event) => setHeaders(event.target.value)} spellCheck={false} /></label>
      <label className="repeater-field">Body <small>Text-safe editing only</small><textarea value={body} onChange={(event) => setBody(event.target.value)} spellCheck={false} /></label>
      <div className="response-heading"><h2>Response</h2>{result && <span className="ok">{result.status}</span>}</div>
      {result ? <>
        <dl className="response-meta"><div><dt>Duration</dt><dd>{result.durationMs} ms</dd></div><div><dt>Size</dt><dd>{result.size} B</dd></div></dl>
        <label className="repeater-field">Headers<textarea readOnly value={formatHeaders(result.headers)} /></label>
        <label className="repeater-field">Body<textarea readOnly value={result.body} /></label>
      </> : <p className="empty-state">Send the request to inspect the local response.</p>}
    </section>
  );
}
