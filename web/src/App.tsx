import { useEffect, useState } from 'react';
import { Boxes, FileText, History, Network, Search, Send, SlidersHorizontal } from 'lucide-react';
import {
  dropIntercept as dropInterceptAPI,
  forwardIntercept as forwardInterceptAPI,
  getExchange,
  getHistory,
  getInterceptConfig,
  getInterceptQueue,
  getStatus,
  sendRepeater as sendRepeaterAPI,
  updateInterceptConfig,
} from './api/client';
import { connectEvents } from './api/events';
import { HistoryTable } from './components/HistoryTable';
import { InterceptPanel } from './components/InterceptPanel';
import { Inspector } from './components/Inspector';
import { Repeater } from './components/Repeater';
import { Settings } from './components/Settings';
import { StatusBar } from './components/StatusBar';
import type { Exchange, HistoryItem, InterceptConfig, InterceptItem, SendRequest, SendResult, StatusDTO } from './types';

const fallbackStatus: StatusDTO = {
  apiAddr: '127.0.0.1:9080',
  proxyAddr: '127.0.0.1:8080',
  caFingerprint: '',
  caTrust: 'unavailable',
  httpsInterception: false,
};

const developmentFallback: HistoryItem[] = [{
  id: 1, method: 'GET', scheme: 'https', host: 'api.example.test', path: '/v1/example', query: '',
  status: 200, mimeType: 'application/json', requestSize: 128, responseSize: 512, durationMs: 42,
  startedAt: '2026-08-19T10:24:00Z', intercepted: false, error: false,
}];

const emptyRepeaterRequest: SendRequest = { method: 'GET', url: '', headers: {}, body: '' };

export function App() {
  const [status, setStatus] = useState<StatusDTO>(fallbackStatus);
  const [items, setItems] = useState<HistoryItem[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [exchange, setExchange] = useState<Exchange | null>(null);
  const [interceptItems, setInterceptItems] = useState<InterceptItem[]>([]);
  const [interceptConfig, setInterceptConfig] = useState<InterceptConfig>({ enabled: false, rules: [] });
  const [repeaterRequest, setRepeaterRequest] = useState<SendRequest>(emptyRepeaterRequest);
  const [repeaterResult, setRepeaterResult] = useState<SendResult | null>(null);
  const [apiErrors, setAPIErrors] = useState<Record<string, string | undefined>>({});
  const [view, setView] = useState<'traffic' | 'settings'>('traffic');

  useEffect(() => {
    let active = true;
    const recordError = (key: string, label: string, error: unknown) => {
      if (active) setAPIErrors((errors) => ({ ...errors, [key]: `${label}: ${error instanceof Error ? error.message : 'request failed'}` }));
    };
    const clearError = (key: string) => {
      if (active) setAPIErrors((errors) => ({ ...errors, [key]: undefined }));
    };
    const loadStatus = async () => {
      try {
        const nextStatus = await getStatus();
        if (active) setStatus(nextStatus);
        clearError('status');
      } catch (error) {
        recordError('status', 'Status unavailable', error);
      }
    };
    const loadHistory = async () => {
      try {
        const nextHistory = await getHistory();
        if (active) {
          setItems(nextHistory);
          setSelectedId((id) => nextHistory.some((item) => item.id === id) ? id : nextHistory[0]?.id ?? null);
        }
        clearError('history');
      } catch (error) {
        if (active && import.meta.env.DEV) {
          setItems(developmentFallback);
          setSelectedId((id) => id ?? developmentFallback[0].id);
        }
        recordError('history', 'History unavailable', error);
      }
    };
    const loadIntercept = async () => {
      try {
        const queue = await getInterceptQueue();
        if (active) setInterceptItems(queue);
        clearError('intercept');
      } catch (error) {
        recordError('intercept', 'Intercept unavailable', error);
      }
      try {
        const config = await getInterceptConfig();
        if (active) setInterceptConfig(config);
      } catch (error) {
        recordError('interceptConfig', 'Intercept setting unavailable', error);
      }
    };

    void loadStatus();
    void loadHistory();
    void loadIntercept();
    const disconnect = connectEvents((event) => {
      if (event.type === 'history.entry.created' || event.type === 'history.entry.updated') void loadHistory();
      if (event.type === 'proxy.status.changed') void loadStatus();
      if (event.type.startsWith('intercept.item.') || event.type === 'settings.changed') void loadIntercept();
    });
    return () => {
      active = false;
      disconnect();
    };
  }, []);

  useEffect(() => {
    let active = true;
    if (selectedId === null) {
      setExchange(null);
      return () => { active = false; };
    }
    setExchange(null);
    void getExchange(selectedId).then((detail) => {
      if (active) {
        setExchange(detail);
        setAPIErrors((errors) => ({ ...errors, detail: undefined }));
      }
    }).catch((error) => {
      if (active) setAPIErrors((errors) => ({ ...errors, detail: `Detail unavailable: ${error instanceof Error ? error.message : 'request failed'}` }));
    });
    return () => { active = false; };
  }, [selectedId]);

  const loadInterceptQueue = async () => {
    const queue = await getInterceptQueue();
    setInterceptItems(queue);
  };

  const forwardIntercept = async (item: InterceptItem) => {
    try {
      await forwardInterceptAPI(item.id, item);
      await loadInterceptQueue();
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, intercept: `Forward failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    }
  };

  const dropIntercept = async (id: string) => {
    try {
      await dropInterceptAPI(id);
      await loadInterceptQueue();
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, intercept: `Drop failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    }
  };

  const toggleIntercept = async (enabled: boolean) => {
    try {
      const next = await updateInterceptConfig({ ...interceptConfig, enabled });
      setInterceptConfig(next);
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, intercept: `Intercept setting failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    }
  };

  const sendHistoryToRepeater = async (id: number) => {
    try {
      const detail = exchange?.id === id ? exchange : await getExchange(id);
      const query = detail.query ? `?${detail.query}` : '';
      setRepeaterRequest({
        method: detail.method,
        url: `${detail.scheme}://${detail.host}${detail.path}${query}`,
        headers: detail.request.headers,
        body: detail.request.textSafe ? detail.request.body : '',
      });
      setRepeaterResult(null);
      setAPIErrors((errors) => ({ ...errors, repeater: undefined }));
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, repeater: `Send to Repeater failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    }
  };

  const sendRepeater = async (request: SendRequest) => {
    try {
      const result = await sendRepeaterAPI('default', request);
      setRepeaterResult(result);
      setAPIErrors((errors) => ({ ...errors, repeater: undefined }));
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, repeater: `Repeater send failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    }
  };

  return (
    <main className="app-shell">
      <StatusBar status={status} />
      <div className="workspace">
        <nav className="navigation" aria-label="Tools">
          <button className={`nav-item ${view === 'traffic' ? 'active' : ''}`} onClick={() => setView('traffic')} type="button"><Network size={17} />Traffic</button>
          <button className="nav-item" type="button"><History size={17} />History</button>
          <button className="nav-item" type="button"><Send size={17} />Repeater</button>
          <button className="nav-item" type="button"><Boxes size={17} />Extensions</button>
          <div className="nav-spacer" />
          <button className={`nav-item ${view === 'settings' ? 'active' : ''}`} onClick={() => setView('settings')} type="button"><SlidersHorizontal size={17} />Settings</button>
        </nav>

        <section className="history-panel" aria-label="Request history">
          <div className="panel-heading">
            <div><span className="eyebrow">Capture</span><h1>Requests</h1></div>
            <button className="icon-button" aria-label="Filter history" type="button"><SlidersHorizontal size={16} /></button>
          </div>
          <label className="search"><Search size={15} /><input placeholder="Filter requests" /></label>
          <HistoryTable items={items} selectedId={selectedId} onSelect={setSelectedId} onSendToRepeater={(id) => void sendHistoryToRepeater(id)} />
        </section>

        <section className="inspector-panel" aria-label="Exchange inspector">
          <Inspector exchange={exchange} />
        </section>

        <aside className={`utility-panel ${view === 'settings' ? 'settings-utility' : ''}`} aria-label="Utilities">
          {view === 'settings' ? <Settings status={status} /> : <>
            <div className="utility-heading"><FileText size={16} /> Details</div>
            <dl>
              <div><dt>Result</dt><dd className={exchange?.error ? 'error' : 'ok'}>{exchange ? (exchange.error ? 'ERR' : exchange.status) : '-'}</dd></div>
              <div><dt>Content-Type</dt><dd>{exchange?.mimeType ?? '-'}</dd></div>
              <div><dt>Duration</dt><dd>{exchange ? `${exchange.durationMs} ms` : '-'}</dd></div>
              <div><dt>Response</dt><dd>{exchange ? `${(exchange.responseSize / 1024).toFixed(1)} kB` : '-'}</dd></div>
            </dl>
          </>}
        </aside>

        <section className="repeater-workspace">
          {Object.values(apiErrors).filter(Boolean).length > 0 && <div className="api-error" role="status">{Object.values(apiErrors).filter(Boolean).join(' ')}</div>}
          <InterceptPanel
            enabled={interceptConfig.enabled}
            items={interceptItems}
            onEnabledChange={(enabled) => void toggleIntercept(enabled)}
            onForward={(item) => void forwardIntercept(item)}
            onDrop={(id) => void dropIntercept(id)}
          />
          <Repeater initialRequest={repeaterRequest} result={repeaterResult} onSend={(request) => void sendRepeater(request)} />
        </section>
      </div>
    </main>
  );
}
