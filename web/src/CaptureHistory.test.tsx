import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, expect, test, vi } from 'vitest';
import { App } from './App';
import type { HistoryItem } from './types';
import type { ServerEvent } from './api/events';

let emit: (event: ServerEvent) => void;
vi.mock('./api/events', () => ({ connectEvents: (listener: typeof emit) => { emit = listener; return () => undefined; } }));
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });
const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
const item = (id: number): HistoryItem => ({
  id, method: 'GET', scheme: 'https', host: 'capture.test', path: `/request-${id}`, query: '',
  status: 200, mimeType: 'text/plain', requestSize: 0, responseSize: 1, durationMs: 1,
  startedAt: '2026-09-10T10:00:00Z', intercepted: false, error: false, inScope: id % 2 === 0,
  scopeVersion: 1, scopeRuleId: null,
});

function fixture(count = 201) {
  const state = {
    items: Array.from({ length: count }, (_, index) => item(count - index)),
    storage: { limitBytes: 1073741824, usedBytes: 1048576, paused: false, skippedRecords: 0 },
    pageOverride: null as null | ((url: URL) => Promise<Response> | undefined),
    storageOverride: null as null | (() => Promise<Response>),
    failSave: false,
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    const path = url.pathname;
    if (path === '/api/history/page') {
      const overridden = state.pageOverride?.(url);
      if (overridden) return overridden;
      const q = url.searchParams;
      const snapshotId = Number(q.get('snapshotId')) || state.items[0]?.id || 0;
      const before = Number(q.get('beforeId')) || Infinity;
      const rows = state.items.filter((row) => row.id <= snapshotId && row.id < before
        && (!q.has('inScope') || row.inScope === (q.get('inScope') === 'true'))
        && (!q.has('search') || [row.host, row.path, row.query].some((value) => value.includes(q.get('search')!))));
      return json({ items: rows.slice(0, 100), snapshotId, nextBeforeId: rows.length > 100 ? rows[99].id : 0 });
    }
    if (/^\/api\/history\/\d+$/.test(path)) {
      const row = state.items.find((entry) => entry.id === Number(path.split('/').at(-1)))!;
      const message = { body: '', headers: {}, raw: '', truncated: false, textSafe: true };
      return json({ ...row, request: message, response: message, tags: [], note: '' });
    }
    if (path === '/api/storage') {
      if (init?.method === 'PUT') {
        if (state.failSave) return new Response('Storage update failed', { status: 500 });
        state.storage = { ...state.storage, limitBytes: JSON.parse(String(init.body)).limitBytes, paused: false };
      } else if (state.storageOverride) return state.storageOverride();
      return json(state.storage);
    }
    if (path === '/api/status') return json({ apiAddr: '', proxyAddr: '', caFingerprint: '', caTrust: 'unavailable', httpsInterception: false });
    if (path === '/api/intercept/config') return json({ enabled: false, rules: [] });
    if (path === '/api/intercept/queue' || path === '/api/intercept/response-queue') return json([]);
    if (path === '/api/repeater/sessions/default/send') return json({ saved: false, storageWarning: 'Capture budget exhausted.',
      status: 200, body: 'Upstream response received', headers: {}, durationMs: 1, size: 26, truncated: false, contentType: 'text/plain' });
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  const pageCalls = () => fetchMock.mock.calls.map(([input]) => new URL(String(input), 'http://localhost')).filter((url) => url.pathname === '/api/history/page');
  return { state, fetchMock, pageCalls };
}
const event = (type: string) => act(() => emit({ type, data: {} }));
const rows = () => within(screen.getByRole('table', { name: 'Request history' })).getAllByRole('row').slice(1);
const next = () => screen.getByRole('button', { name: 'Next' });
const previous = () => screen.getByRole('button', { name: 'Previous' });
async function ready() { await waitFor(() => expect(screen.getByRole('button', { name: /Refresh history/ })).toBeEnabled()); }

test.each([0, 100, 101])('renders bounded navigation for %i retained records', async (count) => {
  const { fetchMock } = fixture(count);
  render(<App />);
  await ready();
  expect(rows()).toHaveLength(Math.min(count, 100));
  expect(previous()).toBeDisabled();
  if (count > 100) expect(next()).toBeEnabled(); else expect(next()).toBeDisabled();
  expect(fetchMock.mock.calls.some(([input]) => String(input) === '/api/history')).toBe(false);
});

test('keeps older snapshots stable through new traffic, Previous and metadata updates', async () => {
  const { state, pageCalls } = fixture();
  render(<App />);
  await ready();
  fireEvent.click(next());
  await screen.findByText('/request-101');
  expect(rows()).toHaveLength(100);
  expect(pageCalls().at(-1)?.searchParams.get('beforeId')).toBe('102');
  expect(pageCalls().at(-1)?.searchParams.get('snapshotId')).toBe('201');
  state.items.unshift(item(202));
  const calls = pageCalls().length;
  event('history.entry.created');
  expect(screen.getByRole('button', { name: /New traffic/ })).toBeInTheDocument();
  expect(pageCalls()).toHaveLength(calls);
  event('history.entry.updated');
  await ready();
  expect(pageCalls().at(-1)?.searchParams.get('beforeId')).toBe('102');
  expect(screen.getByText('/request-101')).toBeInTheDocument();
  fireEvent.click(next());
  await screen.findByText('/request-1');
  expect(rows()).toHaveLength(1);
  expect(next()).toBeDisabled();
  fireEvent.click(previous());
  await screen.findByText('/request-101');
  fireEvent.click(previous());
  await screen.findByText('/request-201');
  expect(screen.queryByText('/request-202')).not.toBeInTheDocument();
  event('history.entry.created');
  expect(screen.queryByText('/request-202')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /New traffic/ }));
  await screen.findByText('/request-202');
  expect(pageCalls().at(-1)?.searchParams.has('snapshotId')).toBe(false);
});

test('searches beyond page one and resets server scope and snapshot filters', async () => {
  const { pageCalls } = fixture();
  render(<App />);
  await ready();
  fireEvent.click(next());
  await screen.findByText('/request-101');
  fireEvent.change(screen.getByPlaceholderText('Filter requests'), { target: { value: '/request-1' } });
  await waitFor(() => expect(pageCalls().at(-1)?.searchParams.get('search')).toBe('/request-1'));
  await ready();
  expect(pageCalls().at(-1)?.searchParams.has('snapshotId')).toBe(false);
  expect(previous()).toBeDisabled();
  fireEvent.change(screen.getByLabelText('History scope'), { target: { value: 'out' } });
  await waitFor(() => expect(pageCalls().at(-1)?.searchParams.get('inScope')).toBe('false'));
  await ready();
  expect(screen.queryByText('In scope')).not.toBeInTheDocument();
  expect(screen.getByText('/request-1')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('History scope'), { target: { value: 'in' } });
  await waitFor(() => expect(pageCalls().at(-1)?.searchParams.get('inScope')).toBe('true'));
  await ready();
  expect(screen.queryByText('Out of scope')).not.toBeInTheDocument();
});

test.each([200, 500])('ignores late navigation responses (%i) after a new search', async (status) => {
  const { state } = fixture();
  render(<App />);
  await ready();
  let resolve!: (value: Response) => void;
  state.pageOverride = (url) => url.searchParams.has('beforeId') ? new Promise((done) => { resolve = done; }) : undefined;
  fireEvent.click(next());
  fireEvent.change(screen.getByPlaceholderText('Filter requests'), { target: { value: '/request-201' } });
  await ready();
  expect(rows()).toHaveLength(1);
  await act(async () => resolve(status === 200 ? json({ items: [item(99)], snapshotId: 201, nextBeforeId: 0 }) : new Response('late failure', { status })));
  expect(screen.queryByText('/request-99')).not.toBeInTheDocument();
  expect(screen.queryByText(/late failure/)).not.toBeInTheDocument();
  expect(rows()).toHaveLength(1);
});

test('shows global pause warning, validates MiB and saves without losing an edited draft to polling', async () => {
  const { state, fetchMock } = fixture(0);
  state.storage.paused = true;
  state.storage.skippedRecords = 12;
  render(<App />);
  expect(await screen.findByRole('alert')).toHaveTextContent(/new traffic is not being saved/i);
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  const editor = screen.getByLabelText('Capture limit (MiB)');
  expect(editor).toHaveValue('1024');
  for (const value of ['0', '-1', '1.5', '1048577', '']) {
    fireEvent.change(editor, { target: { value } });
    expect(screen.getByRole('button', { name: 'Save storage limit' })).toBeDisabled();
  }
  fireEvent.change(editor, { target: { value: '2048' } });
  event('storage.status.changed');
  await act(async () => {});
  expect(editor).toHaveValue('2048');
  fireEvent.click(screen.getByRole('button', { name: 'Save storage limit' }));
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  expect(fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT')?.[1]?.body).toBe(JSON.stringify({ limitBytes: 2147483648 }));
  expect(screen.getByText(/SQLite.*overhead/i)).toBeInTheDocument();
});

test('polls storage every five seconds and keeps response visible when Repeater cannot save', async () => {
  const { state, fetchMock } = fixture(0);
  vi.useFakeTimers();
  render(<App />);
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  state.storage.paused = true;
  await act(async () => { await vi.advanceTimersByTimeAsync(4998); });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  expect(screen.getByRole('alert')).toHaveTextContent(/Capture storage is paused/i);
  // Event also provides an immediate refresh after a missed notification.
  event('storage.status.changed');
  await act(async () => {});
  expect(screen.getByRole('alert')).toHaveTextContent(/Capture storage is paused/i);
  vi.useRealTimers();
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  expect(await screen.findByDisplayValue('Upstream response received')).toBeInTheDocument();
  expect(screen.getByText(/Capture budget exhausted/)).toBeInTheDocument();
  expect(screen.queryByText(/Repeater send failed/)).not.toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([input]) => String(input) === '/api/storage').length).toBeGreaterThanOrEqual(2);
});

test('failed page navigation does not skip a page when retried', async () => {
  const { state, pageCalls } = fixture();
  render(<App />);
  await ready();
  state.pageOverride = () => Promise.resolve(new Response('page failed', { status: 500 }));
  fireEvent.click(next());
  await screen.findByText(/History unavailable.*page failed/);
  expect(previous()).toBeDisabled();
  expect(screen.getByText('/request-201')).toBeInTheDocument();
  state.pageOverride = null;
  fireEvent.click(next());
  await screen.findByText('/request-101');
  expect(screen.getByText(/Page 2/)).toBeInTheDocument();
  expect(pageCalls().at(-1)?.searchParams.get('beforeId')).toBe('102');
});

test('first-page traffic refresh preserves selection and page failures never load legacy history', async () => {
  const { state, fetchMock } = fixture(2);
  render(<App />);
  await ready();
  const selected = screen.getByRole('button', { name: 'Select GET capture.test/request-1' });
  fireEvent.click(selected);
  state.items.unshift(item(3));
  event('history.entry.created');
  await screen.findByText('/request-3');
  expect(selected).toHaveAttribute('aria-pressed', 'true');
  state.pageOverride = () => Promise.resolve(new Response('unavailable', { status: 503 }));
  fireEvent.click(screen.getByRole('button', { name: 'Refresh history' }));
  await screen.findByText(/History unavailable/);
  expect(fetchMock.mock.calls.some(([input]) => String(input) === '/api/history')).toBe(false);
});

test('stale storage reads cannot undo a successful settings save; errors retain draft', async () => {
  const { state } = fixture(0);
  state.storage.paused = true;
  render(<App />);
  await ready();
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  fireEvent.change(screen.getByLabelText('Capture limit (MiB)'), { target: { value: '2048' } });
  state.failSave = true;
  fireEvent.click(screen.getByRole('button', { name: 'Save storage limit' }));
  await screen.findByText(/Storage limit not saved/);
  expect(screen.getByLabelText('Capture limit (MiB)')).toHaveValue('2048');
  let release!: (value: Response) => void;
  state.storageOverride = () => new Promise((resolve) => { release = resolve; });
  event('storage.status.changed');
  state.failSave = false;
  fireEvent.click(screen.getByRole('button', { name: 'Save storage limit' }));
  await waitFor(() => expect(screen.queryByText(/New traffic is not being saved/)).not.toBeInTheDocument());
  await act(async () => release(json({ limitBytes: 1073741824, usedBytes: 0, paused: true, skippedRecords: 0 })));
  expect(screen.getByLabelText('Capture limit (MiB)')).toHaveValue('2048');
  expect(screen.queryByText(/New traffic is not being saved/)).not.toBeInTheDocument();
});
