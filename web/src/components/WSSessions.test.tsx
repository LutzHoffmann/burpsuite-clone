import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { WSRepeater } from './WSRepeater';

const id = 'a'.repeat(32);
const base = { id, url: 'ws://test.example/socket', state: 'connected', reason: '', subprotocol: 'chat', messages: [] as ReturnType<typeof message>[], oldestSequence: 0, latestSequence: 0, nextSequence: 0, droppedMessages: 0 };
function message(sequence: number, payload = `message-${sequence}`) {
  return { sequence, direction: 'server-to-client', timestamp: '2026-09-16T12:00:00Z', type: 'text', payload, payloadFormat: 'text', size: payload.length, truncated: false, complete: true };
}
const response = (value: unknown, status = 200) => new Response(status === 204 ? null : JSON.stringify(value), { status });
const fetcher = vi.fn<typeof fetch>();
const change = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });
const click = async (name: string) => { await act(async () => { fireEvent.click(screen.getByRole('button', { name })); }); };
async function start() {
  await click('Session'); change('WebSocket URL', base.url);
  change('Handshake headers', 'Authorization: Bearer private'); change('Subprotocols', 'chat');
  await click('Connect');
}
beforeEach(() => {
  vi.useFakeTimers(); vi.stubGlobal('fetch', fetcher);
  fetcher.mockImplementation(async (_url, init) => init?.method === 'DELETE' ? response(null, 204) : response(base));
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); fetcher.mockReset(); });

test('connect only, repeated manual sends, unsolicited polling and close use the contract', async () => {
  render(<WSRepeater />);
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeInTheDocument();
  await start();
  expect(JSON.parse(fetcher.mock.calls[0][1]!.body as string)).toEqual({ url: base.url, headers: { Authorization: ['Bearer private'] }, subprotocols: ['chat'] });
  for (const label of ['WebSocket URL', 'Handshake headers', 'Subprotocols']) expect(screen.getByLabelText(label)).toBeDisabled();
  expect(screen.getByLabelText('Message payload')).toBeEnabled();
  change('Message payload', 'first'); await click('Send WebSocket message');
  change('Message payload', 'second'); await click('Send WebSocket message');
  const sends = fetcher.mock.calls.filter(([url]) => String(url).endsWith('/send'));
  expect(sends.map(([, init]) => JSON.parse(init!.body as string))).toEqual(['first', 'second'].map(payload => ({ type: 'text', payload, payloadFormat: 'text' })));
  fetcher.mockResolvedValueOnce(response({ ...base, messages: [message(1, '<img src=x>')], oldestSequence: 1, latestSequence: 1, nextSequence: 1 }));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(fetcher).toHaveBeenCalledWith(`/api/websocket-repeater/sessions/${id}?afterSequence=0`, expect.objectContaining({ cache: 'no-store' }));
  expect(screen.getByLabelText('Session messages')).toHaveTextContent('<img src=x>');
  expect(screen.getByLabelText('Session messages').querySelector('img')).toBeNull();
  fetcher.mockResolvedValueOnce(response({ ...base, state: 'closed', reason: 'operator_closed' }));
  await click('Close session');
  expect(fetcher.mock.calls.at(-1)).toEqual([`/api/websocket-repeater/sessions/${id}/close`, expect.objectContaining({ method: 'POST' })]);
  expect(fetcher.mock.calls.at(-1)?.[1]?.body).toBeUndefined();
  expect(screen.getByText(/operator_closed/)).toBeInTheDocument();
});

test('late poll cannot reopen a closed session', async () => {
  render(<WSRepeater />); await start();
  let resolve!: (value: Response) => void;
  fetcher.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  fetcher.mockResolvedValueOnce(response({ ...base, state: 'closed', reason: 'scope_revoked' }));
  await click('Close session');
  await act(async () => resolve(response(base)));
  expect(screen.getByText(/scope_revoked/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeDisabled();
});

test('send snapshots cannot skip a backlog; closed sessions drain final pages', async () => {
  render(<WSRepeater />); await start();
  fetcher.mockResolvedValueOnce(response({ ...base, nextSequence: 100, latestSequence: 101, messages: [message(100)] }));
  await click('Send WebSocket message');
  fetcher.mockResolvedValueOnce(response({ ...base, state: 'closed', reason: 'peer_closed', messages: Array.from({ length: 100 }, (_, i) => message(i + 1)), oldestSequence: 1, latestSequence: 101, nextSequence: 100 }));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(String(fetcher.mock.calls.at(-1)?.[0])).toContain('afterSequence=0');
  fetcher.mockResolvedValueOnce(response({ ...base, state: 'closed', reason: 'peer_closed', messages: [message(101)], oldestSequence: 1, latestSequence: 101, nextSequence: 101 }));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(String(fetcher.mock.calls.at(-1)?.[0])).toContain('afterSequence=100');
  expect(screen.getByLabelText('Session messages').querySelectorAll('article')).toHaveLength(101);
});

test('cancel send reports unknown delivery and ignores late completion', async () => {
  render(<WSRepeater />); await start();
  let resolve!: (value: Response) => void;
  fetcher.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  await click('Send WebSocket message');
  fetcher.mockResolvedValueOnce(response({ ...base, state: 'closed', reason: 'operator_closed' }));
  await click('Cancel send');
  expect(screen.getByText(/delivery unknown/i)).toBeInTheDocument();
  await act(async () => resolve(response(base)));
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeDisabled();
});

test('late poll after mode departure cannot replace the new session', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  render(<WSRepeater />); await start();
  let resolve!: (value: Response) => void;
  fetcher.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  await click('One-shot'); await start();
  await act(async () => resolve(response({ ...base, state: 'closed', reason: 'stale_reason', messages: [message(1, 'stale payload')], nextSequence: 1 })));
  expect(screen.queryByText(/stale_reason|stale payload/)).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeEnabled();
});

test('busy rejection is not sent; expired polling is terminal without reconnection', async () => {
  render(<WSRepeater />); await start();
  fetcher.mockResolvedValueOnce(response({}, 409)); await click('Send WebSocket message');
  expect(screen.getByText(/Message not sent/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeEnabled();
  fetcher.mockResolvedValueOnce(response({}, 404));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(screen.getByText(/expired_or_disposed/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeDisabled();
});

test('source replacement requires confirmation and disposes the active session', async () => {
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
  const view = render(<WSRepeater />); await start();
  view.rerender(<WSRepeater source={{ connectionId: 1, messageId: 2, revision: 1 }} />);
  expect(confirm).toHaveBeenCalled();
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeEnabled();
  confirm.mockReturnValue(true);
  fetcher.mockImplementation(async (url, init) => init?.method === 'DELETE' ? response(null, 204) : String(url).endsWith('/draft') ? response({ url: base.url, type: 'binary', payload: 'ff', payloadFormat: 'hex', headers: {}, subprotocols: [] }) : response(base));
  await act(async () => view.rerender(<WSRepeater source={{ connectionId: 1, messageId: 2, revision: 2 }} />));
  expect(screen.getByLabelText('Message payload')).toHaveValue('ff');
  expect(screen.getByLabelText('Handshake headers')).toHaveValue('');
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeDisabled();
  expect(fetcher.mock.calls.some(([, init]) => init?.method === 'DELETE')).toBe(true);
});

test('cancel connection disposes a late success; mode changes and departure confirm', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(false);
  const guard = { current: () => true };
  const view = render(<WSRepeater leaveGuard={guard} />);
  let resolve!: (value: Response) => void;
  fetcher.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  await start();
  expect(screen.getByLabelText('WebSocket URL')).toBeDisabled();
  await click('Cancel connect');
  expect(fetcher.mock.calls[0][1]!.signal!.aborted).toBe(true);
  await act(async () => resolve(response(base)));
  expect(fetcher.mock.calls.at(-1)?.[1]?.method).toBe('DELETE');
  await click('Connect');
  expect(guard.current()).toBe(false);
  await click('One-shot');
  expect(screen.getByRole('button', { name: 'Close session' })).toBeInTheDocument();
  vi.mocked(window.confirm).mockReturnValue(true);
  expect(guard.current()).toBe(true);
  view.unmount();
  expect(fetcher.mock.calls.at(-1)?.[1]).toMatchObject({ method: 'DELETE', keepalive: true });
});

test('send_failed is delivery unknown, never retried, and cannot send again', async () => {
  render(<WSRepeater />); await start();
  fetcher.mockResolvedValueOnce(response({ ...base, state: 'closed', reason: 'send_failed' }));
  await click('Send WebSocket message');
  expect(screen.getByText(/delivery unknown/i)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeDisabled();
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith('/send'))).toHaveLength(1);
});

test('lost send response reports unknown delivery even after a terminal poll', async () => {
  render(<WSRepeater />); await start();
  let reject!: (reason: Error) => void;
  fetcher.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
  await click('Send WebSocket message');
  fetcher.mockResolvedValueOnce(response({ ...base, state: 'closed', reason: 'peer_closed' }));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  await act(async () => reject(new TypeError('Network response lost')));
  expect(screen.getByText(/delivery unknown/i)).toBeInTheDocument();
  expect(screen.getByText(/Close reason: send_failed/)).toBeInTheDocument();
});

test('retention loss counts known unread records and marks additional loss unknown', async () => {
  render(<WSRepeater />); await start();
  fetcher.mockResolvedValueOnce(response({ ...base, latestSequence: 200, nextSequence: 100 }));
  await click('Send WebSocket message');
  fetcher.mockResolvedValueOnce(response({}, 404));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(screen.getByText(/Capture gaps: 200/)).toBeInTheDocument();
  expect(screen.getByText(/additional missing messages is unknown/i)).toBeInTheDocument();
});

test.each(['send', 'close'])('lost %s response marks unknown collection loss even at a caught-up cursor', async operation => {
  render(<WSRepeater />); await start();
  fetcher.mockRejectedValueOnce(new TypeError('Lost response'));
  await click(operation === 'send' ? 'Send WebSocket message' : 'Close session');
  expect(screen.getByText(/additional missing messages is unknown/i)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeDisabled();
  expect(fetcher.mock.calls.at(-1)?.[1]?.method).toBe('DELETE');
});

test('failed close after canceling a send preserves delivery uncertainty', async () => {
  render(<WSRepeater />); await start();
  fetcher.mockImplementationOnce(() => new Promise(() => {}));
  await click('Send WebSocket message');
  fetcher.mockRejectedValueOnce(new TypeError('Lost close response'));
  await click('Cancel send');
  expect(screen.getByText(/delivery unknown/i)).toBeInTheDocument();
  expect(screen.getByText(/additional missing messages is unknown/i)).toBeInTheDocument();
});

test('bounded visible log reports server gaps and local evictions', async () => {
  render(<WSRepeater />); await start();
  for (let page = 0; page < 3; page++) {
    fetcher.mockResolvedValueOnce(response({ ...base, messages: Array.from({ length: 100 }, (_, i) => message(page * 100 + i + 1)), oldestSequence: 1, latestSequence: 300, nextSequence: (page + 1) * 100, droppedMessages: 2 }));
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  }
  expect(screen.getByLabelText('Session messages').querySelectorAll('article')).toHaveLength(256);
  expect(screen.getByText(/Local evictions: 44/)).toBeInTheDocument();
  expect(screen.getByText(/Server evictions: 2/)).toBeInTheDocument();
  fetcher.mockResolvedValueOnce(response({ ...base, messages: [message(301, 'a'.repeat(600000)), message(302, 'b'.repeat(600000))], nextSequence: 302, latestSequence: 302, oldestSequence: 301 }));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(screen.getByLabelText('Session messages').querySelectorAll('article')).toHaveLength(1);
});
