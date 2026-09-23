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
  vi.unstubAllGlobals();
});
