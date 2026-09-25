import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, expect, test, vi } from 'vitest';
import { App } from './App';
import { Inspector } from './components/Inspector';
import type { Exchange, InterceptConfig, InterceptItem } from './types';
import type { ServerEvent } from './api/events';

let emit: (event: ServerEvent) => void;
vi.mock('./api/events', () => ({ connectEvents: (listener: typeof emit) => { emit = listener; return () => undefined; } }));
afterEach(() => vi.unstubAllGlobals());

const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
const filter = { enabled: true, method: 'GET', hostContains: 'example.test', pathContains: '/api', mimeContains: 'json' };
const initial: InterceptConfig = { enabled: true, rules: [filter], responseEnabled: false, responseRules: [{ ...filter, statusCode: 201 }], replacementRules: [{ id: 'replace-1', enabled: true, direction: 'response', target: 'body', header: '', pattern: 'before', replacement: 'after', regex: false, hostContains: '', pathContains: '', mimeContains: '' }] };
const responseItem = (id: string, bodyEditable = true): InterceptItem => ({ id, phase: 'response', method: 'GET', url: 'https://example.test/api', statusCode: 201, headers: { 'Content-Type': ['text/plain'] }, body: 'before', bodyEditable, bodyTruncated: !bodyEditable });

function fixture(config: InterceptConfig = structuredClone(initial)) {
  const state = { config, queue: [] as InterceptItem[], failSave: false, failAction: false, holdSave: null as Promise<void> | null };
  const writes: InterceptConfig[] = [];
  const actions: { path: string; body: unknown }[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === '/api/status') return json({ apiAddr: '', proxyAddr: '', caFingerprint: '', caTrust: 'unavailable', httpsInterception: false });
    if (path.startsWith('/api/history/page')) return json({ items: [], nextBeforeId: 0, snapshotId: 0 });
    if (path === '/api/storage') return json({ limitBytes: 1073741824, usedBytes: 0, paused: false, skippedRecords: 0 });
    if (path === '/api/intercept/queue') return json([]);
    if (path === '/api/intercept/response-queue') return json(state.queue);
    if (path === '/api/intercept/config') {
      if (init?.method === 'PUT') {
        writes.push(JSON.parse(String(init.body)) as InterceptConfig);
        if (state.holdSave) await state.holdSave;
        if (state.failSave) return new Response('invalid regex in replace-1', { status: 400 });
        state.config = writes.at(-1)!;
      }
      return json(state.config);
    }
    if (path.startsWith('/api/intercept/response/') && init?.method === 'POST') {
      actions.push({ path, body: JSON.parse(String(init.body)) });
      if (state.failAction) return new Response('response is no longer queued', { status: 404 });
      state.queue = state.queue.filter((item) => !path.includes(`/${item.id}/`));
      return new Response(null, { status: 204 });
    }
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { state, writes, actions, fetchMock };
}

test('independent toggles and rule saves preserve all other configuration', async () => {
  const { writes } = fixture();
  render(<App />);
  const responseToggle = screen.getByLabelText('Pause matching responses');
  await waitFor(() => expect(responseToggle).toBeEnabled());
  fireEvent.click(responseToggle);
  await waitFor(() => expect(responseToggle).toBeChecked());
  expect(writes[0]).toEqual({ ...initial, responseEnabled: true });
  fireEvent.click(screen.getByLabelText('Pause matching requests'));
  await waitFor(() => expect(screen.getByLabelText('Pause matching requests')).not.toBeChecked());
  expect(writes[1]).toEqual({ ...initial, enabled: false, responseEnabled: true });
  fireEvent.click(screen.getByText('Response filters'));
  fireEvent.change(screen.getByLabelText('Response filter 1 Status code'), { target: { value: '404' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save response filters' }));
  await waitFor(() => expect(writes).toHaveLength(3));
  expect(writes[2]).toEqual({ ...writes[1], responseRules: [{ ...filter, statusCode: 404 }] });
  await waitFor(() => expect(responseToggle).toBeEnabled());
  fireEvent.click(screen.getByText('Match and replace'));
  fireEvent.change(await screen.findByLabelText('Replacement'), { target: { value: 'new' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save replacements' }));
  await waitFor(() => expect(writes).toHaveLength(4));
  expect(writes[3]).toEqual({ ...writes[2], replacementRules: [{ ...initial.replacementRules![0], replacement: 'new' }] });
});

test('old request-only config gets safe response defaults on save', async () => {
  const { writes } = fixture({ enabled: false, rules: [filter] });
  render(<App />);
  const toggle = screen.getByLabelText('Pause matching requests');
  await waitFor(() => expect(toggle).toBeEnabled());
  expect(screen.getByLabelText('Pause matching responses')).not.toBeChecked();
  fireEvent.click(toggle);
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({ enabled: true, rules: [filter], responseEnabled: false, replacementRules: [], responseRules: [{ enabled: true, method: '', hostContains: '', pathContains: '', mimeContains: '', statusCode: 0 }] });
});

test('pending saves lock independent controls and keep replacement drafts across toggle saves', async () => {
  const { state, writes } = fixture();
  render(<App />);
  await waitFor(() => expect(screen.getByLabelText('Pause matching responses')).toBeEnabled());
  fireEvent.click(screen.getByText('Match and replace'));
  fireEvent.change(await screen.findByLabelText('Replacement'), { target: { value: 'unsaved' } });
  let release!: () => void;
  state.holdSave = new Promise<void>((resolve) => { release = resolve; });
  fireEvent.click(screen.getByLabelText('Pause matching responses'));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(screen.getByLabelText('Pause matching requests')).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Save replacements' })).toBeDisabled();
  await act(async () => release());
  await waitFor(() => expect(screen.getByLabelText('Pause matching requests')).toBeEnabled());
  expect(screen.getByLabelText('Replacement')).toHaveValue('unsaved');
});

test('server validation errors are displayed and failed configuration stays inactive', async () => {
  const { state } = fixture();
  state.failSave = true;
  render(<App />);
  const toggle = screen.getByLabelText('Pause matching responses');
  await waitFor(() => expect(toggle).toBeEnabled());
  fireEvent.click(toggle);
  expect(await screen.findByRole('status')).toHaveTextContent('invalid regex in replace-1');
  expect(toggle).not.toBeChecked();
  state.failSave = false;
  fireEvent.click(toggle);
  await waitFor(() => expect(toggle).toBeChecked());
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

test('response editor forwards status, duplicate headers and body, and drops another response', async () => {
  const { state, actions } = fixture();
  state.queue = [responseItem('one'), responseItem('two', false)];
  render(<App />);
  fireEvent.change(await screen.findByLabelText('Response status one'), { target: { value: '202' } });
  fireEvent.change(screen.getByLabelText('Response headers one'), { target: { value: 'X-Test: one\nX-Test: two' } });
  fireEvent.change(screen.getByLabelText('Response body one'), { target: { value: 'edited' } });
  expect(screen.getByLabelText('Response body two')).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Forward response one' }));
  await waitFor(() => expect(actions).toHaveLength(1));
  expect(actions[0]).toEqual({ path: '/api/intercept/response/one/forward', body: { statusCode: 202, headers: { 'X-Test': ['one', 'two'] }, body: 'edited' } });
  await waitFor(() => expect(screen.queryByLabelText('Response body one')).not.toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: 'Drop response two' }));
  await waitFor(() => expect(actions).toHaveLength(2));
  expect(actions[1]).toEqual({ path: '/api/intercept/response/two/drop', body: {} });
});

test('queue events refresh responses without losing an in-progress edit', async () => {
  const { state } = fixture();
  state.queue = [responseItem('one')];
  render(<App />);
  fireEvent.change(await screen.findByLabelText('Response body one'), { target: { value: 'draft' } });
  state.queue.push(responseItem('two'));
  act(() => emit({ type: 'intercept.response.queued', data: {} }));
  await screen.findByLabelText('Response body two');
  expect(screen.getByLabelText('Response body one')).toHaveValue('draft');
  state.queue = [responseItem('one')];
  act(() => emit({ type: 'intercept.response.completed', data: {} }));
  await waitFor(() => expect(screen.queryByLabelText('Response body two')).not.toBeInTheDocument());
});

test('invalid edits do not forward and API failures leave response edits available for retry', async () => {
  const { state, actions } = fixture();
  state.queue = [responseItem('one')];
  state.failAction = true;
  render(<App />);
  const status = await screen.findByLabelText('Response status one');
  fireEvent.change(status, { target: { value: '99' } });
  fireEvent.click(screen.getByRole('button', { name: 'Forward response one' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('200 to 599');
  expect(actions).toHaveLength(0);
  fireEvent.change(status, { target: { value: '202' } });
  fireEvent.change(screen.getByLabelText('Response headers one'), { target: { value: 'invalid header' } });
  fireEvent.click(screen.getByRole('button', { name: 'Forward response one' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Name: value');
  expect(actions).toHaveLength(0);
  fireEvent.change(screen.getByLabelText('Response headers one'), { target: { value: 'X-Test: retry' } });
  fireEvent.click(screen.getByRole('button', { name: 'Forward response one' }));
  expect(await screen.findByRole('status')).toHaveTextContent('response is no longer queued');
  expect(status).toHaveValue(202);
  expect(screen.getByLabelText('Response headers one')).toHaveValue('X-Test: retry');
});

test('Inspector audit reports response interception and ordered rule IDs with legacy defaults', () => {
  const message = { headers: {}, body: '', raw: '', textSafe: true, truncated: false };
  const exchange = { request: message, response: message, method: 'GET', scheme: 'http', host: 'test', path: '/', startedAt: '', durationMs: 0, intercepted: false, responseIntercepted: true, appliedRuleIds: ['second', 'first'], errorMessage: 'Response dropped' } as Exchange;
  const { rerender } = render(<Inspector exchange={exchange} />);
  fireEvent.click(screen.getByRole('tab', { name: 'Audit' }));
  expect(screen.getByRole('tabpanel')).toHaveTextContent('Response intercepted: Yes');
  expect(screen.getByRole('tabpanel')).toHaveTextContent(/second first/);
  expect(screen.getByRole('tabpanel')).toHaveTextContent('Response dropped');
  rerender(<Inspector exchange={{ ...exchange, responseIntercepted: undefined, appliedRuleIds: undefined }} />);
  expect(within(screen.getByRole('tabpanel')).getByText(/Response intercepted: No/)).toHaveTextContent('None');
});
