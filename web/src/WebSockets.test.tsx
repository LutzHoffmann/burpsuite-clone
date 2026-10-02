import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, expect, test, vi } from 'vitest';
import { App } from './App';

const events = vi.hoisted(() => ({ receive: (_event: { type: string }) => {} }));
vi.mock('./api/events', () => ({ connectEvents: (receive: typeof events.receive) => { events.receive = receive; return () => undefined; } }));
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
const json = (value: unknown) => new Response(JSON.stringify(value));
const connection = (id: number) => ({ id, url: `wss://capture.test/${id}`, inScope: false, state: 'open', openedAt: '2026-09-14T10:00:00Z', closedAt: null, gaps: 2, captureIncomplete: true });
const message = (id: number, connectionId = 1) => ({ id, connectionId, sequence: id, direction: 'client-to-server', observedAt: '2026-09-14T10:00:01Z', type: 'text', size: 1024, truncated: true, complete: false, encoding: 'opaque' });

function fixture() {
 const state = { connections: [connection(2), connection(1)], messages: [message(101)], override: null as null | ((url: URL) => Promise<Response> | undefined) };
 const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = new URL(String(input), 'http://localhost');
  const override = state.override?.(url);
  if (override) return override;
  const path = url.pathname;
  if (path === '/api/status') return json({ apiAddr: '', proxyAddr: '', caFingerprint: '', caTrust: 'unavailable', httpsInterception: false });
  if (path === '/api/storage') return json({ paused: true, limitBytes: 100, usedBytes: 100, skippedRecords: 1 });
  if (path === '/api/history/page') return json({ items: [], nextBeforeId: 0, snapshotId: 0 });
  if (path === '/api/intercept/config') return json({ enabled: false, rules: [] });
  if (path.endsWith('/queue') || path.endsWith('/response-queue')) return json([]);
  if (path === '/api/websockets') {
   const snapshotId = Number(url.searchParams.get('snapshotId')) || state.connections[0].id;
   return json({ items: url.searchParams.has('beforeId') ? [connection(0)] : state.connections.filter((item) => item.id <= snapshotId), nextBeforeId: url.searchParams.has('beforeId') ? 0 : 1, snapshotId });
  }
  const detail = path.match(/^\/api\/websockets\/(\d+)$/);
  if (detail) return json(connection(Number(detail[1])));
  const messages = path.match(/^\/api\/websockets\/(\d+)\/messages$/);
  if (messages) {
   const snapshotId = Number(url.searchParams.get('snapshotId')) || state.messages[0].id;
   return json({ items: url.searchParams.has('beforeId') ? [message(1, Number(messages[1]))] : state.messages.filter((item) => item.id <= snapshotId).map((m) => ({ ...m, connectionId: Number(messages[1]) })), nextBeforeId: url.searchParams.has('beforeId') ? 0 : 101, snapshotId });
  }
  const payload = path.match(/^\/api\/websockets\/(\d+)\/messages\/(\d+)$/);
  if (payload) return json({ ...message(Number(payload[2]), Number(payload[1])), payload: '<img src=x onerror=alert(1)>', payloadFormat: 'text' });
  throw new Error(`Unexpected request ${input}`);
 });
 vi.stubGlobal('fetch', fetchMock);
 return { state, fetchMock };
}
async function open() {
 fireEvent.click(screen.getByRole('button', { name: 'WebSockets' }));
 await act(async () => {});
}
const pageButton = (kind: 'Connections' | 'Messages', name: string | RegExp) => within(screen.getByRole('navigation', { name: `${kind} pagination` })).getByRole('button', { name });

test('workspace loads connections, messages and safe detail with capture labels', async () => {
 fixture(); render(<App />); await open();
 expect(screen.getByText(/handshake.*bypass/i)).toBeInTheDocument();
 expect(screen.getByRole('alert')).toHaveTextContent(/Capture storage is paused/);
 fireEvent.click(screen.getByRole('button', { name: 'Select connection 1' }));
 await act(async () => {});
 expect(screen.getAllByText(/Out of scope/).length).toBeGreaterThan(0);
 expect(screen.getByLabelText('Connection detail')).toHaveTextContent(/Capture gaps: 2/);
 fireEvent.click(screen.getByRole('button', { name: 'Select message 101' }));
 await act(async () => {});
 const detail = screen.getByLabelText('WebSocket message detail');
 expect(detail).toHaveTextContent('opaque');
 expect(detail).toHaveTextContent(/Truncated/);
 expect(detail).toHaveTextContent(/Incomplete/);
 expect(detail).toHaveTextContent('<img src=x onerror=alert(1)>');
 expect(detail.querySelector('img')).toBeNull();
});

test('polling keeps older message and connection pages stable and offers refresh', async () => {
 const { state, fetchMock } = fixture(); vi.useFakeTimers(); render(<App />); await open();
 fireEvent.click(screen.getByRole('button', { name: 'Select connection 1' })); await act(async () => {});
 fireEvent.click(pageButton('Messages', 'Next')); await act(async () => {});
 expect(screen.getByRole('button', { name: 'Select message 1' })).toBeInTheDocument();
 state.messages = [message(102), message(101)];
 await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
 expect(screen.queryByRole('button', { name: 'Select message 102' })).not.toBeInTheDocument();
 expect(pageButton('Messages', /Refresh/)).toHaveTextContent(/New traffic/);
 expect(fetchMock.mock.calls.some(([input]) => String(input).includes('beforeId=101&snapshotId=101'))).toBe(true);
 fireEvent.click(pageButton('Messages', 'Previous')); await act(async () => {});
 expect(screen.getByRole('button', { name: 'Select message 101' })).toBeInTheDocument();
 fireEvent.click(pageButton('Connections', 'Next')); await act(async () => {});
 state.connections.unshift(connection(3));
 await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
 expect(screen.queryByRole('button', { name: 'Select connection 3' })).not.toBeInTheDocument();
 expect(pageButton('Connections', /Refresh/)).toHaveTextContent(/New traffic/);
});

test('capture limit warning survives switching to the WebSockets workspace', async () => {
 fixture(); render(<App />); await act(async () => {});
 act(() => events.receive({ type: 'websocket.capture.limited' }));
 await open();
 expect(screen.getByText(/Some WebSocket connections were not recorded/)).toBeInTheDocument();
});

test('eligible history transfer opens editor and guards navigation without changing browser traffic', async () => {
 const { state, fetchMock } = fixture();
 state.override = (url) => {
  if (url.pathname === '/api/websockets/1/messages/101') return Promise.resolve(json({ ...message(101), encoding: 'identity', complete: true, truncated: false, payload: 'hello', payloadFormat: 'text' }));
  if (url.pathname.endsWith('/draft')) return Promise.resolve(json({ url: 'wss://capture.test/1', type: 'text', payload: 'hello', payloadFormat: 'text', headers: {}, subprotocols: [] }));
 };
 render(<App />); await open();
 fireEvent.click(screen.getByRole('button', { name: 'Select connection 1' })); await act(async () => {});
 fireEvent.click(screen.getByRole('button', { name: 'Select message 101' })); await act(async () => {});
 fireEvent.click(screen.getByRole('button', { name: 'Use in WebSocket Repeater' })); await act(async () => {});
 expect(screen.getByLabelText('Message payload')).toHaveValue('hello');
 expect(screen.getByLabelText('Handshake headers')).toHaveValue('');
 expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/websocket-repeater/send'))).toBe(false);
 const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
 fireEvent.click(screen.getByRole('button', { name: 'Traffic' }));
 expect(screen.getByLabelText('Message payload')).toHaveValue('hello');
 expect(confirm).toHaveBeenCalled();
 confirm.mockReturnValue(true);
 fireEvent.click(screen.getByRole('button', { name: 'Traffic' }));
 expect(screen.queryByLabelText('WebSocket repeater')).not.toBeInTheDocument();
});

test.each([200, 500])('late message loads cannot replace another connection (%s)', async (status) => {
 const { state } = fixture(); render(<App />); await open();
 let release!: (response: Response) => void;
 state.override = (url) => url.pathname === '/api/websockets/1/messages' ? new Promise((resolve) => { release = resolve; }) : undefined;
 fireEvent.click(screen.getByRole('button', { name: 'Select connection 1' })); await act(async () => {});
 fireEvent.click(screen.getByRole('button', { name: 'Select connection 2' })); await act(async () => {});
 await act(async () => release(status === 200 ? json({ items: [message(999)], nextBeforeId: 0, snapshotId: 999 }) : new Response('stale failure', { status })));
 expect(screen.queryByRole('button', { name: 'Select message 999' })).not.toBeInTheDocument();
 expect(screen.queryByText(/stale failure/)).not.toBeInTheDocument();
 expect(screen.getByRole('button', { name: 'Select message 101' })).toBeInTheDocument();
});
