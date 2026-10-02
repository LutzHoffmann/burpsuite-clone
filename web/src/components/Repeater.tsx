import { useRef, useState } from 'react';
import type { SendRequest, SendResult } from '../types';
import { generateCsrfPoc } from '../tools/csrfPoc';
import { draftFromRequest, findResponseMatches, formatRepeaterHeaders, parseRepeaterHeaders } from '../tools/repeaterTabs';
import type { RepeaterDraft } from '../tools/repeaterTabs';

type RepeaterProps = {
  initialRequest: SendRequest;
  result: SendResult | null;
  onSend: (request: SendRequest) => void;
  onSendToIntruder?: (request: SendRequest) => void;
  initialDraft?: RepeaterDraft;
  onDraftChange?: (draft: RepeaterDraft) => void;
  pending?: boolean;
};

export function Repeater({ initialRequest, initialDraft, result, onSend, onSendToIntruder, onDraftChange, pending = false }: RepeaterProps) {
  const initial = initialDraft ?? draftFromRequest(initialRequest);
  const [method, setMethod] = useState(initial.method);
  const [url, setUrl] = useState(initial.url);
  const [headers, setHeaders] = useState(initial.headersText);
  const [body, setBody] = useState(initial.body);
  const [poc, setPoc] = useState('');
  const [pocError, setPocError] = useState('');
  const [responseSearch, setResponseSearch] = useState('');
  const [searchPosition, setSearchPosition] = useState(0);
  const responseBody = useRef<HTMLTextAreaElement>(null);
  const matches = findResponseMatches(result?.body ?? '', responseSearch);

  const clearPoc = () => { setPoc(''); setPocError(''); };

  const snapshot = (patch: Partial<RepeaterDraft>) => onDraftChange?.({ method, url, headersText: headers, body, ...patch });
  const send = () => onSend({ method, url, headers: parseRepeaterHeaders(headers), body });
  const buildPoc = () => {
    try { setPoc(generateCsrfPoc({ method, url, headers: parseRepeaterHeaders(headers), body })); setPocError(''); }
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
  const selectMatch = (position: number) => {
    if (!matches.length) return;
    const index = (position + matches.length) % matches.length;
    setSearchPosition(index);
    responseBody.current?.focus();
    responseBody.current?.setSelectionRange(matches[index], matches[index] + responseSearch.length);
  };

  return (
    <section className="repeater-panel" aria-label="Repeater">
      <div className="repeater-heading"><div><span className="eyebrow">Manual requests</span><h2>Request Editor</h2></div><div className="repeater-heading-actions">{onSendToIntruder && <button className="quiet-button" type="button" onClick={() => onSendToIntruder({ method, url, headers: parseRepeaterHeaders(headers), body })}>Send to Intruder</button>}<button className="quiet-button" type="button" onClick={buildPoc}>Generate CSRF PoC</button><button className="send-button" type="button" disabled={pending} onClick={send}>{pending ? 'Sending...' : 'Send'}</button></div></div>
      <div className="repeater-request-line">
        <label aria-label="Method">Verb<select value={method} onChange={(event) => { setMethod(event.target.value); snapshot({ method: event.target.value }); clearPoc(); }}><option>GET</option><option>POST</option><option>PUT</option><option>PATCH</option><option>DELETE</option></select></label>
        <label>URL<input value={url} onChange={(event) => { setUrl(event.target.value); snapshot({ url: event.target.value }); clearPoc(); }} /></label>
      </div>
      <label className="repeater-field">Headers<textarea value={headers} onChange={(event) => { setHeaders(event.target.value); snapshot({ headersText: event.target.value }); clearPoc(); }} spellCheck={false} /></label>
      <label className="repeater-field">Body <small>Text-safe editing only</small><textarea value={body} onChange={(event) => { setBody(event.target.value); snapshot({ body: event.target.value }); clearPoc(); }} spellCheck={false} /></label>
      {pocError && <p className="repeater-poc-error" role="alert">{pocError}</p>}
      {poc && <div className="repeater-poc"><div className="repeater-poc-heading"><strong>CSRF proof of concept</strong><button className="quiet-button" type="button" onClick={downloadPoc}>Download HTML</button></div><p>Review before use. This form requires manual submission and omits custom headers, cookies, and authorization.</p><label className="repeater-field">Generated HTML<textarea aria-label="CSRF PoC HTML" readOnly value={poc} spellCheck={false} /></label></div>}
      <div className="response-heading"><h2>Response</h2>{result && <span className="ok">{result.status}</span>}</div>
      {result ? <>
        {result.saved === false && <div className="storage-warning" role="alert">
          Response received, but this exchange was not saved to history. {result.storageWarning || 'Capture storage is paused.'}
        </div>}
        <dl className="response-meta"><div><dt>Duration</dt><dd>{result.durationMs} ms</dd></div><div><dt>Size</dt><dd>{result.size} B</dd></div></dl>
        <div className="repeater-response-search"><label>Find in response<input aria-label="Find in response" value={responseSearch} onChange={(event) => { setResponseSearch(event.target.value); setSearchPosition(0); }} /></label><span aria-live="polite">{responseSearch ? `${matches.length === 0 ? 0 : searchPosition + 1} of ${matches.length}${matches.length === 1000 ? '+' : ''}` : 'Literal, case-insensitive'}</span><button type="button" className="quiet-button" disabled={!matches.length} onClick={() => selectMatch(searchPosition - 1)}>Previous match</button><button type="button" className="quiet-button" disabled={!matches.length} onClick={() => selectMatch(searchPosition + 1)}>Next match</button></div>
        <label className="repeater-field">Headers<textarea readOnly value={formatRepeaterHeaders(result.headers)} /></label>
        <label className="repeater-field">Body<textarea ref={responseBody} readOnly value={result.body} /></label>
      </> : <p className="empty-state">Send the request to inspect the local response.</p>}
    </section>
  );
}
