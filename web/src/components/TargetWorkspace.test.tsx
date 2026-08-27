import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
// @ts-expect-error Vitest executes this test in Node, while the web build excludes Node types.
import { readFileSync } from 'node:fs';
import type { TargetTreeNode } from '../types';
import { TargetWorkspace } from './TargetWorkspace';

const styles = readFileSync('src/styles.css', 'utf8');

const endpoint = {
  id: 7, scheme: 'https', host: 'example.test', port: 443, path: '/api/users', method: 'GET', inScope: true,
  firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 3, statuses: [200, 401],
  requestMimes: ['application/json'], responseMimes: ['application/json'],
  parseDiagnostics: ['json depth limit reached', 'field limit reached', 'multipart field limit reached', 'must remain hidden'],
  errorSeen: false, latestExchangeId: 42,
};

const tree: TargetTreeNode[] = [
  {
    id: 0, scheme: 'https', host: 'example.test', port: 443, path: '', method: '', inScope: true, statuses: [200, 401, 404, 500], requestMimes: ['application/json', 'text/plain'], responseMimes: ['application/json', 'text/css', 'text/html'], count: 6, lastSeen: '2026-08-20T10:05:00Z', children: [
      { id: 0, scheme: '', host: '', port: 0, path: 'api', method: '', inScope: true, statuses: [200, 401], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 3, lastSeen: '2026-08-20T10:05:00Z', children: [
        { id: 0, scheme: '', host: '', port: 0, path: 'users', method: '', inScope: true, statuses: [200, 401], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 3, lastSeen: '2026-08-20T10:05:00Z', children: [
          { id: 7, scheme: '', host: '', port: 0, path: '', method: 'GET', inScope: true, statuses: [200, 401], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 3, lastSeen: '2026-08-20T10:05:00Z', children: [] },
        ] },
      ] },
      { id: 0, scheme: '', host: '', port: 0, path: 'assets', method: '', inScope: false, statuses: [404], requestMimes: ['text/plain'], responseMimes: ['text/css'], count: 1, lastSeen: '2026-08-20T10:04:00Z', children: [
        { id: 8, scheme: '', host: '', port: 0, path: '', method: 'POST', inScope: false, statuses: [404], requestMimes: ['text/plain'], responseMimes: ['text/css'], count: 1, lastSeen: '2026-08-20T10:04:00Z', children: [] },
      ] },
      { id: 0, scheme: '', host: '', port: 0, path: 'admin', method: '', inScope: true, statuses: [500], requestMimes: ['application/json'], responseMimes: ['text/html'], count: 1, lastSeen: '2026-08-20T10:03:00Z', children: [
        { id: 9, scheme: '', host: '', port: 0, path: '', method: 'DELETE', inScope: true, statuses: [500], requestMimes: ['application/json'], responseMimes: ['text/html'], count: 1, lastSeen: '2026-08-20T10:03:00Z', children: [] },
      ] },
    ],
  },
  {
    id: 0, scheme: 'https', host: 'outside.test', port: 443, path: '', method: '', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [
      { id: 0, scheme: '', host: '', port: 0, path: 'api', method: '', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [
        { id: 0, scheme: '', host: '', port: 0, path: 'profile', method: '', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [
          { id: 10, scheme: '', host: '', port: 0, path: '', method: 'PATCH', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [] },
        ] },
      ] },
    ],
  },
];

const scope = { version: 3, rules: [
  { id: 11, enabled: true, action: 'include', scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '/' },
  { id: 12, enabled: false, action: 'exclude', scheme: '', hostPattern: 'disabled.test', port: 0, pathPrefix: '/' },
] };
const changedScope = { version: 4, rules: [{ id: 18, enabled: true, action: 'exclude', scheme: '', hostPattern: 'changed.test', port: 0, pathPrefix: '/' }] };
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

function targetFetchFixture(options: { rebuildStatus?: 'idle' | 'building' | 'failed'; tree?: TargetTreeNode[] } = {}): ReturnType<typeof vi.fn> {
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
    if (url.pathname === '/api/target/rebuild' && init?.method === 'POST') { retried = true; return response({ id: 5, scopeVersion: 3, activeScopeVersion: 2, status: 'building', processed: 0, total: 7, error: '' }, 202); }
    if (url.pathname === '/api/target/endpoints/7') return response(endpoint);
    if (url.pathname === '/api/target/endpoints/7/requests') return response([{ exchangeId: 42, startedAt: '2026-08-20T10:05:00Z', status: 200, error: false }]);
    if (url.pathname === '/api/target/endpoints/7/parameters') return response([{ location: 'json', name: 'user.email', valueType: 'string', firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 3 }]);
    throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url.pathname}`);
  });
}

function scopeConflictFetchFixture(): ReturnType<typeof vi.fn> {
  const fixture = targetFetchFixture();
  let conflicted = false;
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/scope/rules' && init?.method === 'PUT') { conflicted = true; return response({}, 409); }
    if (url.pathname === '/api/scope/rules' && (!init?.method || init.method === 'GET') && conflicted) return response(changedScope);
    return fixture(input, init);
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((nextResolve, nextReject) => { resolve = nextResolve; reject = nextReject; });
  return { promise, resolve, reject };
}

function paths(fetchMock: ReturnType<typeof vi.fn>) { return fetchMock.mock.calls.map(([input]) => new URL(String(input), 'http://localhost').pathname); }
async function expandTreeItem(user: ReturnType<typeof userEvent.setup>, name: string | RegExp) {
  const nameMatcher = typeof name === 'string' ? new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?:$| )`) : name;
  const item = await screen.findByRole('treeitem', { name: nameMatcher });
  if (item.getAttribute('aria-expanded') === 'false') await user.click(item);
  return item;
}
async function expandExampleUsers(user: ReturnType<typeof userEvent.setup>) {
  await expandTreeItem(user, 'example.test:443');
  await expandTreeItem(user, 'api');
  await expandTreeItem(user, 'users');
}

beforeEach(() => vi.stubGlobal('fetch', targetFetchFixture()));
afterEach(() => vi.unstubAllGlobals());

test('loads site-map parameter metadata without values and bounds diagnostics', async () => {
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await expandExampleUsers(user);
  await user.click(screen.getByRole('treeitem', { name: /^GET \/api\/users/ }));
  expect(await screen.findByText('user.email')).toBeInTheDocument();
  expect(screen.getByText('3 occurrences')).toBeInTheDocument();
  expect(screen.getByText(`${new Date('2026-08-20T10:00:00Z').toLocaleString()} to ${new Date('2026-08-20T10:05:00Z').toLocaleString()}`)).toBeInTheDocument();
  expect(screen.getByText('multipart field limit reached')).toBeInTheDocument();
  expect(screen.queryByText('must remain hidden')).not.toBeInTheDocument();
  expect(screen.queryByText('secret@example.test')).not.toBeInTheDocument();
});

test('saves versioned scope rules, reloads changed conflict state, and renders disabled excludes', async () => {
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
  expect(await screen.findByText('v4')).toBeInTheDocument();
  expect(screen.getByRole('group', { name: 'exclude' })).toBeInTheDocument();
  expect(screen.getByDisplayValue('changed.test')).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([input, init]) => new URL(String(input), 'http://localhost').pathname === '/api/scope/rules' && (!init?.method || init.method === 'GET'))).toHaveLength(2);
});

test('renders disabled exclude rules from the server', async () => {
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  expect(await screen.findByDisplayValue('disabled.test')).toBeInTheDocument();
  expect(screen.getByRole('group', { name: 'exclude' })).toBeInTheDocument();
  expect(screen.getAllByRole('checkbox')[1]).not.toBeChecked();
});

test('independently filters site map text, scope, method, status family, and MIME metadata', async () => {
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await expandTreeItem(user, 'example.test:443');
  const text = screen.getByLabelText('Filter site map');
  const scopeFilter = screen.getByLabelText('Scope filter');
  const method = screen.getByLabelText('Method filter');
  const status = screen.getByLabelText('Status filter');
  const mime = screen.getByLabelText('MIME filter');
  await user.type(text, 'assets');
  await expandTreeItem(user, 'assets');
  await waitFor(() => expect(screen.getByRole('treeitem', { name: /^POST \/assets/ })).toBeInTheDocument());
  expect(screen.queryByRole('treeitem', { name: /^GET \/api\/users/ })).not.toBeInTheDocument();
  await user.clear(text);
  await user.selectOptions(scopeFilter, 'in');
  await expandExampleUsers(user);
  expect(screen.getByRole('treeitem', { name: /^GET \/api\/users/ })).toBeInTheDocument();
  expect(screen.queryByRole('treeitem', { name: /^POST \/assets/ })).not.toBeInTheDocument();
  await user.selectOptions(scopeFilter, 'out');
  await expandTreeItem(user, 'example.test:443');
  await expandTreeItem(user, 'assets');
  expect(screen.getByRole('treeitem', { name: /^POST \/assets/ })).toBeInTheDocument();
  expect(screen.queryByRole('treeitem', { name: /^DELETE \/admin/ })).not.toBeInTheDocument();
  await user.selectOptions(scopeFilter, 'all');
  await user.selectOptions(method, 'POST');
  expect(screen.getByRole('treeitem', { name: /^POST \/assets/ })).toBeInTheDocument();
  expect(screen.queryByRole('treeitem', { name: /^PATCH \/profile/ })).not.toBeInTheDocument();
  await user.selectOptions(method, 'all');
  await user.selectOptions(status, '5xx');
  await expandTreeItem(user, 'example.test:443');
  await expandTreeItem(user, 'admin');
  expect(screen.getByRole('treeitem', { name: /^DELETE \/admin/ })).toBeInTheDocument();
  expect(screen.queryByRole('treeitem', { name: /^GET \/api\/users/ })).not.toBeInTheDocument();
  await user.selectOptions(status, 'all');
  await user.selectOptions(mime, 'text/css');
  expect(screen.getByRole('treeitem', { name: /^POST \/assets/ })).toBeInTheDocument();
  expect(screen.queryByRole('treeitem', { name: /^DELETE \/admin/ })).not.toBeInTheDocument();
});

test('derives unique tree identities from repeated zero-ID branches', async () => {
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await expandTreeItem(user, 'example.test:443');
  await expandTreeItem(user, 'outside.test:443');
  const apiNodes = await screen.findAllByRole('treeitem', { name: 'api' });
  expect(apiNodes).toHaveLength(2);
  expect(new Set(apiNodes.map((node) => node.id)).size).toBe(2);
  expect(new Set(apiNodes.map((node) => node.getAttribute('aria-owns'))).size).toBe(2);
  await user.click(apiNodes[1]);
  expect(apiNodes[0]).toHaveAttribute('aria-expanded', 'false');
  expect(apiNodes[1]).toHaveAttribute('aria-expanded', 'true');
  expect(apiNodes.filter((node) => node.getAttribute('tabindex') === '0')).toHaveLength(1);
  apiNodes[1].focus();
  await user.keyboard('{ArrowRight}');
  expect(screen.getByRole('treeitem', { name: 'profile' })).toHaveFocus();
});

test('uses owned groups and standard roving keyboard navigation in the site map', async () => {
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  const treeElement = await screen.findByRole('tree', { name: 'Target site map' });
  const host = screen.getByRole('treeitem', { name: /example\.test/ });
  expect(host).toHaveAttribute('aria-owns');
  host.focus();
  await user.keyboard('{ArrowRight}');
  expect(host).toHaveAttribute('aria-expanded', 'true');
  expect(document.getElementById(host.getAttribute('aria-owns')!)).toHaveAttribute('role', 'group');
  expect(within(treeElement).getByRole('group')).toBeInTheDocument();
  await user.keyboard('{ArrowDown}');
  const api = screen.getByRole('treeitem', { name: 'api' });
  expect(api).toHaveFocus();
  await user.keyboard('{ArrowRight}');
  expect(api).toHaveAttribute('aria-expanded', 'true');
  await user.keyboard('{ArrowDown}');
  const users = screen.getByRole('treeitem', { name: 'users' });
  expect(users).toHaveFocus();
  await user.keyboard('{ArrowRight}');
  await user.keyboard('{ArrowDown}');
  expect(screen.getByRole('treeitem', { name: /^GET \/api\/users/ })).toHaveFocus();
  await user.keyboard('{ArrowLeft}');
  expect(users).toHaveFocus();
  expect(users).toHaveAttribute('aria-expanded', 'true');
  await user.keyboard('{ArrowLeft}');
  expect(users).toHaveFocus();
  expect(users).toHaveAttribute('aria-expanded', 'false');
  await user.keyboard('{ArrowLeft}');
  expect(api).toHaveFocus();
});

test('loads exactly the resources required by every refresh event', async () => {
  const fetchMock = targetFetchFixture();
  vi.stubGlobal('fetch', fetchMock);
  const props = { onOpenHistory: vi.fn(), onSendToRepeater: vi.fn() };
  const { rerender } = render(<TargetWorkspace {...props} refresh={{ sequence: 0, type: 'initial' }} />);
  await screen.findByRole('treeitem', { name: /example\.test/ });
  expect(paths(fetchMock)).toEqual(['/api/scope/rules', '/api/target/tree', '/api/target/rebuild']);
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 1, type: 'target.endpoint.updated' }} />);
  await waitFor(() => expect(paths(fetchMock)).toHaveLength(4));
  expect(paths(fetchMock).slice(-1)).toEqual(['/api/target/tree']);
  for (const [sequence, type] of [[2, 'target.rebuild.started'], [3, 'target.rebuild.progress'], [4, 'target.rebuild.failed']] as const) {
    rerender(<TargetWorkspace {...props} refresh={{ sequence, type }} />);
    await waitFor(() => expect(paths(fetchMock)).toHaveLength(sequence + 3));
    expect(paths(fetchMock).slice(-1)).toEqual(['/api/target/rebuild']);
  }
  for (const [sequence, type] of [[5, 'scope.changed'], [6, 'target.rebuild.completed']] as const) {
    rerender(<TargetWorkspace {...props} refresh={{ sequence, type }} />);
    await waitFor(() => expect(paths(fetchMock)).toHaveLength(sequence === 5 ? 10 : 13));
    expect(paths(fetchMock).slice(-3)).toEqual(['/api/scope/rules', '/api/target/tree', '/api/target/rebuild']);
  }
});

test('ignores slow stale refresh responses and errors after a newer event', async () => {
  const oldTree = deferred<Response>();
  const oldError = deferred<Response>();
  let treeCalls = 0;
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    if (path === '/api/scope/rules') return Promise.resolve(response(scope));
    if (path === '/api/target/rebuild') return Promise.resolve(response({ id: 4, scopeVersion: 3, activeScopeVersion: 3, status: 'idle', processed: 0, total: 0, error: '' }));
    if (path === '/api/target/tree') {
      treeCalls += 1;
      if (treeCalls === 1) return oldTree.promise;
      if (treeCalls === 2) return Promise.resolve(response([{ ...tree[0], host: 'current.test' }]));
      if (treeCalls === 3) return oldError.promise;
      return Promise.resolve(response([{ ...tree[0], host: 'newer.test' }]));
    }
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  const props = { onOpenHistory: vi.fn(), onSendToRepeater: vi.fn() };
  const { rerender } = render(<TargetWorkspace {...props} refresh={{ sequence: 0, type: 'initial' }} />);
  await waitFor(() => expect(paths(fetchMock)).toHaveLength(3));
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 1, type: 'target.endpoint.updated' }} />);
  expect(await screen.findByRole('treeitem', { name: /current\.test/ })).toBeInTheDocument();
  oldTree.resolve(response([{ ...tree[0], host: 'stale.test' }]));
  await waitFor(() => expect(screen.queryByRole('treeitem', { name: /stale\.test/ })).not.toBeInTheDocument());
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 2, type: 'target.endpoint.updated' }} />);
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 3, type: 'target.endpoint.updated' }} />);
  expect(await screen.findByRole('treeitem', { name: /newer\.test/ })).toBeInTheDocument();
  oldError.reject(new Error('stale target failure'));
  await waitFor(() => expect(screen.queryByText('stale target failure')).not.toBeInTheDocument());
});

test('opens endpoint request history and sends it to repeater', async () => {
  const onOpenHistory = vi.fn();
  const onSendToRepeater = vi.fn();
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={onOpenHistory} onSendToRepeater={onSendToRepeater} />);
  const user = userEvent.setup();
  await expandExampleUsers(user);
  await user.click(await screen.findByRole('treeitem', { name: /^GET \/api\/users/ }));
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
  await expandExampleUsers(user);
  expect(screen.getByRole('treeitem', { name: /^GET \/api\/users/ })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Retry rebuild' }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/target/rebuild', expect.objectContaining({ method: 'POST' })));
  rerender(<TargetWorkspace refresh={{ sequence: 1, type: 'target.rebuild.progress' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  expect(await screen.findByRole('status')).toHaveTextContent('2 / 7');
  expect(screen.getByRole('treeitem', { name: /^GET \/api\/users/ })).toBeInTheDocument();
});

test('renders an actionable mobile-safe empty project state with landmarks', async () => {
  vi.stubGlobal('fetch', targetFetchFixture({ tree: [] }));
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  expect(await screen.findByText(/No in-scope endpoints/)).toBeInTheDocument();
  expect(screen.getByRole('main', { name: 'Target workspace' })).toHaveClass('target-workspace');
  expect(screen.getByRole('region', { name: 'Scope editor' })).toHaveClass('scope-editor');
  expect(screen.getByRole('region', { name: 'Site map' })).toHaveClass('target-site-map');
  expect(screen.getByRole('region', { name: 'Endpoint details' })).toHaveClass('target-details');
  expect(document.querySelector('.target-filters')).toBeInTheDocument();
  expect(styles).toContain('@media (max-width: 720px) { body { overflow-x: hidden; }.target-workspace { grid-column: 1; grid-template-columns: 1fr; min-width: 0; }');
  expect(styles).toContain('.scope-editor, .target-site-map, .target-details { border-right: 0; border-bottom: 1px solid #2b3a37; overflow: visible; }');
});
