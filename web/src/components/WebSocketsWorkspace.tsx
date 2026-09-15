import { useEffect, useState } from 'react';
import { getWSConnection, getWSConnections, getWSMessage, getWSMessages } from '../api/client';
import { useWSPages } from '../api/useWSPages';
import type { WSConnection, WSMessage, WSMessageDetail } from '../types';
import { WSRepeater } from './WSRepeater';
import type { WSLeaveGuard, WSSource } from './WSRepeater';

function Pagination<T>({ label, page }: { label: string; page: ReturnType<typeof useWSPages<T>> }) {
  return <>
    <nav className="history-pagination" aria-label={`${label} pagination`}>
      <button className="quiet-button" type="button" disabled={page.loading || page.pageNumber === 1} onClick={page.previous}>Previous</button>
      <span>Page {page.pageNumber} / {page.items.length} of 100 entries</span>
      <button className="quiet-button" type="button" disabled={page.loading || !page.nextBeforeId} onClick={page.next}>Next</button>
      <button className="quiet-button" type="button" disabled={page.loading} onClick={page.refresh}>{page.newTraffic ? 'New traffic / Refresh' : 'Refresh'}</button>
    </nav>
    {page.refreshed && !page.newTraffic && <p className="ws-notice" role="status">Refreshed. Polling every 5 seconds.</p>}
    {page.error && <p className="api-error" role="status">{page.error}</p>}
    {page.loading && <p className="history-message">Loading {label.toLowerCase()}...</p>}
    {!page.loading && !page.error && !page.items.length && <p className="history-message">No {label.toLowerCase()} captured.</p>}
  </>;
}

function ConnectionLabels({ connection }: { connection: WSConnection }) {
  return <span className="ws-labels">
    <span>{connection.inScope ? 'In scope' : 'Out of scope'}</span>
    <span>{connection.state}</span>
    <span>Capture gaps: {connection.gaps}</span>
    {connection.captureIncomplete && <span>Capture incomplete</span>}
  </span>;
}

function MessageLabels({ message }: { message: WSMessage }) {
  return <span className="ws-labels">
    <span>{message.direction}</span><span>{message.type}</span><span>{message.size} bytes observed</span>
    <span>Encoding: {message.encoding || 'identity'}</span>
    {message.truncated && <span>Truncated</span>}
    {!message.complete && <span>Incomplete</span>}
  </span>;
}

function MessageDetail({ connectionId, id, onUse }: { connectionId: number; id: number; onUse: (connectionId: number, messageId: number) => void }) {
  const [message, setMessage] = useState<WSMessageDetail | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    void getWSMessage(connectionId, id).then((next) => { if (active) setMessage(next); })
      .catch((cause) => { if (active) setError(`Message unavailable: ${cause instanceof Error ? cause.message : 'request failed'}`); });
    return () => { active = false; };
  }, [connectionId, id]);
  return <section className="ws-payload" aria-label="WebSocket message detail">
    <h2>Message {id}</h2>
    {error ? <p className="api-error" role="status">{error}</p> : !message ? <p>Loading message...</p> : <>
      <MessageLabels message={message} />
      <p>Sequence {message.sequence} / <time>{message.observedAt}</time></p>
      <p>Payload format: {message.payloadFormat}. Retained prefix only when truncated.</p>
      {message.encoding && !['identity', 'utf-8'].includes(message.encoding) && <p className="ws-notice">Opaque payload: not decoded or decompressed.</p>}
      <pre>{message.payload}</pre>
      <button className="quiet-button" type="button" disabled={message.direction !== 'client-to-server' || !message.complete || message.truncated || !['text', 'binary'].includes(message.type) || !['identity', 'utf-8'].includes(message.encoding)} onClick={() => onUse(connectionId, id)}>Use in WebSocket Repeater</button>
      <p className="ws-notice">Only complete, uncompressed client data messages can be transferred. Credentials are not copied.</p>
    </>}
  </section>;
}

function ConnectionDetail({ id, onUse }: { id: number; onUse: (connectionId: number, messageId: number) => void }) {
  const [connection, setConnection] = useState<WSConnection | null>(null);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<number | null>(null);
  const page = useWSPages((cursor) => getWSMessages(id, cursor));
  useEffect(() => {
    let active = true;
    let busy = false;
    const load = async () => {
      if (busy) return;
      busy = true;
      try {
        const next = await getWSConnection(id);
        if (active) { setConnection(next); setError(''); }
      } catch (cause) {
        if (active) setError(`Connection unavailable: ${cause instanceof Error ? cause.message : 'request failed'}`);
      } finally { busy = false; }
    };
    void load();
    const timer = setInterval(() => void load(), 5000);
    return () => { active = false; clearInterval(timer); };
  }, [id]);
  return <section className="ws-messages" aria-label="WebSocket messages">
    <div className="ws-connection-detail" aria-label="Connection detail">
      {error && <p className="api-error" role="status">{error}</p>}
      {connection ? <><h2>{connection.url}</h2><ConnectionLabels connection={connection} />
        <p>Opened <time>{connection.openedAt}</time>{connection.closedAt && <> / Closed <time>{connection.closedAt}</time></>}</p>
      </> : !error && <p>Loading connection...</p>}
    </div>
    <h2 className="ws-heading">Messages</h2>
    <p className="ws-notice">Sequence is observation order, not causal order across directions.</p>
    <Pagination label="Messages" page={page} />
    <div className="ws-list">
      {page.items.map((message) => <button className="ws-row" type="button" key={message.id} aria-label={`Select message ${message.id}`} aria-pressed={selected === message.id} onClick={() => setSelected(message.id)}>
        <strong>#{message.sequence}</strong><time>{message.observedAt}</time><MessageLabels message={message} />
      </button>)}
    </div>
    {selected !== null ? <MessageDetail key={selected} connectionId={id} id={selected} onUse={onUse} /> : <p className="history-message">Select a message to inspect its payload.</p>}
  </section>;
}

export function WebSocketsWorkspace({ leaveGuard }: { leaveGuard?: WSLeaveGuard }) {
  const page = useWSPages(getWSConnections);
  const [selected, setSelected] = useState<number | null>(null);
  const [source, setSource] = useState<WSSource>();
  const useMessage = (connectionId: number, messageId: number) => setSource((current) => ({ connectionId, messageId, revision: (current?.revision ?? 0) + 1 }));
  return <section className="ws-workspace" aria-label="WebSockets workspace">
    <header className="ws-header"><span className="eyebrow">Passive capture</span><h1>WebSockets</h1>
      <p>WebSocket handshake and messages bypass HTTP interception and replacement. History stays read-only; the repeater sends on a separate connection.</p>
      <p>Capture-time scope is labeled; out-of-scope traffic is also recorded. Gaps indicate missing observations, not dropped network bytes.</p>
    </header>
    <section className="ws-connections" aria-label="WebSocket connections">
      <h2 className="ws-heading">Connections</h2>
      <Pagination label="Connections" page={page} />
      <div className="ws-list">
        {page.items.map((connection) => <button className="ws-row" type="button" key={connection.id} aria-label={`Select connection ${connection.id}`} aria-pressed={selected === connection.id} onClick={() => setSelected(connection.id)}>
          <strong>{connection.url}</strong><time>{connection.openedAt}</time><ConnectionLabels connection={connection} />
        </button>)}
      </div>
    </section>
    {selected !== null ? <ConnectionDetail key={selected} id={selected} onUse={useMessage} /> : <p className="history-message">Select a connection to view messages.</p>}
    <WSRepeater source={source} leaveGuard={leaveGuard} />
  </section>;
}
