import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { IntruderWorkspace } from './IntruderWorkspace';

const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

it('sends selected request bytes and UTF-8 payloads as base64', async () => {
  const calls: unknown[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === '/api/intruder/jobs' && !init?.method) return response([]);
    if (path === '/api/intruder/jobs' && init?.method === 'POST') {
      calls.push(JSON.parse(String(init.body)));
      return response({ id: 'a'.repeat(32), state: 'draft', revision: 1, config: (calls[0] as {config: unknown}).config }, 201);
    }
    if (path.includes('/results?')) return response({ Results: [], NextBeforeSequence: null });
    if (path.endsWith('/api/intruder/jobs/' + 'a'.repeat(32))) return response({ id: 'a'.repeat(32), state: 'draft', revision: 1, config: (calls[0] as {config: unknown}).config });
    throw new Error(`Unexpected ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('WebSocket', undefined);
  render(<IntruderWorkspace />);
  const raw = screen.getByLabelText('Raw HTTP request') as HTMLTextAreaElement;
  raw.setSelectionRange(5, 6);
  await userEvent.click(screen.getByRole('button', { name: 'Mark selected bytes as position' }));
  fireEvent.change(screen.getByLabelText('Payloads, one per line'), { target: { value: 'ä' } });
  await userEvent.click(screen.getByRole('button', { name: 'Create draft' }));
  await waitFor(() => expect(calls).toHaveLength(1));
  const config = (calls[0] as {config: {template: {raw: string}; positions: Array<{Start: number; End: number}>; payloadSets: Array<{Payloads: string[]}>}}).config;
  expect(atob(config.template.raw)).toContain('\r\n');
  expect(config.positions[0]).toMatchObject({ Start: 5, End: 6 });
  expect(config.payloadSets[0].Payloads).toEqual([btoa('\xc3\xa4')]);
  await waitFor(() => expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled());
  fireEvent.change(screen.getByLabelText('Destination URL'), { target: { value: 'http://changed.test/' } });
  expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
  const confirm = vi.fn(() => false);
  vi.stubGlobal('confirm', confirm);
  await userEvent.click(screen.getByRole('button', { name: 'New' }));
  expect(confirm).toHaveBeenCalled();
  expect(screen.getByLabelText('Destination URL')).toHaveValue('http://changed.test/');
  vi.unstubAllGlobals();
});

it('rejects malformed hex and submits binary payload bytes exactly', async () => {
  const creates: Array<{config: {payloadSets: Array<{Payloads: string[]}>}}> = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === '/api/intruder/jobs' && !init?.method) return response([]);
    if (path === '/api/intruder/jobs' && init?.method === 'POST') {
      const body = JSON.parse(String(init.body)) as typeof creates[number];
      creates.push(body);
      return response({ id: 'b'.repeat(32), state: 'draft', revision: 1, config: body.config }, 201);
    }
    if (path.includes('/results?')) return response({ Results: [], NextBeforeSequence: null });
    if (path.endsWith('/api/intruder/jobs/' + 'b'.repeat(32))) return response({ id: 'b'.repeat(32), state: 'draft', revision: 1, config: creates[0].config });
    throw new Error(`Unexpected ${path}`);
  }));
  vi.stubGlobal('WebSocket', undefined);
  render(<IntruderWorkspace />);
  const raw = screen.getByLabelText('Raw HTTP request') as HTMLTextAreaElement;
  raw.setSelectionRange(5, 6);
  await userEvent.click(screen.getByRole('button', { name: 'Mark selected bytes as position' }));
  await userEvent.selectOptions(screen.getByLabelText('Payload format for p1'), 'hex');
  const hex = screen.getByLabelText('Hex payloads, one per line');
  fireEvent.change(hex, { target: { value: '0 f' } });
  await userEvent.click(screen.getByRole('button', { name: 'Create draft' }));
  expect(screen.getByRole('alert')).toHaveTextContent(/complete byte pairs/);
  expect(creates).toHaveLength(0);
  fireEvent.change(hex, { target: { value: '00 ff' } });
  await userEvent.click(screen.getByRole('button', { name: 'Create draft' }));
  await waitFor(() => expect(creates).toHaveLength(1));
  expect(creates[0].config.payloadSets[0].Payloads).toEqual(['AP8=']);
  vi.unstubAllGlobals();
});

it('filters a result page and loads only the selected bounded detail', async () => {
  const id = 'c'.repeat(32);
  const queries: string[] = [];
  const config = { attack: 'sniper', template: { method: 'GET', url: 'http://local.test/', raw: btoa('GET / HTTP/1.1\r\nHost: local.test\r\n\r\n') }, positions: [], payloadSets: [], requestLimit: 1, concurrency: 1, ratePerSecond: 1, timeoutMs: 1000 };
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === '/api/intruder/jobs') return response([{ ID: id, Attack: 'sniper', State: 'completed', CompletedCount: 1, TotalRequests: 1 }]);
    if (path === `/api/intruder/jobs/${id}`) return response({ id, state: 'completed', revision: 3, config, completedCount: 1, totalRequests: 1, errorCount: 0 });
    if (path.startsWith(`/api/intruder/jobs/${id}/results?`)) {
      queries.push(path);
      return response({ Results: [{ Sequence: 0, URL: 'http://local.test/', Status: 200, ResponseSize: 2, MIMEType: 'text/plain', ErrorCategory: '' }], NextBeforeSequence: null });
    }
    if (path === `/api/intruder/jobs/${id}/results/0`) return response({ Sequence: 0, URL: 'http://local.test/', Status: 200, ResponseSize: 2, MIMEType: 'text/plain', ResponseCapture: btoa('ok'), RequestCapture: '', StorageStatus: '', ResponseTruncated: false });
    throw new Error(`Unexpected ${path}`);
  }));
  vi.stubGlobal('WebSocket', undefined);
  render(<IntruderWorkspace />);
  await userEvent.click(await screen.findByRole('button', { name: /sniper/ }));
  await userEvent.click(await screen.findByRole('button', { name: /#0/ }));
  expect(await screen.findByText('ok')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Status'), { target: { value: '200' } });
  await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }));
  await waitFor(() => expect(queries.some((query) => query.includes('status=200'))).toBe(true));
  vi.unstubAllGlobals();
});

it('preserves a handed-off request body byte-for-byte when marking a position', async () => {
  const original = 'POST / HTTP/1.1\r\nHost: local.test\r\n\r\nline1\nline2';
  let submitted: {config: {template: {raw: string}; positions: Array<{Start: number; End: number}>}} | null = null;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input) === '/api/intruder/jobs' && !init?.method) return response([]);
    if (String(input) === '/api/intruder/jobs' && init?.method === 'POST') {
      submitted = JSON.parse(String(init.body));
      return response({ id: 'd'.repeat(32), state: 'draft', revision: 1, config: submitted!.config }, 201);
    }
    if (String(input).includes('/results?')) return response({ Results: [], NextBeforeSequence: null });
    if (String(input).endsWith('/api/intruder/jobs/' + 'd'.repeat(32))) return response({ id: 'd'.repeat(32), state: 'draft', revision: 1, config: submitted!.config });
    throw new Error(`Unexpected ${String(input)}`);
  }));
  vi.stubGlobal('WebSocket', undefined);
  render(<IntruderWorkspace source={{ url: 'http://local.test/', method: 'POST', raw: original, revision: 1 }} />);
  const editor = await screen.findByLabelText('Raw HTTP request') as HTMLTextAreaElement;
  await waitFor(() => expect(editor.value).toContain('line1\nline2'));
  const selected = editor.value.indexOf('line2');
  editor.setSelectionRange(selected, selected + 5);
  await userEvent.click(screen.getByRole('button', { name: 'Mark selected bytes as position' }));
  fireEvent.change(screen.getByLabelText('Payloads, one per line'), { target: { value: 'replacement' } });
  await userEvent.click(screen.getByRole('button', { name: 'Create draft' }));
  await waitFor(() => expect(submitted).not.toBeNull());
  expect(atob(submitted!.config.template.raw)).toBe(original);
  expect(submitted!.config.positions[0].Start).toBe(new TextEncoder().encode(original.slice(0, original.indexOf('line2'))).length);
  vi.unstubAllGlobals();
});

it('edits a binary raw request in hex mode without losing bytes', async () => {
  const id = 'e'.repeat(32);
  const original = 'POST / HTTP/1.1\r\nHost: local.test\r\n\r\n' + String.fromCharCode(0, 255);
  const config = { attack: 'sniper', template: { method: 'POST', url: 'http://local.test/', raw: btoa(original) }, positions: [], payloadSets: [], requestLimit: 1, concurrency: 1, ratePerSecond: 1, timeoutMs: 1000 };
  let saved: {config: typeof config} | null = null;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === '/api/intruder/jobs' && !init?.method) return response([{ ID: id, Attack: 'sniper', State: 'draft', CompletedCount: 0, TotalRequests: 1 }]);
    if (path === `/api/intruder/jobs/${id}` && !init?.method) return response({ id, state: 'draft', revision: 1, config });
    if (path === `/api/intruder/jobs/${id}` && init?.method === 'PUT') { saved = JSON.parse(String(init.body)); return response({ id, state: 'draft', revision: 2, config: saved!.config }); }
    if (path.includes('/results?')) return response({ Results: [], NextBeforeSequence: null });
    throw new Error(`Unexpected ${path}`);
  }));
  vi.stubGlobal('WebSocket', undefined);
  render(<IntruderWorkspace />);
  await userEvent.click(await screen.findByRole('button', { name: /sniper/ }));
  expect(await screen.findByLabelText('Request format')).toHaveValue('hex');
  const raw = screen.getByLabelText('Raw HTTP request') as HTMLTextAreaElement;
  await waitFor(() => expect(raw.value).toContain('00 ff'));
  fireEvent.change(raw, { target: { value: raw.value.replace('00 ff', '01 ff') } });
  const marker = raw.value.indexOf('01 ff');
  raw.setSelectionRange(marker, marker + 2);
  await userEvent.click(screen.getByRole('button', { name: 'Mark selected bytes as position' }));
  fireEvent.change(screen.getByLabelText('Payloads, one per line'), { target: { value: 'x' } });
  await userEvent.click(screen.getByRole('button', { name: 'Save draft' }));
  await waitFor(() => expect(saved).not.toBeNull());
  expect(atob(saved!.config.template.raw)).toBe(original.slice(0, -2) + String.fromCharCode(1, 255));
  expect(saved!.config.positions[0]).toMatchObject({ Start: original.length - 2, End: original.length - 1 });
  vi.unstubAllGlobals();
});
