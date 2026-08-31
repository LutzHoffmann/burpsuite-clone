import { act } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { App } from './App';
import { Inspector } from './components/Inspector';
import type { HistoryItem, ScopeState, StatusDTO, TargetTreeNode } from './types';

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

function requestDetails(input: RequestInfo | URL, init?: RequestInit) {
  return { path: new URL(String(input), 'http://localhost').pathname, method: init?.method ?? 'GET' };
}

function strictBaseResponse(input: RequestInfo | URL, init: RequestInit | undefined, history: HistoryItem[], status: StatusDTO = statusFixture) {
  const { path, method } = requestDetails(input, init);
  if (method !== 'GET') return null;
  if (path === '/api/status') return jsonResponse(status);
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

function initialAppFetchFixture(history: HistoryItem[] = [], status: StatusDTO = statusFixture): ReturnType<typeof vi.fn> {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const response = strictBaseResponse(input, init, history, status);
    if (response) return response;
    const { path, method } = requestDetails(input, init);
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
}

function fullAppTargetFetchFixture(): ReturnType<typeof vi.fn> {
  const history = [
    { ...historyFixture(42, 'target.test', '/from-target'), query: 'q=1', inScope: true },
    { ...historyFixture(43, 'outside.test', '/public'), method: 'GET', query: 'token=needle', inScope: false },
  ];
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { path, method } = requestDetails(input, init);
    const base = strictBaseResponse(input, init, history);
    if (base) return base;
    if (path === '/api/scope/rules' && method === 'GET') return jsonResponse(scopeFixture);
    if (path === '/api/target/tree' && method === 'GET') return jsonResponse(targetTreeFixture);
    if (path === '/api/target/rebuild' && method === 'GET') {
      return jsonResponse({ id: 3, scopeVersion: 12, activeScopeVersion: 12, status: 'building', processed: 2, total: 5, error: '' });
    }
    if (path === '/api/target/endpoints/7' && method === 'GET') return jsonResponse({
      id: 7, scheme: 'https', host: 'target.test', port: 443, path: '/from-target', method: 'GET', inScope: true,
      firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 1, statuses: [200],
      requestMimes: ['text/plain'], responseMimes: ['text/plain'], parseDiagnostics: [], errorSeen: false, latestExchangeId: 43,
    });
    if (path === '/api/target/endpoints/7/requests' && method === 'GET') return jsonResponse([{ exchangeId: 43, startedAt: '2026-08-20T10:05:00Z', status: 200, error: false }]);
    if (path === '/api/target/endpoints/7/parameters' && method === 'GET') return jsonResponse([]);
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
}

function countFetches(fetchMock: ReturnType<typeof vi.fn>, path: string, method = 'GET') {
  return fetchMock.mock.calls.filter(([input, init]) => {
    const requestPath = new URL(String(input), 'http://localhost').pathname;
    return requestPath === path && (init?.method ?? 'GET') === method;
  }).length;
}

function historyAddToScopeFetchFixture(options: { item?: HistoryItem; scope?: ScopeState; conflict?: boolean } = {}): ReturnType<typeof vi.fn> {
  const item = options.item ?? { ...historyFixture(51, 'Example.TEST', '/admin'), scheme: 'https', inScope: false };
  const history = [item];
  let scopeReads = 0;
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { path, method } = requestDetails(input, init);
    const base = strictBaseResponse(input, init, history);
    if (base) return base;
    if (path === '/api/scope/rules' && method === 'GET') {
      scopeReads += 1;
      return jsonResponse({ ...(options.scope ?? scopeFixture), version: (options.scope ?? scopeFixture).version + (scopeReads > 1 ? 1 : 0) });
    }
    if (path === '/api/scope/rules' && method === 'PUT') {
      scopeUpdates.push(JSON.parse(String(init?.body)));
      if (options.conflict) return new Response('{}', { status: 409, statusText: 'Conflict' });
      return jsonResponse({ version: (options.scope ?? scopeFixture).version + 1, rules: scopeUpdates.at(-1)?.rules ?? [] });
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
}

function lastScopeUpdate() {
  return scopeUpdates.at(-1);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((nextResolve, nextReject) => { resolve = nextResolve; reject = nextReject; });
  return { promise, resolve, reject };
}

async function renderSettledApp() {
  render(<App />);
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

beforeEach(() => {
  recordedSockets = [];
  scopeUpdates = [];
  vi.stubGlobal('fetch', initialAppFetchFixture());
  installFakeEventSocket();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test('default App fetch fixture rejects unexpected requests', async () => {
  await expect(fetch('/api/unexpected')).rejects.toThrow('Unexpected request: GET /api/unexpected');
});

test('loads status from api', async () => {
  vi.stubGlobal('fetch', initialAppFetchFixture([], {
    apiAddr: '127.0.0.1:9080',
    proxyAddr: '127.0.0.1:18080',
    caFingerprint: 'AA:BB',
    caTrust: 'manual',
    httpsInterception: true,
  }));
  render(<App />);
  expect(await screen.findByText('127.0.0.1:18080')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  expect(screen.getByText('Manual setup required')).toBeInTheDocument();
  expect(screen.getByText(/install and trust the local ca certificate/i)).toBeInTheDocument();
});

test('renders operator shell status', async () => {
  await renderSettledApp();
  expect(screen.getByText('Proxy')).toBeInTheDocument();
  expect(screen.getByText('History')).toBeInTheDocument();
  expect(screen.getByText('Repeater')).toBeInTheDocument();
});

test('shows history columns and inspector tabs', async () => {
  await renderSettledApp();
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

test('shows intercept and repeater controls', async () => {
  await renderSettledApp();
  expect(screen.getByText('Intercept Queue')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Forward' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Drop' })).toBeInTheDocument();
  expect(screen.getByText('Request Editor')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
});

test('keeps the intercept queue outside the hidden utilities panel', async () => {
  await renderSettledApp();

  expect(screen.getByLabelText('Intercept Queue').closest('.utility-panel')).toBeNull();
});

test('selects history rows and loads request and response detail from api', async () => {
  const history = [historyFixture(1, 'first.test', '/one'), historyFixture(2, 'second.test', '/two')];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { path, method } = requestDetails(input, init);
    if (path === '/api/history/1' && method === 'GET') return jsonResponse(exchangeFixture(1, 'first.test', '/one', 'first response'));
    if (path === '/api/history/2' && method === 'GET') return jsonResponse(exchangeFixture(2, 'second.test', '/two', 'second response'));
    const response = strictBaseResponse(input, init, history);
    if (response) return response;
    throw new Error(`Unexpected request: ${method} ${path}`);
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
  const history = [historyFixture(7, 'replay.test', '/submit')];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { path, method } = requestDetails(input, init);
    if (path === '/api/history/7' && method === 'GET') return jsonResponse(exchangeFixture(7, 'replay.test', '/submit', 'captured'));
    if (path === '/api/repeater/sessions/default/send' && method === 'POST') {
      expect(init?.method).toBe('POST');
      expect(JSON.parse(String(init?.body))).toMatchObject({ method: 'POST', url: 'https://replay.test/submit', body: 'request text' });
      return jsonResponse({ status: 202, headers: { 'Content-Type': ['text/plain'] }, body: 'real response', durationMs: 9, size: 13, truncated: false, contentType: 'text/plain' });
    }
    const response = strictBaseResponse(input, init, history);
    if (response) return response;
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<App />);
  const rowHost = await screen.findByText('replay.test');
  fireEvent.doubleClick(rowHost.closest('tr')!.querySelector('button')!);
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
    const { path, method } = requestDetails(input, init);
    if (path === '/api/status' && method === 'GET') return jsonResponse(statusFixture);
    if (path === '/api/history' && method === 'GET') return jsonResponse([]);
    if (path === '/api/intercept/queue' && method === 'GET') return jsonResponse(queue);
    if (path === '/api/intercept/config' && method === 'GET') return jsonResponse({ enabled: true, rules: [] });
    if ((path === '/api/intercept/one/forward' || path === '/api/intercept/two/drop') && method === 'POST') {
      actions.push({ url: path, init });
      queue = queue.filter((item) => !path.includes(`/${item.id}/`));
      return new Response(null, { status: 204 });
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
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
  expect(screen.getByRole('region', { name: 'Target workspace' })).toBeInTheDocument();
  expect(screen.queryByRole('main', { name: 'Target workspace' })).not.toBeInTheDocument();
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

test.each(['target.rebuild.progress', 'target.endpoint.updated'])('opens Target with a complete load after hidden %s', async (eventType) => {
  const fetchMock = fullAppTargetFetchFixture();
  vi.stubGlobal('fetch', fetchMock);
  const socket = recordedSockets[0];

  render(<App />);
  await screen.findByRole('button', { name: /Select POST target\.test\/from-target/ });
  socket.emit(eventType);
  expect(countFetches(fetchMock, '/api/history')).toBe(1);

  fireEvent.click(screen.getByRole('button', { name: 'Target' }));
  expect(await screen.findByRole('treeitem', { name: /^GET \/from-target/ })).toBeInTheDocument();
  expect(countFetches(fetchMock, '/api/scope/rules')).toBe(1);
  expect(countFetches(fetchMock, '/api/target/tree')).toBe(1);
  expect(countFetches(fetchMock, '/api/target/rebuild')).toBe(1);
  expect(countFetches(fetchMock, '/api/history')).toBe(1);
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
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('button', { name: /Select POST target\.test\/from-target/ });
  await screen.findByText('POST https://target.test/from-target');

  const query = screen.getByPlaceholderText('Filter requests');
  await user.type(query, 'needle');
  expect(screen.queryByRole('button', { name: /Select POST target\.test/ })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /Select GET outside\.test/ })).toBeInTheDocument();
  await user.clear(query);

  const scopeFilter = screen.getByRole('combobox', { name: 'History scope' });
  await user.selectOptions(scopeFilter, 'in');
  expect(screen.getByText('In scope')).toBeInTheDocument();
  expect(screen.queryByText('Out of scope')).not.toBeInTheDocument();
  await user.selectOptions(scopeFilter, 'out');
  expect(screen.getByText('Out of scope')).toBeInTheDocument();
  expect(screen.queryByText('In scope')).not.toBeInTheDocument();
  await user.selectOptions(scopeFilter, 'all');

  expect(screen.getByRole('table', { name: 'Request history' }).tagName).toBe('TABLE');
  expect(screen.getAllByRole('columnheader')).toHaveLength(10);
  for (const row of screen.getAllByRole('row').slice(1)) {
    expect(row.querySelector('button button')).toBeNull();
    expect(within(row).getAllByRole('button')).toHaveLength(2);
  }
});

test('disables every Add to scope control until the pending scope update finishes', async () => {
  const scopeRead = deferred<Response>();
  const history = [
    { ...historyFixture(71, 'first.test', '/one'), inScope: false },
    { ...historyFixture(72, 'second.test', '/two'), inScope: false },
  ];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const base = strictBaseResponse(input, init, history);
    if (base) return base;
    const { path, method } = requestDetails(input, init);
    if (path === '/api/scope/rules' && method === 'GET') return scopeRead.promise;
    if (path === '/api/scope/rules' && method === 'PUT') {
      scopeUpdates.push(JSON.parse(String(init?.body)));
      return jsonResponse({ version: 13, rules: scopeUpdates.at(-1)?.rules ?? [] });
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  const first = await screen.findByRole('button', { name: 'Add first.test to scope' });
  const second = screen.getByRole('button', { name: 'Add second.test to scope' });
  fireEvent.click(first);
  await waitFor(() => expect(countFetches(fetchMock, '/api/scope/rules')).toBe(1));
  expect(first).toBeDisabled();
  expect(second).toBeDisabled();
  fireEvent.click(second);
  scopeRead.resolve(jsonResponse(scopeFixture));

  await waitFor(() => expect(countFetches(fetchMock, '/api/scope/rules', 'PUT')).toBe(1));
  expect(lastScopeUpdate()?.rules).toEqual([expect.objectContaining({ hostPattern: 'first.test' })]);
  expect(countFetches(fetchMock, '/api/scope/rules')).toBe(1);
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
  const item = { ...historyFixture(52, '[2001:DB8::1]:8443', '/admin'), scheme: 'https', inScope: false };
  vi.stubGlobal('fetch', historyAddToScopeFetchFixture({ item }));
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add [2001:DB8::1]:8443 to scope' }));

  await waitFor(() => expect(lastScopeUpdate()).toBeDefined());
  expect(lastScopeUpdate()?.rules).toEqual([
    expect.objectContaining({ scheme: 'https', hostPattern: '2001:db8::1', port: 8443, pathPrefix: '/' }),
  ]);
});

test.each([
  ['dotted IPv4-mapped IPv6 with the effective HTTPS port', '[::ffff:192.0.2.1]', 443],
  ['hex IPv4-mapped IPv6 with an explicit port', '[::ffff:c000:0201]:8443', 8443],
])('normalizes %s like Go net.IP.String', async (_name, host, expectedPort) => {
  const item = { ...historyFixture(53, host, '/admin'), scheme: 'https', inScope: false };
  vi.stubGlobal('fetch', historyAddToScopeFetchFixture({ item }));
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: `Add ${host} to scope` }));

  await waitFor(() => expect(lastScopeUpdate()).toBeDefined());
  expect(lastScopeUpdate()?.rules).toEqual([
    expect.objectContaining({ scheme: 'https', hostPattern: '192.0.2.1', port: expectedPort, pathPrefix: '/' }),
  ]);
});

test.each([
  ['numeric DNS-like host', '127.1', 'http', '127.1', 80],
  ['Unicode DNS host', 'BÜCHER.Example', 'https', 'xn--bcher-kva.example', 443],
  ['expanded bare IPv6 host', '2001:0DB8:0000:0000:0000:0000:0000:0001', 'https', '2001:db8::1', 443],
  ['minimum explicit port', 'ports.test:1', 'http', 'ports.test', 1],
  ['maximum explicit port', 'ports.test:65535', 'https', 'ports.test', 65535],
])('normalizes %s like the backend', async (_name, host, scheme, expectedHost, expectedPort) => {
  const item = { ...historyFixture(60, host, '/path'), scheme };
  vi.stubGlobal('fetch', historyAddToScopeFetchFixture({ item }));
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: `Add ${host} to scope` }));

  await waitFor(() => expect(lastScopeUpdate()).toBeDefined());
  expect(lastScopeUpdate()?.rules).toEqual([
    expect.objectContaining({ scheme, hostPattern: expectedHost, port: expectedPort, pathPrefix: '/' }),
  ]);
});

test.each([
  'user@example.test',
  'example.test/path',
  'example.test?query',
  'example.test#fragment',
  '[2001:db8::1',
  '[2001:db8::1]extra',
  'example.test:',
  'example.test:0',
  'example.test:65536',
  'example.test:not-a-port',
])('rejects malformed scope authority %s before fetching scope', async (host) => {
  const fetchMock = historyAddToScopeFetchFixture({ item: { ...historyFixture(61, host, '/path'), scheme: 'https' } });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: `Add ${host} to scope` }));

  expect(await screen.findByRole('status')).toHaveTextContent(/Add to scope failed/);
  expect(countFetches(fetchMock, '/api/scope/rules')).toBe(0);
  expect(countFetches(fetchMock, '/api/scope/rules', 'PUT')).toBe(0);
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

test.each([
  ['empty root path', 'example.test', 'https', { scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '' }],
  ['Unicode and IDNA hosts', 'xn--bcher-kva.example', 'https', { scheme: 'https', hostPattern: 'BÜCHER.Example', port: 443, pathPrefix: '/' }],
  ['expanded and compressed IPv6', '[2001:db8::1]', 'https', { scheme: 'https', hostPattern: '2001:0DB8:0000:0000:0000:0000:0000:0001', port: 443, pathPrefix: '/' }],
  ['case-normalized scheme and host', 'example.test', 'https', { scheme: 'HTTPS', hostPattern: 'EXAMPLE.TEST', port: 443, pathPrefix: '/' }],
])('deduplicates semantically equivalent include rules with %s', async (_name, host, scheme, equivalent) => {
  const scope = { version: 30, rules: [{ id: 11, enabled: true, action: 'include', ...equivalent }] } as ScopeState;
  const fetchMock = historyAddToScopeFetchFixture({ item: { ...historyFixture(62, host, '/path'), scheme }, scope });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: `Add ${host} to scope` }));

  await waitFor(() => expect(countFetches(fetchMock, '/api/scope/rules')).toBe(1));
  expect(countFetches(fetchMock, '/api/scope/rules', 'PUT')).toBe(0);
  expect(lastScopeUpdate()).toBeUndefined();
});

test('deduplicates an IPv4-mapped IPv6 origin against an existing IPv4 include rule', async () => {
  const host = '[::ffff:192.0.2.1]:8443';
  const scope: ScopeState = { version: 31, rules: [
    { id: 12, enabled: true, action: 'include', scheme: 'https', hostPattern: '192.0.2.1', port: 8443, pathPrefix: '/' },
  ] };
  const fetchMock = historyAddToScopeFetchFixture({ item: { ...historyFixture(63, host, '/path'), scheme: 'https' }, scope });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: `Add ${host} to scope` }));

  await waitFor(() => expect(countFetches(fetchMock, '/api/scope/rules')).toBe(1));
  expect(countFetches(fetchMock, '/api/scope/rules', 'PUT')).toBe(0);
  expect(lastScopeUpdate()).toBeUndefined();
});

test('does not deduplicate disabled, exclude, wildcard, or broader scheme and port rules', async () => {
  const scope: ScopeState = { version: 31, rules: [
    { id: 1, enabled: false, action: 'include', scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '/' },
    { id: 2, enabled: true, action: 'exclude', scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '/' },
    { id: 3, enabled: true, action: 'include', scheme: 'https', hostPattern: '*.example.test', port: 443, pathPrefix: '/' },
    { id: 4, enabled: true, action: 'include', scheme: '', hostPattern: 'example.test', port: 443, pathPrefix: '/' },
    { id: 5, enabled: true, action: 'include', scheme: 'https', hostPattern: 'example.test', port: 0, pathPrefix: '/' },
  ] };
  const fetchMock = historyAddToScopeFetchFixture({ scope });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add Example.TEST to scope' }));

  await waitFor(() => expect(lastScopeUpdate()).toBeDefined());
  expect(lastScopeUpdate()?.rules).toHaveLength(6);
  expect(lastScopeUpdate()?.rules.at(-1)).toEqual(expect.objectContaining({ scheme: 'https', hostPattern: 'example.test', port: 443 }));
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
  inScope: false, scopeVersion: 12, scopeRuleId: null,
});

const exchangeFixture = (id: number, host: string, path: string, responseBody: string) => ({
  ...historyFixture(id, host, path), errorMessage: '', requestTruncated: false, responseTruncated: false,
  request: { headers: { 'Content-Type': ['text/plain'] }, body: 'request text', raw: '', textSafe: true, truncated: false },
  response: { headers: { 'Content-Type': ['text/plain'] }, body: responseBody, raw: '', textSafe: true, truncated: false },
  tags: [], note: '',
});

const jsonResponse = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } });
