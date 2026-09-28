import { useEffect, useState } from 'react';
import type { SendRequest, SendResult } from '../types';
import { generateCsrfPoc } from '../tools/csrfPoc';

type RepeaterProps = {
  initialRequest: SendRequest;
  result: SendResult | null;
  onSend: (request: SendRequest) => void;
  onSendToIntruder?: (request: SendRequest) => void;
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

export function Repeater({ initialRequest, result, onSend, onSendToIntruder }: RepeaterProps) {
  const [method, setMethod] = useState(initialRequest.method);
  const [url, setUrl] = useState(initialRequest.url);
  const [headers, setHeaders] = useState(formatHeaders(initialRequest.headers));
  const [body, setBody] = useState(initialRequest.body);
  const [poc, setPoc] = useState('');
  const [pocError, setPocError] = useState('');

  const clearPoc = () => { setPoc(''); setPocError(''); };

  useEffect(() => {
    setMethod(initialRequest.method);
    setUrl(initialRequest.url);
    setHeaders(formatHeaders(initialRequest.headers));
    setBody(initialRequest.body);
    setPoc('');
    setPocError('');
  }, [initialRequest]);

  const send = () => onSend({ method, url, headers: parseHeaders(headers), body });
  const buildPoc = () => {
    try { setPoc(generateCsrfPoc({ method, url, headers: parseHeaders(headers), body })); setPocError(''); }
    catch (error) { setPoc(''); setPocError(error instanceof Error ? error.message : 'PoC generation failed'); }
  };
  const downloadPoc = () => {
    if (!poc) return;
    const objectUrl = URL.createObjectURL(new Blob([poc], { type: 'text/html;charset=utf-8' }));
    const anchor = document.createElement('a');
    anchor.href = objectUrl;
    anchor.download = 'csrf-poc.html';
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(objectUrl), 0);
  };

  return (
    <section className="repeater-panel" aria-label="Repeater">
      <div className="repeater-heading"><div><span className="eyebrow">Single Tab</span><h2>Request Editor</h2></div><div className="repeater-heading-actions">{onSendToIntruder && <button className="quiet-button" type="button" onClick={() => onSendToIntruder({ method, url, headers: parseHeaders(headers), body })}>Send to Intruder</button>}<button className="quiet-button" type="button" onClick={buildPoc}>Generate CSRF PoC</button><button className="send-button" type="button" onClick={send}>Send</button></div></div>
      <div className="repeater-request-line">
        <label aria-label="Method">Verb<select value={method} onChange={(event) => { setMethod(event.target.value); clearPoc(); }}><option>GET</option><option>POST</option><option>PUT</option><option>PATCH</option><option>DELETE</option></select></label>
        <label>URL<input value={url} onChange={(event) => { setUrl(event.target.value); clearPoc(); }} /></label>
      </div>
      <label className="repeater-field">Headers<textarea value={headers} onChange={(event) => { setHeaders(event.target.value); clearPoc(); }} spellCheck={false} /></label>
      <label className="repeater-field">Body <small>Text-safe editing only</small><textarea value={body} onChange={(event) => { setBody(event.target.value); clearPoc(); }} spellCheck={false} /></label>
      {pocError && <p className="repeater-poc-error" role="alert">{pocError}</p>}
      {poc && <div className="repeater-poc"><div className="repeater-poc-heading"><strong>CSRF proof of concept</strong><button className="quiet-button" type="button" onClick={downloadPoc}>Download HTML</button></div><p>Review before use. This form requires manual submission and omits custom headers, cookies, and authorization.</p><label className="repeater-field">Generated HTML<textarea aria-label="CSRF PoC HTML" readOnly value={poc} spellCheck={false} /></label></div>}
      <div className="response-heading"><h2>Response</h2>{result && <span className="ok">{result.status}</span>}</div>
      {result ? <>
        {result.saved === false && <div className="storage-warning" role="alert">
          Response received, but this exchange was not saved to history. {result.storageWarning || 'Capture storage is paused.'}
        </div>}
        <dl className="response-meta"><div><dt>Duration</dt><dd>{result.durationMs} ms</dd></div><div><dt>Size</dt><dd>{result.size} B</dd></div></dl>
        <label className="repeater-field">Headers<textarea readOnly value={formatHeaders(result.headers)} /></label>
        <label className="repeater-field">Body<textarea readOnly value={result.body} /></label>
      </> : <p className="empty-state">Send the request to inspect the local response.</p>}
    </section>
  );
}
