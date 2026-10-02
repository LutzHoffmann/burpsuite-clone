import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { CrawlWorkspace } from './CrawlWorkspace';

it('requires confirmation and sends bounded crawl settings', async () => {
  const calls: string[] = [];
  const bodies: unknown[] = [];
  vi.stubGlobal('confirm', vi.fn(() => false));
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL, init?: RequestInit) => {
    calls.push(`${init?.method || 'GET'} ${String(path)}`);
    if (String(path) === '/api/crawl/runs' && !init?.method) return new Response('[]', { status: 200 });
    if (String(path) === '/api/crawl/runs' && init?.method === 'POST') { bodies.push(JSON.parse(String(init.body))); return new Response('{"runId":2,"state":"running"}', { status: 202 }); }
    if (String(path) === '/api/crawl/runs/2') return new Response('{"id":2,"state":"completed","pageCount":0,"pages":[],"forms":[]}', { status: 200 });
    throw new Error(String(path));
  }));
  render(<CrawlWorkspace exchange={{ id: 7, method: 'GET', inScope: true, error: false, scheme: 'https', host: 'example.test', path: '/' }} />);
  await userEvent.click(screen.getByRole('button', { name: 'Start crawl' }));
  expect(calls.filter((call) => call.startsWith('POST'))).toHaveLength(0);
  vi.stubGlobal('confirm', vi.fn(() => true));
  await userEvent.type(screen.getByLabelText('Session cookie'), 'sid=explicit');
  await userEvent.type(screen.getByLabelText('Authorization header'), 'Bearer explicit');
  await userEvent.click(screen.getByRole('button', { name: 'Start crawl' }));
  expect(await screen.findByText(/Crawl #2/)).toBeInTheDocument();
  expect(calls).toContain('POST /api/crawl/runs');
  expect(bodies).toEqual([{ historyId: 7, maxPages: 10, maxDepth: 2, acknowledge: true, session: { cookie: 'sid=explicit', authorization: 'Bearer explicit' } }]);
  vi.unstubAllGlobals();
});
