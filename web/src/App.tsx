import { useEffect, useRef, useState } from 'react';
import { Boxes, FileText, History, Map, Network, Search, Send, SlidersHorizontal } from 'lucide-react';
import {
  ApiError,
  dropIntercept as dropInterceptAPI,
  forwardIntercept as forwardInterceptAPI,
  forwardResponse as forwardResponseAPI,
  dropResponse as dropResponseAPI,
  getResponseQueue,
  getExchange,
  getHistory,
  getInterceptConfig,
  getInterceptQueue,
  getScopeState,
  getStatus,
  replaceScopeRules,
  sendRepeater as sendRepeaterAPI,
  updateInterceptConfig,
} from './api/client';
import { connectEvents } from './api/events';
import { HistoryTable } from './components/HistoryTable';
import { InterceptPanel } from './components/InterceptPanel';
import { InterceptRules, matchAllRule } from './components/InterceptRules';
import { Inspector } from './components/Inspector';
import { Repeater } from './components/Repeater';
import { Settings } from './components/Settings';
import { StatusBar } from './components/StatusBar';
import { TargetWorkspace } from './components/TargetWorkspace';
import type { Exchange, HistoryItem, InterceptConfig, InterceptItem, ScopeRule, SendRequest, SendResult, StatusDTO, TargetRefresh } from './types';

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
  startedAt: '2026-08-19T10:24:00Z', intercepted: false, error: false, inScope: false, scopeVersion: 0, scopeRuleId: null,
}];

const emptyRepeaterRequest: SendRequest = { method: 'GET', url: '', headers: {}, body: '' };

function isTargetRefreshType(type: string): type is TargetRefresh['type'] {
  return type === 'scope.changed' || type === 'target.endpoint.updated' || type.startsWith('target.rebuild.');
}

function canonicalIPLiteral(value: string): string | null {
  try {
    const hostname = new URL(`http://[${value}]`).hostname;
    if (!hostname.startsWith('[') || !hostname.endsWith(']')) return null;
    const canonical = hostname.slice(1, -1).toLowerCase();
    const mapped = canonical.match(/^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/);
    if (!mapped) return canonical;
    const high = Number.parseInt(mapped[1], 16);
    const low = Number.parseInt(mapped[2], 16);
    return `${high >>> 8}.${high & 0xff}.${low >>> 8}.${low & 0xff}`;
  } catch {
    return null;
  }
}

function normalizeHost(value: string): string {
  const host = value.trim().toLowerCase();
  if (!host) throw new Error('Host is empty');
  const ip = canonicalIPLiteral(host);
  if (ip) return ip;
  if (/[@/\\?#%:[\]\s]/u.test(host)) throw new Error(`Invalid host ${value}`);
  try {
    const ascii = new URL(`http://${host}`).hostname.toLowerCase();
    if (!ascii) throw new Error(`Invalid host ${value}`);
    // WHATWG canonicalizes numeric DNS-like names as IPv4; Go's IDNA path preserves them.
    return /^[\x00-\x7F]+$/.test(host) ? host : ascii;
  } catch {
    throw new Error(`Invalid host ${value}`);
  }
}

function parsePort(value: string): number {
  if (!/^\d+$/.test(value)) throw new Error(`Invalid port ${value || '(empty)'}`);
  const port = Number(value);
  if (port < 1 || port > 65535) throw new Error(`Invalid port ${value}`);
  return port;
}

function splitAuthority(value: string, scheme: 'http' | 'https') {
  const authority = value.trim();
  if (!authority || /[@/\\?#]/u.test(authority)) throw new Error(`Invalid host ${value}`);
  const defaultPort = scheme === 'https' ? 443 : 80;

  if (authority.startsWith('[')) {
    const match = authority.match(/^\[([^\]]+)\](?::(.*))?$/);
    if (!match) throw new Error(`Invalid host ${value}`);
    const host = canonicalIPLiteral(match[1]);
    if (!host) throw new Error(`Invalid host ${value}`);
    return { host, port: match[2] === undefined ? defaultPort : parsePort(match[2]) };
  }
  if (authority.includes('[') || authority.includes(']')) throw new Error(`Invalid host ${value}`);

  if (authority.includes(':')) {
    const ip = canonicalIPLiteral(authority);
    if (ip) return { host: ip, port: defaultPort };
    if (authority.indexOf(':') !== authority.lastIndexOf(':')) throw new Error(`Invalid host ${value}`);
    const separator = authority.lastIndexOf(':');
    return { host: normalizeHost(authority.slice(0, separator)), port: parsePort(authority.slice(separator + 1)) };
  }
  return { host: normalizeHost(authority), port: defaultPort };
}

function scopeRuleForOrigin(item: HistoryItem): ScopeRule {
  const scheme = item.scheme.toLowerCase();
  if (scheme !== 'http' && scheme !== 'https') throw new Error(`Unsupported scheme ${item.scheme}`);
  const origin = splitAuthority(item.host, scheme);
  return {
    id: 0,
    enabled: true,
    action: 'include',
    scheme,
    hostPattern: origin.host,
    port: origin.port,
    pathPrefix: '/',
  };
}

function equivalentInclude(rule: ScopeRule, candidate: ScopeRule) {
  if (!rule.enabled || rule.action !== 'include' || rule.hostPattern.startsWith('*.') || rule.hostPattern.includes('*')) return false;
  if (rule.scheme.toLowerCase() !== candidate.scheme || rule.port !== candidate.port || (rule.pathPrefix || '/') !== '/') return false;
  try {
    return normalizeHost(rule.hostPattern) === candidate.hostPattern;
  } catch {
    return false;
  }
}

export function App() {
  const [status, setStatus] = useState<StatusDTO>(fallbackStatus);
  const [items, setItems] = useState<HistoryItem[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [exchange, setExchange] = useState<Exchange | null>(null);
  const [interceptItems, setInterceptItems] = useState<InterceptItem[]>([]);
  const [interceptConfig, setInterceptConfig] = useState<InterceptConfig>({ enabled: false, rules: [] });
  const [responseItems, setResponseItems] = useState<InterceptItem[]>([]);
  const [configReady, setConfigReady] = useState(false);
  const [configSaving, setConfigSaving] = useState(false);
  const configRef = useRef(interceptConfig);
  const configPending = useRef(false);
  const configRevision = useRef(0);
  const [repeaterRequest, setRepeaterRequest] = useState<SendRequest>(emptyRepeaterRequest);
  const [repeaterResult, setRepeaterResult] = useState<SendResult | null>(null);
  const [apiErrors, setAPIErrors] = useState<Record<string, string | undefined>>({});
  const [view, setView] = useState<'traffic' | 'target' | 'settings'>('traffic');
  const [historyQuery, setHistoryQuery] = useState('');
  const [historyScope, setHistoryScope] = useState<'all' | 'in' | 'out'>('all');
  const [targetRefresh, setTargetRefresh] = useState<TargetRefresh>({ sequence: 0, type: 'initial' });
  const [addingOriginToScope, setAddingOriginToScope] = useState(false);
  const scopeMutationPending = useRef(false);

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
    };
    const loadConfig = async () => {
      const revision = configRevision.current;
      try {
        const config = await getInterceptConfig();
        if (active && !configPending.current && revision === configRevision.current) {
          configRef.current = config;
          setInterceptConfig(config);
          setConfigReady(true);
          clearError('interceptConfig');
        }
      } catch (error) {
        recordError('interceptConfig', 'Intercept setting unavailable', error);
      }
    };
    const loadResponses = async () => {
      try {
        const queue = await getResponseQueue();
        if (active) setResponseItems(queue ?? []);
        clearError('responses');
      } catch (error) {
        recordError('responses', 'Response queue unavailable', error);
      }
    };

    void loadStatus();
    void loadHistory();
    void loadIntercept();
    void loadConfig();
    void loadResponses();
    const disconnect = connectEvents((event) => {
      const eventType = event.type;
      if (eventType === 'history.entry.created' || eventType === 'history.entry.updated') void loadHistory();
      if (eventType === 'proxy.status.changed') void loadStatus();
      if (eventType.startsWith('intercept.item.') || eventType === 'settings.changed') void loadIntercept();
      if (eventType.startsWith('intercept.response.') || eventType === 'settings.changed') void loadResponses();
      if (eventType === 'settings.changed') void loadConfig();
      if (isTargetRefreshType(eventType)) setTargetRefresh((refresh) => ({ sequence: refresh.sequence + 1, type: eventType }));
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
      setAPIErrors((errors) => ({ ...errors, intercept: undefined }));
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, intercept: `Forward failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    }
  };

  const dropIntercept = async (id: string) => {
    try {
      await dropInterceptAPI(id);
      await loadInterceptQueue();
      setAPIErrors((errors) => ({ ...errors, intercept: undefined }));
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, intercept: `Drop failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    }
  };

  const saveIntercept = async (patch: Partial<InterceptConfig>) => {
    if (configPending.current || !configReady) return;
    configPending.current = true;
    configRevision.current += 1;
    setConfigSaving(true);
    try {
      const current = configRef.current;
      const merged = { ...current, responseEnabled: current.responseEnabled ?? false,
        responseRules: current.responseRules ?? [matchAllRule()], replacementRules: current.replacementRules ?? [], ...patch };
      const saved = await updateInterceptConfig(merged);
      const next = { ...merged, ...saved };
      configRef.current = next;
      setInterceptConfig(next);
      setAPIErrors((errors) => ({ ...errors, interceptConfig: undefined }));
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, interceptConfig: `Intercept setting failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    } finally {
      configPending.current = false;
      configRevision.current += 1;
      setConfigSaving(false);
    }
  };

  const actOnResponse = async (item: InterceptItem | string) => {
    try {
      if (typeof item === 'string') await dropResponseAPI(item);
      else await forwardResponseAPI(item);
      setResponseItems(await getResponseQueue());
      setAPIErrors((errors) => ({ ...errors, responses: undefined }));
    } catch (error) {
      setAPIErrors((errors) => ({ ...errors, responses: `Response ${typeof item === 'string' ? 'drop' : 'forward'} failed: ${error instanceof Error ? error.message : 'request failed'}` }));
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

  const addOriginToScope = async (item: HistoryItem) => {
    if (scopeMutationPending.current) return;
    scopeMutationPending.current = true;
    setAddingOriginToScope(true);
    try {
      const candidate = scopeRuleForOrigin(item);
      const current = await getScopeState();
      if (current.rules.some((rule) => equivalentInclude(rule, candidate))) {
        setAPIErrors((errors) => ({ ...errors, scope: undefined }));
        return;
      }
      await replaceScopeRules(current.version, [...current.rules, candidate]);
      setAPIErrors((errors) => ({ ...errors, scope: undefined }));
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        try {
          await getScopeState();
          setAPIErrors((errors) => ({ ...errors, scope: 'Add to scope failed: Scope changed in another session. Reloaded the latest scope; review and try again.' }));
        } catch (reloadError) {
          setAPIErrors((errors) => ({ ...errors, scope: `Add to scope failed: Scope changed and reload failed: ${reloadError instanceof Error ? reloadError.message : 'request failed'}` }));
        }
        return;
      }
      setAPIErrors((errors) => ({ ...errors, scope: `Add to scope failed: ${error instanceof Error ? error.message : 'request failed'}` }));
    } finally {
      scopeMutationPending.current = false;
      setAddingOriginToScope(false);
    }
  };

  const openTarget = () => {
    if (view !== 'target') setTargetRefresh((refresh) => ({ sequence: refresh.sequence + 1, type: 'initial' }));
    setView('target');
  };

  return (
    <main className="app-shell">
      <StatusBar status={status} />
      <div className="workspace">
        <nav className="navigation" aria-label="Tools">
          <button className={`nav-item ${view === 'traffic' ? 'active' : ''}`} onClick={() => setView('traffic')} type="button"><Network size={17} />Traffic</button>
          <button className="nav-item" type="button"><History size={17} />History</button>
          <button className={`nav-item ${view === 'target' ? 'active' : ''}`} onClick={openTarget} type="button"><Map size={17} />Target</button>
          <button className="nav-item" type="button"><Send size={17} />Repeater</button>
          <button className="nav-item" type="button"><Boxes size={17} />Extensions</button>
          <div className="nav-spacer" />
          <button className={`nav-item ${view === 'settings' ? 'active' : ''}`} onClick={() => setView('settings')} type="button"><SlidersHorizontal size={17} />Settings</button>
        </nav>

        {view === 'target' ? <TargetWorkspace
          refresh={targetRefresh}
          onOpenHistory={(id) => { setSelectedId(id); setView('traffic'); }}
          onSendToRepeater={(id) => { setView('traffic'); void sendHistoryToRepeater(id); }}
        /> : <><section className="history-panel" aria-label="Request history">
          <div className="panel-heading">
            <div><span className="eyebrow">Capture</span><h1>Requests</h1></div>
            <button className="icon-button" aria-label="Filter history" type="button"><SlidersHorizontal size={16} /></button>
          </div>
          <div className="history-controls">
            <label className="search"><Search size={15} /><input onChange={(event) => setHistoryQuery(event.target.value)} placeholder="Filter requests" value={historyQuery} /></label>
            <label className="scope-filter"><span>Scope</span><select aria-label="History scope" onChange={(event) => setHistoryScope(event.target.value as 'all' | 'in' | 'out')} value={historyScope}><option value="all">All</option><option value="in">In</option><option value="out">Out</option></select></label>
          </div>
          <HistoryTable addingToScope={addingOriginToScope} items={items} selectedId={selectedId} query={historyQuery} scopeFilter={historyScope} onSelect={setSelectedId} onSendToRepeater={(id) => void sendHistoryToRepeater(id)} onAddOriginToScope={(item) => void addOriginToScope(item)} />
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
            disabled={!configReady || configSaving}
            onEnabledChange={(enabled) => void saveIntercept({ enabled })}
            onForward={forwardIntercept}
            onDrop={dropIntercept}
          />
          <InterceptPanel phase="response" enabled={interceptConfig.responseEnabled ?? false} items={responseItems}
            disabled={!configReady || configSaving} onEnabledChange={(responseEnabled) => void saveIntercept({ responseEnabled })}
            onForward={actOnResponse} onDrop={actOnResponse} />
          <InterceptRules config={interceptConfig} disabled={!configReady || configSaving} onSave={(patch) => void saveIntercept(patch)} />
          <Repeater initialRequest={repeaterRequest} result={repeaterResult} onSend={(request) => void sendRepeater(request)} />
        </section></>}
      </div>
    </main>
  );
}
