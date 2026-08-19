import { ArrowRight, Boxes, FileText, History, Network, Play, Search, Send, SlidersHorizontal } from 'lucide-react';
import { StatusBar } from './components/StatusBar';

const requests = [
  ['GET', 'api.internal.test', '/v1/users', '200', '42 ms'],
  ['POST', 'accounts.test', '/session', '201', '118 ms'],
  ['GET', 'cdn.internal.test', '/assets/app.js', '304', '12 ms'],
  ['PUT', 'api.internal.test', '/v1/profile', '204', '76 ms'],
];

export function App() {
  return (
    <main className="app-shell">
      <StatusBar />
      <div className="workspace">
        <nav className="navigation" aria-label="Tools">
          <button className="nav-item active"><Network size={17} />Traffic</button>
          <button className="nav-item"><History size={17} />History</button>
          <button className="nav-item"><Send size={17} />Repeater</button>
          <button className="nav-item"><Boxes size={17} />Extensions</button>
          <div className="nav-spacer" />
          <button className="nav-item"><SlidersHorizontal size={17} />Settings</button>
        </nav>

        <section className="history-panel" aria-label="Request history">
          <div className="panel-heading">
            <div><span className="eyebrow">Capture</span><h1>Requests</h1></div>
            <button className="icon-button" aria-label="Filter history"><SlidersHorizontal size={16} /></button>
          </div>
          <label className="search"><Search size={15} /><input placeholder="Filter requests" /></label>
          <div className="request-table" role="table">
            <div className="table-row table-header" role="row"><span>Method</span><span>Host / path</span><span>Status</span><span>Time</span></div>
            {requests.map(([method, host, path, status, time], index) => (
              <button className={`table-row ${index === 0 ? 'selected' : ''}`} key={`${method}-${path}`} role="row">
                <span className={`method method-${method.toLowerCase()}`}>{method}</span>
                <span className="request-target"><b>{host}</b><small>{path}</small></span>
                <span className="status-code">{status}</span>
                <span>{time}</span>
              </button>
            ))}
          </div>
        </section>

        <section className="inspector-panel" aria-label="Exchange inspector">
          <div className="tab-strip"><button className="tab active">Request</button><button className="tab">Response</button><button className="tab">Render</button></div>
          <div className="inspector-meta"><span>GET https://api.internal.test/v1/users</span><button><Play size={14} /> Send to Repeater</button></div>
          <pre className="code-view"><code><em>GET</em> /v1/users HTTP/1.1{`\n`}Host: api.internal.test{`\n`}Accept: application/json{`\n`}Authorization: Bearer [redacted]{`\n\n`}<em>Response</em> 200 OK</code></pre>
        </section>

        <aside className="utility-panel" aria-label="Utilities">
          <div className="utility-heading"><FileText size={16} /> Details</div>
          <dl>
            <div><dt>Status</dt><dd className="ok">200 OK</dd></div>
            <div><dt>Content-Type</dt><dd>application/json</dd></div>
            <div><dt>Duration</dt><dd>42 ms</dd></div>
            <div><dt>Response</dt><dd>1.8 kB</dd></div>
          </dl>
          <div className="utility-heading queue-title"><ArrowRight size={16} /> Intercept queue <span>0</span></div>
          <p className="empty-state">No requests are waiting for action.</p>
        </aside>
      </div>
    </main>
  );
}
