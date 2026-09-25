import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { ActiveScannerWorkspace } from './ActiveScannerWorkspace';
import type { Exchange } from '../types';

function sample(overrides: Partial<Exchange> = {}): Exchange {
  return {
    id: 7, method: 'GET', scheme: 'https', host: 'example.test', path: '/search', query: 'q=private',
    status: 200, mimeType: 'text/html', requestSize: 0, responseSize: 0, durationMs: 1,
    startedAt: '', intercepted: false, error: false, inScope: true, scopeVersion: 1,
    scopeRuleId: 1, errorMessage: '', requestTruncated: false, responseTruncated: false,
    request: { headers: {}, body: '', raw: '', textSafe: true, truncated: false },
    response: { headers: {}, body: '', raw: '', textSafe: true, truncated: false },
    tags: [], note: '', ...overrides,
  };
}

it('requires confirmation and sends only bounded job parameters', async () => {
  const calls: unknown[] = [];
  vi.stubGlobal('confirm', vi.fn(() => false));
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL, init?: RequestInit) => {
    if (String(path) === '/api/active-scan/runs') return new Response('[]', { status: 200 });
    calls.push(JSON.parse(String(init?.body)));
    return new Response(JSON.stringify({ runId: 1, historyId: 7, state: 'completed', probeCount: 1, probes: [{ parameter: 'q', status: 200, reflected: true, partial: false }] }), { status: 200 });
  }));
  render(<ActiveScannerWorkspace exchange={sample()} />);
  await userEvent.click(screen.getByRole('button', { name: 'Start scan' }));
  expect(calls).toHaveLength(0);
  vi.stubGlobal('confirm', vi.fn(() => true));
  await userEvent.selectOptions(screen.getByLabelText('Maximum probes'), '1');
  await userEvent.click(screen.getByRole('button', { name: 'Start scan' }));
  await waitFor(() => expect(calls).toEqual([{ historyId: 7, maxProbes: 1, acknowledge: true }]));
  expect(await screen.findByText(/marker reflected/)).toBeInTheDocument();
  vi.unstubAllGlobals();
});

it('disables scans for out-of-scope and non-GET captures', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200 })));
  const { rerender } = render(<ActiveScannerWorkspace exchange={sample({ inScope: false })} />);
  await screen.findByText('No saved runs yet.');
  expect(screen.getByRole('button', { name: 'Start scan' })).toBeDisabled();
  rerender(<ActiveScannerWorkspace exchange={sample({ method: 'POST' })} />);
  expect(screen.getByRole('button', { name: 'Start scan' })).toBeDisabled();
  vi.unstubAllGlobals();
});

it('reopens saved scan results after mounting a fresh workspace', async () => {
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL) => {
    if (String(path) === '/api/active-scan/runs') return new Response(JSON.stringify([{ id: 4, historyId: 7, host: 'example.test', path: '/search', state: 'completed', probeCount: 1, reflectedCount: 1, startedAt: '' }]), { status: 200 });
    if (String(path) === '/api/active-scan/runs/4') return new Response(JSON.stringify({ id: 4, historyId: 7, state: 'completed', probeCount: 1, probes: [{ parameter: 'q', status: 200, reflected: true, partial: false }] }), { status: 200 });
    throw new Error(`Unexpected ${String(path)}`);
  }));
  render(<ActiveScannerWorkspace exchange={null} />);
  await userEvent.click(await screen.findByRole('button', { name: /#4 · example.test/ }));
  expect(await screen.findByText(/marker reflected/)).toBeInTheDocument();
  vi.unstubAllGlobals();
});

it('deletes a saved run after confirmation', async () => {
  const calls: string[] = [];
  vi.stubGlobal('confirm', vi.fn(() => true));
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL, init?: RequestInit) => {
    calls.push(`${init?.method || 'GET'} ${String(path)}`);
    if (String(path) === '/api/active-scan/runs') return new Response(JSON.stringify([{ id: 4, historyId: 7, host: 'example.test', path: '/search', state: 'completed', probeCount: 1, reflectedCount: 1, startedAt: '' }]), { status: 200 });
    if (String(path) === '/api/active-scan/runs/4' && init?.method === 'DELETE') return new Response(null, { status: 204 });
    throw new Error(`Unexpected ${String(path)}`);
  }));
  render(<ActiveScannerWorkspace exchange={null} />);
  await userEvent.click(await screen.findByRole('button', { name: 'Delete scan #4' }));
  expect(await screen.findByText('No saved runs yet.')).toBeInTheDocument();
  expect(calls).toContain('DELETE /api/active-scan/runs/4');
  vi.unstubAllGlobals();
});
