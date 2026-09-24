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
  vi.stubGlobal('fetch', vi.fn(async (_path: RequestInfo | URL, init?: RequestInit) => {
    calls.push(JSON.parse(String(init?.body)));
    return new Response(JSON.stringify({ historyId: 7, probeCount: 1, probes: [{ parameter: 'q', status: 200, reflected: true, partial: false }] }), { status: 200 });
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

it('disables scans for out-of-scope and non-GET captures', () => {
  const { rerender } = render(<ActiveScannerWorkspace exchange={sample({ inScope: false })} />);
  expect(screen.getByRole('button', { name: 'Start scan' })).toBeDisabled();
  rerender(<ActiveScannerWorkspace exchange={sample({ method: 'POST' })} />);
  expect(screen.getByRole('button', { name: 'Start scan' })).toBeDisabled();
});
