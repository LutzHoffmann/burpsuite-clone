import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { TargetWorkspace } from './TargetWorkspace';

const endpoint = {
  id: 7, scheme: 'https', host: 'example.test', port: 443, path: '/api/users', method: 'GET', inScope: true,
  firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 3, statuses: [200, 401],
  requestMimes: ['application/json'], responseMimes: ['application/json'], parseDiagnostics: ['json depth limit reached'],
  errorSeen: false, latestExchangeId: 42,
};

const tree = [{
  id: 0, scheme: 'https', host: 'example.test', port: 443, path: '', method: '', inScope: true, statuses: [200, 401],
  requestMimes: ['application/json'], responseMimes: ['application/json'], count: 3, lastSeen: '2026-08-20T10:05:00Z', children: [{
    ...endpoint, children: [],
  }],
}];

const scope = { version: 3, rules: [
  { id: 11, enabled: true, action: 'include', scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '/' },
  { id: 12, enabled: false, action: 'exclude', scheme: '', hostPattern: 'disabled.test', port: 0, pathPrefix: '/' },
] };

const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), {
  status, headers: { 'Content-Type': 'application/json' },
});

function targetFetchFixture(options: { rebuildStatus?: 'idle' | 'building' | 'failed'; tree?: typeof tree } = {}): ReturnType<typeof vi.fn> {
  const rebuild = options.rebuildStatus ?? 'idle';
  let retried = false;
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/scope/rules' && (!init?.method || init.method === 'GET')) return response(scope);
    if (url.pathname === '/api/target/tree') return response(options.tree ?? tree);
    if (url.pathname === '/api/target/rebuild' && (!init?.method || init.method === 'GET')) {
      const current = retried ? 'building' : rebuild;
      return response({ id: 4, scopeVersion: 3, activeScopeVersion: current === 'building' || current === 'failed' ? 2 : 3, status: current, processed: current === 'building' ? 2 : 0, total: current === 'building' ? 7 : 0, error: current === 'failed' ? 'projection failed' : '' });
    }
    if (url.pathname === '/api/target/rebuild' && init?.method === 'POST') {
      retried = true;
      return response({ id: 5, scopeVersion: 3, activeScopeVersion: 2, status: 'building', processed: 0, total: 7, error: '' }, 202);
    }
    if (url.pathname === '/api/target/endpoints/7') return response(endpoint);
    if (url.pathname === '/api/target/endpoints/7/requests') return response([{ exchangeId: 42, startedAt: '2026-08-20T10:05:00Z', status: 200, error: false }]);
    if (url.pathname === '/api/target/endpoints/7/parameters') return response([{ location: 'json', name: 'user.email', valueType: 'string', firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 3 }]);
    throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url.pathname}`);
  });
}

function scopeConflictFetchFixture(): ReturnType<typeof vi.fn> {
  const fixture = targetFetchFixture();
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/scope/rules' && init?.method === 'PUT') return response({}, 409);
    return fixture(input, init);
  });
}

beforeEach(() => vi.stubGlobal('fetch', targetFetchFixture()));
afterEach(() => vi.unstubAllGlobals());

test('loads the site map and shows endpoint parameters without values', async () => {
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole('treeitem', { name: /example\.test/ }));
  await user.click(screen.getByRole('treeitem', { name: /GET \/api\/users/ }));
  expect(await screen.findByText('user.email')).toBeInTheDocument();
  expect(screen.getByText(/3 occurrences/)).toBeInTheDocument();
  expect(screen.getByText(/json depth limit reached/)).toBeInTheDocument();
  expect(screen.queryByText('secret@example.test')).not.toBeInTheDocument();
});

test('saves versioned scope rules and reports a conflict', async () => {
  const fetchMock = scopeConflictFetchFixture();
  vi.stubGlobal('fetch', fetchMock);
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole('button', { name: 'Add include rule' }));
  await user.type(screen.getByLabelText('Host pattern'), 'other.test');
  await user.click(screen.getByRole('button', { name: 'Save scope' }));
  const save = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/api/scope/rules') && init?.method === 'PUT');
  expect(JSON.parse(String(save?.[1]?.body))).toMatchObject({ version: 3, rules: expect.arrayContaining([expect.objectContaining({ action: 'include', hostPattern: 'other.test' })]) });
  expect(await screen.findByRole('alert')).toHaveTextContent('Scope changed in another session');
  expect(screen.getByRole('group', { name: 'exclude' })).toBeInTheDocument();
  expect(screen.getByDisplayValue('disabled.test')).toBeInTheDocument();
  expect(screen.getAllByRole('checkbox')[1]).not.toBeChecked();
});

test('filters the tree by text, scope, method, status family, and MIME', async () => {
  const alternateTree = [{ ...tree[0], children: [
    tree[0].children[0],
    { ...endpoint, id: 8, host: 'outside.test', path: '/assets', method: 'POST', inScope: false, statuses: [404], requestMimes: ['text/plain'], responseMimes: ['text/css'], children: [] },
  ] }];
  vi.stubGlobal('fetch', targetFetchFixture({ tree: alternateTree }));
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole('treeitem', { name: /example\.test/ }));
  await screen.findByRole('treeitem', { name: /GET \/api\/users/ });
  await user.type(screen.getByLabelText('Filter site map'), 'assets');
  await waitFor(() => expect(screen.getByRole('treeitem', { name: /POST \/assets/ })).toBeInTheDocument());
  await user.clear(screen.getByLabelText('Filter site map'));
  await user.selectOptions(screen.getByLabelText('Scope filter'), 'out');
  expect(await screen.findByRole('treeitem', { name: /POST \/assets/ })).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText('Method filter'), 'POST');
  await user.selectOptions(screen.getByLabelText('Status filter'), '4xx');
  await user.selectOptions(screen.getByLabelText('MIME filter'), 'text/css');
  expect(screen.getByRole('treeitem', { name: /POST \/assets/ })).toBeInTheDocument();
  expect(screen.queryByRole('treeitem', { name: /GET \/api\/users/ })).not.toBeInTheDocument();
});

test('opens endpoint request history and sends it to repeater', async () => {
  const onOpenHistory = vi.fn();
  const onSendToRepeater = vi.fn();
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={onOpenHistory} onSendToRepeater={onSendToRepeater} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole('treeitem', { name: /example\.test/ }));
  await user.click(await screen.findByRole('treeitem', { name: /GET \/api\/users/ }));
  await user.click(await screen.findByRole('button', { name: 'Open 42 in History' }));
  await user.click(screen.getByRole('button', { name: 'Send 42 to Repeater' }));
  expect(onOpenHistory).toHaveBeenCalledWith(42);
  expect(onSendToRepeater).toHaveBeenCalledWith(42);
});

test('shows rebuild progress, preserves a stale tree, and retries a failed rebuild', async () => {
  const fetchMock = targetFetchFixture({ rebuildStatus: 'failed' });
  vi.stubGlobal('fetch', fetchMock);
  const { rerender } = render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  expect(await screen.findByText('projection failed')).toBeInTheDocument();
  expect(screen.getByText('Stale site map')).toBeInTheDocument();
  await user.click(screen.getByRole('treeitem', { name: /example\.test/ }));
  expect(screen.getByRole('treeitem', { name: /GET \/api\/users/ })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Retry rebuild' }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/target/rebuild', expect.objectContaining({ method: 'POST' })));
  rerender(<TargetWorkspace refresh={{ sequence: 1, type: 'target.rebuild.progress' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  expect(await screen.findByRole('status')).toHaveTextContent('2 / 7');
});

test('renders an actionable empty project state and mobile landmarks', async () => {
  vi.stubGlobal('fetch', targetFetchFixture({ tree: [] }));
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  expect(await screen.findByText(/No in-scope endpoints/)).toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Scope editor' })).toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Site map' })).toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Endpoint details' })).toBeInTheDocument();
});
