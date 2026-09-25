import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { FindingsWorkspace } from './FindingsWorkspace';

it('filters, pages with a snapshot, and opens the latest History exchange', async () => {
  const requests: string[] = [];
  const open = vi.fn();
  vi.stubGlobal('WebSocket', undefined);
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    requests.push(url);
    const second = url.includes('offset=100');
    return new Response(JSON.stringify({ items: [{ type: 'cookie_secure_missing', host: 'example.test', subject: 'sid', count: 2, latestExchangeId: second ? 8 : 12, latestPath: '/account' }], nextOffset: second ? 0 : 100, snapshotId: 20 }), { status: 200 });
  }));
  render(<FindingsWorkspace onOpenHistory={open} />);
  expect(await screen.findByText('Cookie without Secure attribute')).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText('Exact host'), 'example.test');
  await userEvent.selectOptions(screen.getByLabelText('Finding type'), 'cookie_secure_missing');
  await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }));
  await waitFor(() => expect(requests.some((url) => url.includes('host=example.test') && url.includes('type=cookie_secure_missing'))).toBe(true));
  await userEvent.click(screen.getByRole('button', { name: 'Next' }));
  await waitFor(() => expect(requests.some((url) => url.includes('offset=100') && url.includes('snapshotId=20'))).toBe(true));
  await userEvent.click(await screen.findByRole('button', { name: 'Open History #8' }));
  expect(open).toHaveBeenCalledWith(8);
  vi.unstubAllGlobals();
});
