import { act } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { App } from './App';
import { Inspector } from './components/Inspector';
import type { HistoryItem, ScopeState, TargetTreeNode } from './types';

type RecordedSocket = {
  close: ReturnType<typeof vi.fn>;
  emit: (type: string) => void;
};

let recordedSockets: RecordedSocket[] = [];
let scopeUpdates: Array<{ version: number; rules: unknown[] }> = [];

function installFakeEventSocket(): RecordedSocket {
  const listeners: Array<(event: MessageEvent) => void> = [];
  const socket: RecordedSocket = {
    close: vi.fn(),
    emit: (type) => act(() => listeners.forEach((listener) => listener({ data: JSON.stringify({ type, data: {} }) } as MessageEvent))),
  };
  class FakeWebSocket {
    close = socket.close;
    addEventListener(type: string, listener: (event: MessageEvent) => void) {
      if (type === 'message') listeners.push(listener);
    }
  }
  vi.stubGlobal('WebSocket', FakeWebSocket);
  recordedSockets.push(socket);
  return socket;
}

const targetTreeFixture: TargetTreeNode[] = [{
  id: 7, scheme: 'https', host: 'target.test', port: 443, path: '/from-target', method: 'GET', inScope: true,
  statuses: [200], requestMimes: ['text/plain'], responseMimes: ['text/plain'], count: 1,
  lastSeen: '2026-08-20T10:05:00Z', children: [],
}];

const scopeFixture: ScopeState = { version: 12, rules: [] };

function strictBaseResponse(path: string, history: HistoryItem[]) {
  if (path === '/api/status') return jsonResponse(statusFixture);
  if (path === '/api/history') return jsonResponse(history);
  if (path === '/api/intercept/queue') return jsonResponse([]);
  if (path === '/api/intercept/config') return jsonResponse({ enabled: false, rules: [] });
  const match = path.match(/^\/api\/history\/(\d+)$/);
  if (match) {
    const item = history.find(({ id }) => id === Number(match[1]));
    if (item) return jsonResponse({ ...exchangeFixture(item.id, item.host, item.path, `response ${item.id}`), ...item });
  }
  return null;
}

function fullAppTargetFetchFixture(): ReturnType<typeof vi.fn> {
  const history = [
    { ...historyFixture(42, 'target.test', '/from-target'), query: 'q=1', inScope: true },
    { ...historyFixture(43, 'outside.test', '/public'), method: 'GET', query: 'token=needle', inScope: false },
  ];
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    const base = strictBaseResponse(path, history);
    if (base) return base;
    if (path === '/api/scope/rules' && (!init?.method || init.method === 'GET')) return jsonResponse(scopeFixture);
    if (path === '/api/target/tree') return jsonResponse(targetTreeFixture);
    if (path === '/api/target/rebuild' && (!init?.method || init.method === 'GET')) {
      return jsonResponse({ id: 3, scopeVersion: 12, activeScopeVersion: 12, status: 'building', processed: 2, total: 5, error: '' });
    }
    if (path === '/api/target/endpoints/7') return jsonResponse({
      id: 7, scheme: 'https', host: 'target.test', port: 443, path: '/from-target', method: 'GET', inScope: true,
      firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 1, statuses: [200],
      requestMimes: ['text/plain'], responseMimes: ['text/plain'], parseDiagnostics: [], errorSeen: false, latestExchangeId: 43,
    });
    if (path === '/api/target/endpoints/7/requests') return jsonResponse([{ exchangeId: 43, startedAt: '2026-08-20T10:05:00Z', status: 200, error: false }]);
    if (path === '/api/target/endpoints/7/parameters') return jsonResponse([]);
    throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${path}`);
  });
}

function countFetches(fetchMock: ReturnType<typeof vi.fn>, path: string, method = 'GET') {
  return fetchMock.mock.calls.filter(([input, init]) => {
    const requestPath = new URL(String(input), 'http://localhost').pathname;
    return requestPath === path && (init?.method ?? 'GET') === method;
  }).length;
}

function historyAddToScopeFetchFixture(options: { item?: HistoryItem; scope?: ScopeState; conflict?: boolean } = {}): ReturnType<typeof vi.fn> {
  const item = options.item ?? ({ ...historyFixture(51, 'Example.TEST', '/admin'), scheme: 'https', inScope: false } as HistoryItem);
  const history = [item];
  let scopeReads = 0;
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    const base = strictBaseResponse(path, history);
    if (base) return base;
    if (path === '/api/scope/rules' && (!init?.method || init.method === 'GET')) {
      scopeReads += 1;
      return jsonResponse({ ...(options.scope ?? scopeFixture), version: (options.scope ?? scopeFixture).version + (scopeReads > 1 ? 1 : 0) });
    }
    if (path === '/api/scope/rules' && init?.method === 'PUT') {
      scopeUpdates.push(JSON.parse(String(init.body)));
      if (options.conflict) return new Response('{}', { status: 409, statusText: 'Conflict' });
      return jsonResponse({ version: (options.scope ?? scopeFixture).version + 1, rules: scopeUpdates.at(-1)?.rules ?? [] });
    }
    throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${path}`);
  });
}

function lastScopeUpdate() {
  return scopeUpdates.at(-1);
}

beforeEach(() => {
	recordedSockets = [];
  scopeUpdates = [];
	vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)));
  installFakeEventSocket();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test('loads status from api', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/api/status')) {
      return new Response(JSON.stringify({
        apiAddr: '127.0.0.1:9080',
        proxyAddr: '127.0.0.1:18080',
        caFingerprint: 'AA:BB',
        caTrust: 'manual',
        httpsInterception: true,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/history')) {
      return new Response(JSON.stringify([]), { status: 200 });
    }
    return new Response('{}', { status: 404 });
  }));
  render(<App />);
  expect(await screen.findByText('127.0.0.1:18080')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  expect(screen.getByText('Manual setup required')).toBeInTheDocument();
  expect(screen.getByText(/install and trust the local ca certificate/i)).toBeInTheDocument();
});

test('renders operator shell status', () => {
  render(<App />);
  expect(screen.getByText('Proxy')).toBeInTheDocument();
  expect(screen.getByText('History')).toBeInTheDocument();
  expect(screen.getByText('Repeater')).toBeInTheDocument();
});

test('shows history columns and inspector tabs', () => {
  render(<App />);
  expect(screen.getByText('Method')).toBeInTheDocument();
  expect(screen.getByText('Host')).toBeInTheDocument();
  expect(screen.getByText('Status')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Headers' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Body' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Raw' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Cookies' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Query' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Timing' })).toBeInTheDocument();
});

test('keeps inspector tabs available when no exchange is selected', () => {
  render(<Inspector exchange={null} />);

  expect(screen.getByRole('tab', { name: 'Headers' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Timing' })).toBeInTheDocument();
  expect(screen.getByText('Select a request to inspect its exchange.')).toBeInTheDocument();
});

test('shows intercept and repeater controls', () => {
  render(<App />);
  expect(screen.getByText('Intercept Queue')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Forward' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Drop' })).toBeInTheDocument();
  expect(screen.getByText('Request Editor')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
});

test('keeps the intercept queue outside the hidden utilities panel', () => {
  render(<App />);

  expect(screen.getByLabelText('Intercept Queue').closest('.utility-panel')).toBeNull();
});

test('selects history rows and loads request and response detail from api', async () => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/api/status')) return jsonResponse(statusFixture);
    if (url.endsWith('/api/history')) return jsonResponse([
      historyFixture(1, 'first.test', '/one'),
      historyFixture(2, 'second.test', '/two'),
    ]);
    if (url.endsWith('/api/history/1')) return jsonResponse(exchangeFixture(1, 'first.test', '/one', 'first response'));
    if (url.endsWith('/api/history/2')) return jsonResponse(exchangeFixture(2, 'second.test', '/two', 'second response'));
    if (url.endsWith('/api/intercept/queue')) return jsonResponse([]);
    return new Response('{}', { status: 404 });
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<App />);
  fireEvent.click(await screen.findByText('second.test'));
  fireEvent.click(screen.getByRole('tab', { name: 'Body' }));
  expect(await screen.findByText(/second response/)).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledWith('/api/history/2', undefined);
	fireEvent.click(screen.getByRole('tab', { name: 'Timing' }));
  expect(screen.getByText(/Duration: 125 ms/)).toBeInTheDocument();
});

test('sends a selected history request to repeater through the api', async () => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/status')) return jsonResponse(statusFixture);
    if (url.endsWith('/api/history')) return jsonResponse([historyFixture(7, 'replay.test', '/submit')]);
    if (url.endsWith('/api/history/7')) return jsonResponse(exchangeFixture(7, 'replay.test', '/submit', 'captured'));
    if (url.endsWith('/api/intercept/queue')) return jsonResponse([]);
    if (url.endsWith('/api/repeater/sessions/default/send')) {
      expect(init?.method).toBe('POST');
      expect(JSON.parse(String(init?.body))).toMatchObject({ method: 'POST', url: 'https://replay.test/submit', body: 'request text' });
      return jsonResponse({ status: 202, headers: { 'Content-Type': ['text/plain'] }, body: 'real response', durationMs: 9, size: 13, truncated: false, contentType: 'text/plain' });
    }
    return new Response('{}', { status: 404 });
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<App />);
  const rowHost = await screen.findByText('replay.test');
  fireEvent.doubleClick(rowHost.closest('[role="row"]')!.querySelector('button')!);
  await waitFor(() => expect(screen.getByLabelText('URL')).toHaveValue('https://replay.test/submit'));
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  expect(await screen.findByDisplayValue('real response')).toBeInTheDocument();
});

test('loads intercept queue and calls edit-forward and drop api actions', async () => {
  const actions: Array<{ url: string; init?: RequestInit }> = [];
  let queue = [
    { id: 'one', method: 'POST', url: 'http://one.test/', headers: { 'Content-Type': ['text/plain'] }, body: 'before', bodyEditable: true, bodyTruncated: false },
    { id: 'two', method: 'GET', url: 'http://two.test/', headers: {}, body: '', bodyEditable: true, bodyTruncated: false },
  ];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/status')) return jsonResponse(statusFixture);
    if (url.endsWith('/api/history')) return jsonResponse([]);
    if (url.endsWith('/api/intercept/queue')) return jsonResponse(queue);
	if (url.endsWith('/api/intercept/config')) return jsonResponse({ enabled: true, rules: [] });
    if (url.includes('/api/intercept/')) {
      actions.push({ url, init });
      queue = queue.filter((item) => !url.includes(`/${item.id}/`));
      return new Response(null, { status: 204 });
    }
    return new Response('{}', { status: 404 });
  }));

  render(<App />);
  const body = await screen.findByLabelText('Intercept body one');
  fireEvent.change(body, { target: { value: 'after' } });
  fireEvent.click(screen.getByRole('button', { name: 'Forward one' }));
  await waitFor(() => expect(actions).toHaveLength(1));
  expect(actions[0].url).toBe('/api/intercept/one/forward');
  expect(JSON.parse(String(actions[0].init?.body))).toMatchObject({ id: 'one', body: 'after' });

  fireEvent.click(screen.getByRole('button', { name: 'Drop two' }));
  await waitFor(() => expect(actions).toHaveLength(2));
  expect(actions[1].url).toBe('/api/intercept/two/drop');
});

test('opens Target and routes endpoint and rebuild events without reloading History', async () => {
  const fetchMock = fullAppTargetFetchFixture();
  vi.stubGlobal('fetch', fetchMock);
  const socket = recordedSockets[0];

  render(<App />);
  fireEvent.click(screen.getByRole('button', { name: 'Target' }));
  expect(await screen.findByRole('heading', { name: 'Site Map' })).toBeInTheDocument();
  const historyLoads = countFetches(fetchMock, '/api/history');
  const treeLoads = countFetches(fetchMock, '/api/target/tree');
  const rebuildLoads = countFetches(fetchMock, '/api/target/rebuild');

  socket.emit('target.endpoint.updated');
  await waitFor(() => expect(countFetches(fetchMock, '/api/target/tree')).toBe(treeLoads + 1));
  expect(countFetches(fetchMock, '/api/history')).toBe(historyLoads);
  expect(countFetches(fetchMock, '/api/target/rebuild')).toBe(rebuildLoads);

  socket.emit('target.rebuild.progress');
  await waitFor(() => expect(countFetches(fetchMock, '/api/target/rebuild')).toBe(rebuildLoads + 1));
  expect(countFetches(fetchMock, '/api/history')).toBe(historyLoads);
  expect(countFetches(fetchMock, '/api/target/tree')).toBe(treeLoads + 1);
});

test('opens a Target request in the existing History inspector', async () => {
  vi.stubGlobal('fetch', fullAppTargetFetchFixture());
  render(<App />);
  fireEvent.click(screen.getByRole('button', { name: 'Target' }));
  fireEvent.click(await screen.findByRole('treeitem', { name: /^GET \/from-target/ }));
  fireEvent.click(await screen.findByRole('button', { name: 'Open 43 in History' }));

  expect(screen.queryByRole('main', { name: 'Target workspace' })).not.toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole('button', { name: /Select GET outside\.test\/public/ })).toHaveAttribute('aria-pressed', 'true'));
  fireEvent.click(screen.getByRole('tab', { name: 'Body' }));
  expect(await screen.findByText(/response 43/)).toBeInTheDocument();
});

test('sends a Target request through the existing Repeater flow', async () => {
  vi.stubGlobal('fetch', fullAppTargetFetchFixture());
  render(<App />);
  fireEvent.click(screen.getByRole('button', { name: 'Target' }));
  fireEvent.click(await screen.findByRole('treeitem', { name: /^GET \/from-target/ }));
  fireEvent.click(await screen.findByRole('button', { name: 'Send 43 to Repeater' }));

  expect(screen.queryByRole('main', { name: 'Target workspace' })).not.toBeInTheDocument();
  await waitFor(() => expect(screen.getByLabelText('URL')).toHaveValue('https://outside.test/public?token=needle'));
});

test('filters History by text and scope and renders valid sibling row controls', async () => {
  vi.stubGlobal('fetch', fullAppTargetFetchFixture());
  render(<App />);
  await screen.findByRole('button', { name: /Select POST target\.test\/from-target/ });

  fireEvent.change(screen.getByPlaceholderText('Filter requests'), { target: { value: 'needle' } });
  expect(screen.queryByRole('button', { name: /Select POST target\.test/ })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /Select GET outside\.test/ })).toBeInTheDocument();
  fireEvent.change(screen.getByPlaceholderText('Filter requests'), { target: { value: '' } });

  const scopeFilter = screen.getByRole('combobox', { name: 'History scope' });
  fireEvent.change(scopeFilter, { target: { value: 'in' } });
  expect(screen.getByText('In scope')).toBeInTheDocument();
  expect(screen.queryByText('Out of scope')).not.toBeInTheDocument();
  fireEvent.change(scopeFilter, { target: { value: 'out' } });
  expect(screen.getByText('Out of scope')).toBeInTheDocument();
  expect(screen.queryByText('In scope')).not.toBeInTheDocument();
  fireEvent.change(scopeFilter, { target: { value: 'all' } });

  for (const row of screen.getAllByRole('row').slice(1)) {
    expect(row.querySelector('button button')).toBeNull();
    expect(within(row).getAllByRole('button')).toHaveLength(2);
  }
});

test('adds a normalized origin to scope with the latest version and effective HTTPS port', async () => {
  const fetchMock = historyAddToScopeFetchFixture();
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add Example.TEST to scope' }));

  await waitFor(() => expect(lastScopeUpdate()).toBeDefined());
  expect(lastScopeUpdate()).toEqual({
    version: 12,
    rules: [expect.objectContaining({ enabled: true, action: 'include', scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '/' })],
  });
  expect(countFetches(fetchMock, '/api/scope/rules', 'PUT')).toBe(1);
});

test('adds a bracketed IPv6 origin with its explicit port', async () => {
  const item = { ...historyFixture(52, '[2001:DB8::1]:8443', '/admin'), scheme: 'https', inScope: false } as HistoryItem;
  vi.stubGlobal('fetch', historyAddToScopeFetchFixture({ item }));
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add [2001:DB8::1]:8443 to scope' }));

  await waitFor(() => expect(lastScopeUpdate()).toBeDefined());
  expect(lastScopeUpdate()?.rules).toEqual([
    expect.objectContaining({ scheme: 'https', hostPattern: '2001:db8::1', port: 8443, pathPrefix: '/' }),
  ]);
});

test('does not add an equivalent enabled include rule', async () => {
  const scope: ScopeState = { version: 21, rules: [{ id: 9, enabled: true, action: 'include', scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '/' }] };
  const fetchMock = historyAddToScopeFetchFixture({ scope });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add Example.TEST to scope' }));

  await waitFor(() => expect(countFetches(fetchMock, '/api/scope/rules')).toBe(1));
  expect(countFetches(fetchMock, '/api/scope/rules', 'PUT')).toBe(0);
  expect(lastScopeUpdate()).toBeUndefined();
});

test('reloads scope once after a 409, shows an error, and does not retry the write', async () => {
  const fetchMock = historyAddToScopeFetchFixture({ conflict: true });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add Example.TEST to scope' }));

  expect(await screen.findByRole('status')).toHaveTextContent(/scope changed.*reload/i);
  expect(countFetches(fetchMock, '/api/scope/rules')).toBe(2);
  expect(countFetches(fetchMock, '/api/scope/rules', 'PUT')).toBe(1);
});

const statusFixture = {
  apiAddr: '127.0.0.1:9080', proxyAddr: '127.0.0.1:8080', caFingerprint: '', caTrust: 'unavailable' as const, httpsInterception: false,
};

const historyFixture = (id: number, host: string, path: string) => ({
  id, method: 'POST', scheme: 'https', host, path, query: '', status: 200, mimeType: 'text/plain', requestSize: 12,
  responseSize: 13, durationMs: 125, startedAt: '2026-08-19T10:24:00Z', intercepted: false, error: false,
});

const exchangeFixture = (id: number, host: string, path: string, responseBody: string) => ({
  ...historyFixture(id, host, path), errorMessage: '', requestTruncated: false, responseTruncated: false,
  request: { headers: { 'Content-Type': ['text/plain'] }, body: 'request text', raw: '', textSafe: true, truncated: false },
  response: { headers: { 'Content-Type': ['text/plain'] }, body: responseBody, raw: '', textSafe: true, truncated: false },
  tags: [], note: '',
});

const jsonResponse = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } });
