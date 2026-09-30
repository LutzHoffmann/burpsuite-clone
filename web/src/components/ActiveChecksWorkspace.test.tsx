import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { ActiveChecksWorkspace } from './ActiveChecksWorkspace';

it('requires confirmation and labels reflection as an observation', async () => {
  const calls: string[] = [];
  const bodies: unknown[] = [];
  vi.stubGlobal('confirm', vi.fn(() => false));
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL, init?: RequestInit) => {
    calls.push(`${init?.method || 'GET'} ${String(path)}`);
    if (String(path) === '/api/crawl/runs') return new Response('[{"id":3,"state":"completed","pageCount":1}]');
    if (String(path) === '/api/active-checks/runs' && !init?.method) return new Response('[]');
    if (String(path) === '/api/active-checks/runs' && init?.method === 'POST') { bodies.push(JSON.parse(String(init.body))); return new Response('{"runId":4,"state":"running","maximumRequests":1}', { status: 202 }); }
    if (String(path) === '/api/active-checks/runs/4') return new Response('{"id":4,"crawlId":3,"state":"completed","observationCount":1,"observations":[{"url":"https://example.test/","source":"query","parameter":"q","status":200,"found":true,"context":"html_text","partial":false}]}');
    throw new Error(String(path));
  }));
  render(<ActiveChecksWorkspace />);
  await userEvent.selectOptions(await screen.findByLabelText('Crawl run'), '3');
  await userEvent.click(screen.getByRole('button', { name: 'Start active checks' }));
  expect(calls.filter((item) => item.startsWith('POST'))).toHaveLength(0);
  vi.stubGlobal('confirm', vi.fn(() => true));
  await userEvent.type(screen.getByLabelText('Session cookie'), 'sid=explicit');
  await userEvent.click(screen.getByRole('button', { name: 'Start active checks' }));
  expect(await screen.findByText(/Crawl #3 · completed/)).toBeInTheDocument();
  expect(screen.getByText(/observation, not confirmed XSS/)).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Download HTML report' })).toHaveAttribute('href', '/api/active-checks/runs/4/report.html');
  expect(bodies).toEqual([{ crawlId: 3, acknowledge: true, checkRedirects: false, checkCors: false, session: { cookie: 'sid=explicit', authorization: '' } }]);
  vi.unstubAllGlobals();
});

it('opts into redirect checks and labels results separately', async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal('confirm', vi.fn(() => true));
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL, init?: RequestInit) => {
    if (String(path) === '/api/crawl/runs') return new Response('[{"id":3,"state":"completed","pageCount":1}]');
    if (String(path) === '/api/active-checks/runs' && !init?.method) return new Response('[]');
    if (String(path) === '/api/active-checks/runs' && init?.method === 'POST') { bodies.push(JSON.parse(String(init.body))); return new Response('{"runId":5,"state":"running","maximumRequests":2}', { status: 202 }); }
    if (String(path) === '/api/active-checks/runs/5') return new Response('{"id":5,"crawlId":3,"state":"completed","observationCount":1,"observations":[{"url":"https://example.test/go","source":"redirect_query","parameter":"next","status":302,"found":true,"context":"redirect_location","partial":false}]}');
    throw new Error(String(path));
  }));
  render(<ActiveChecksWorkspace />);
  await userEvent.selectOptions(await screen.findByLabelText('Crawl run'), '3');
  await userEvent.click(screen.getByLabelText('Check open redirects'));
  await userEvent.click(screen.getByRole('button', { name: 'Start active checks' }));
  expect(await screen.findByText(/external redirect observed/)).toBeInTheDocument();
  expect(bodies).toEqual([{ crawlId: 3, acknowledge: true, checkRedirects: true, checkCors: false, session: { cookie: '', authorization: '' } }]);
  vi.unstubAllGlobals();
});

it('opts into credentialed CORS checks and labels the observation', async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal('confirm', vi.fn(() => true));
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL, init?: RequestInit) => {
    if (String(path) === '/api/crawl/runs') return new Response('[{"id":3,"state":"completed","pageCount":1}]');
    if (String(path) === '/api/active-checks/runs' && !init?.method) return new Response('[]');
    if (String(path) === '/api/active-checks/runs' && init?.method === 'POST') { bodies.push(JSON.parse(String(init.body))); return new Response('{"runId":6,"state":"running","maximumRequests":2}', { status: 202 }); }
    if (String(path) === '/api/active-checks/runs/6') return new Response('{"id":6,"crawlId":3,"state":"completed","observationCount":1,"observations":[{"url":"https://example.test/api","source":"cors","parameter":"Origin","status":200,"found":true,"context":"cors_credentials","partial":false}]}');
    throw new Error(String(path));
  }));
  render(<ActiveChecksWorkspace />);
  await userEvent.selectOptions(await screen.findByLabelText('Crawl run'), '3');
  await userEvent.click(screen.getByLabelText('Check credentialed CORS'));
  await userEvent.click(screen.getByRole('button', { name: 'Start active checks' }));
  expect(await screen.findByText(/credentialed CORS origin reflection observed/)).toBeInTheDocument();
  expect(bodies).toEqual([{ crawlId: 3, acknowledge: true, checkRedirects: false, checkCors: true, session: { cookie: '', authorization: '' } }]);
  vi.unstubAllGlobals();
});

it('refreshes completed crawls without remounting', async () => {
  let ready = false;
  vi.stubGlobal('fetch', vi.fn(async (path: RequestInfo | URL) => {
    if (String(path) === '/api/crawl/runs') return new Response(ready ? '[{"id":8,"state":"completed","pageCount":2}]' : '[]');
    return new Response('[]');
  }));
  render(<ActiveChecksWorkspace />);
  expect(await screen.findByText('Select completed crawl')).toBeInTheDocument();
  ready = true;
  await userEvent.click(screen.getByRole('button', { name: 'Refresh crawls' }));
  expect(await screen.findByRole('option', { name: 'Crawl #8 · 2 pages' })).toBeInTheDocument();
  vi.unstubAllGlobals();
});
