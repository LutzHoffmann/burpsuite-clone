import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { App } from './App';
import { Inspector } from './components/Inspector';

beforeEach(() => {
	vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)));
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
  fireEvent.doubleClick(rowHost.closest('button')!);
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
