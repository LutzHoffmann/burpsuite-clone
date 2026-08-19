import { useState } from 'react';
import { Boxes, FileText, History, Network, Search, Send, SlidersHorizontal } from 'lucide-react';
import { HistoryTable } from './components/HistoryTable';
import { InterceptPanel } from './components/InterceptPanel';
import { Inspector } from './components/Inspector';
import { Repeater } from './components/Repeater';
import { StatusBar } from './components/StatusBar';
import type { Exchange, HistoryItem, InterceptItem, SendRequest, SendResult } from './types';

const historyItems: HistoryItem[] = [
  { id: 1, method: 'GET', scheme: 'https', host: 'api.internal.test', path: '/v1/users', query: '', status: 200, mimeType: 'JSON', requestSize: 312, responseSize: 1843, durationMs: 42, startedAt: '2026-08-19T10:24:00', intercepted: false, error: false },
  { id: 2, method: 'POST', scheme: 'https', host: 'accounts.test', path: '/session', query: '', status: 201, mimeType: 'JSON', requestSize: 492, responseSize: 823, durationMs: 118, startedAt: '2026-08-19T10:23:00', intercepted: false, error: false },
  { id: 3, method: 'GET', scheme: 'https', host: 'cdn.internal.test', path: '/assets/app.js', query: '', status: 304, mimeType: 'JavaScript', requestSize: 185, responseSize: 0, durationMs: 12, startedAt: '2026-08-19T10:22:00', intercepted: false, error: false },
  { id: 4, method: 'PUT', scheme: 'https', host: 'api.internal.test', path: '/v1/profile', query: '', status: 204, mimeType: 'JSON', requestSize: 746, responseSize: 0, durationMs: 76, startedAt: '2026-08-19T10:21:00', intercepted: false, error: false },
];

const exchanges: Record<number, Exchange> = Object.fromEntries(historyItems.map((item) => [item.id, {
  ID: item.id, Method: item.method, Scheme: item.scheme, Host: item.host, Path: item.path, Query: item.query,
  Status: item.status, MIMEType: item.mimeType, RequestSize: item.requestSize, ResponseSize: item.responseSize,
  Duration: item.durationMs, StartedAt: item.startedAt, Intercepted: item.intercepted, Error: item.error,
  ErrorMessage: '', RequestTruncated: false, ResponseTruncated: false,
  Request: { Headers: { Host: [item.host], Accept: ['application/json'], Authorization: ['Bearer [redacted]'] }, Body: item.method === 'POST' || item.method === 'PUT' ? '{\n  "name": "operator"\n}' : '', Raw: `${item.method} ${item.path} HTTP/1.1\nHost: ${item.host}\nAccept: application/json` },
  Response: { Headers: { 'Content-Type': ['application/json'] }, Body: '', Raw: `HTTP/1.1 ${item.status}` }, Tags: [], Note: '',
}]));

export function App() {
  const [selectedId, setSelectedId] = useState<number | null>(historyItems[0].id);
  const [interceptItems, setInterceptItems] = useState<InterceptItem[]>([]);
  const [repeaterResult, setRepeaterResult] = useState<SendResult | null>(null);
  const exchange = selectedId === null ? null : exchanges[selectedId] ?? null;
  const repeaterRequest: SendRequest = { method: 'GET', url: 'https://api.internal.test/v1/users', headers: { Accept: ['application/json'] }, body: '' };

  const forwardIntercept = (id: string) => setInterceptItems((items) => items.filter((item) => item.id !== id));
  const dropIntercept = (id: string) => setInterceptItems((items) => items.filter((item) => item.id !== id));
  const sendRepeater = (request: SendRequest) => setRepeaterResult({ status: 200, headers: { 'Content-Type': ['application/json'] }, body: request.body, durationMs: 0, size: new Blob([request.body]).size, truncated: false, contentType: 'application/json' });

  return (
    <main className="app-shell">
      <StatusBar />
      <div className="workspace">
        <nav className="navigation" aria-label="Tools">
          <button className="nav-item active" type="button"><Network size={17} />Traffic</button>
          <button className="nav-item" type="button"><History size={17} />History</button>
          <button className="nav-item" type="button"><Send size={17} />Repeater</button>
          <button className="nav-item" type="button"><Boxes size={17} />Extensions</button>
          <div className="nav-spacer" />
          <button className="nav-item" type="button"><SlidersHorizontal size={17} />Settings</button>
        </nav>

        <section className="history-panel" aria-label="Request history">
          <div className="panel-heading">
            <div><span className="eyebrow">Capture</span><h1>Requests</h1></div>
            <button className="icon-button" aria-label="Filter history" type="button"><SlidersHorizontal size={16} /></button>
          </div>
          <label className="search"><Search size={15} /><input placeholder="Filter requests" /></label>
          <HistoryTable items={historyItems} selectedId={selectedId} onSelect={setSelectedId} onSendToRepeater={() => undefined} />
        </section>

        <section className="inspector-panel" aria-label="Exchange inspector">
          <Inspector exchange={exchange} />
        </section>

        <aside className="utility-panel" aria-label="Utilities">
          <div className="utility-heading"><FileText size={16} /> Details</div>
          <dl>
            <div><dt>Result</dt><dd className="ok">{exchange ? `${exchange.Status} OK` : '-'}</dd></div>
            <div><dt>Content-Type</dt><dd>{exchange?.MIMEType ?? '-'}</dd></div>
            <div><dt>Duration</dt><dd>{exchange ? `${exchange.Duration} ms` : '-'}</dd></div>
            <div><dt>Response</dt><dd>{exchange ? `${(exchange.ResponseSize / 1024).toFixed(1)} kB` : '-'}</dd></div>
          </dl>
          <InterceptPanel items={interceptItems} onForward={forwardIntercept} onDrop={dropIntercept} />
        </aside>

        <section className="repeater-workspace">
          <Repeater initialRequest={repeaterRequest} result={repeaterResult} onSend={sendRepeater} />
        </section>
      </div>
    </main>
  );
}
