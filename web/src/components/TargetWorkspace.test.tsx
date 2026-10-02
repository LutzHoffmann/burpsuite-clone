import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { act, StrictMode } from 'react';
// @ts-expect-error Vitest executes this test in Node, while the web build excludes Node types.
import { readFileSync } from 'node:fs';
import type { ScopeState, TargetTreeNode } from '../types';
import { TargetWorkspace } from './TargetWorkspace';
import { ScopeEditor } from './ScopeEditor';
import { SiteMapTree, type TreeFilters } from './SiteMapTree';

const styles = readFileSync('src/styles.css', 'utf8');

const endpoint = {
  id: 7, scheme: 'https', host: 'example.test', port: 443, path: '/api/users', method: 'GET', inScope: true,
  firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 3, statuses: [200],
  requestMimes: ['application/json'], responseMimes: ['application/json'],
  parseDiagnostics: ['json depth limit reached', 'field limit reached', 'multipart field limit reached', 'must remain hidden'],
  errorSeen: false, latestExchangeId: 42,
};

const tree: TargetTreeNode[] = [
  {
    id: 0, scheme: 'https', host: 'example.test', port: 443, path: '', method: '', inScope: true, statuses: [200, 401, 404, 500], requestMimes: ['application/json', 'text/plain'], responseMimes: ['application/json', 'text/css', 'text/html'], count: 5, lastSeen: '2026-08-20T10:05:00Z', children: [
      { id: 0, scheme: '', host: '', port: 0, path: 'api', method: '', inScope: true, statuses: [200], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 3, lastSeen: '2026-08-20T10:05:00Z', children: [
        { id: 0, scheme: '', host: '', port: 0, path: 'users', method: '', inScope: true, statuses: [200], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 3, lastSeen: '2026-08-20T10:05:00Z', children: [
          { id: 7, scheme: '', host: '', port: 0, path: '/api/users', method: 'GET', inScope: true, statuses: [200], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 3, lastSeen: '2026-08-20T10:05:00Z', children: [] },
        ] },
      ] },
      { id: 0, scheme: '', host: '', port: 0, path: 'assets', method: '', inScope: false, statuses: [401, 404], requestMimes: ['text/plain'], responseMimes: ['text/css'], count: 1, lastSeen: '2026-08-20T10:04:00Z', children: [
        { id: 8, scheme: '', host: '', port: 0, path: '/assets', method: 'POST', inScope: false, statuses: [401, 404], requestMimes: ['text/plain'], responseMimes: ['text/css'], count: 1, lastSeen: '2026-08-20T10:04:00Z', children: [] },
      ] },
      { id: 0, scheme: '', host: '', port: 0, path: 'admin', method: '', inScope: true, statuses: [500], requestMimes: ['application/json'], responseMimes: ['text/html'], count: 1, lastSeen: '2026-08-20T10:03:00Z', children: [
        { id: 9, scheme: '', host: '', port: 0, path: '/admin', method: 'DELETE', inScope: true, statuses: [500], requestMimes: ['application/json'], responseMimes: ['text/html'], count: 1, lastSeen: '2026-08-20T10:03:00Z', children: [] },
      ] },
    ],
  },
  {
    id: 0, scheme: 'https', host: 'outside.test', port: 443, path: '', method: '', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [
      { id: 0, scheme: '', host: '', port: 0, path: 'api', method: '', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [
        { id: 0, scheme: '', host: '', port: 0, path: 'profile', method: '', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [
          { id: 10, scheme: '', host: '', port: 0, path: '/api/profile', method: 'PATCH', inScope: false, statuses: [201], requestMimes: ['application/json'], responseMimes: ['application/json'], count: 1, lastSeen: '2026-08-20T10:02:00Z', children: [] },
        ] },
      ] },
    ],
  },
];

const scope: ScopeState = { version: 3, rules: [
  { id: 11, enabled: true, action: 'include', scheme: 'https', hostPattern: 'example.test', port: 443, pathPrefix: '/' },
  { id: 12, enabled: false, action: 'exclude', scheme: '', hostPattern: 'disabled.test', port: 0, pathPrefix: '/' },
] };
const changedScope: ScopeState = { version: 4, rules: [{ id: 18, enabled: true, action: 'exclude', scheme: '', hostPattern: 'changed.test', port: 0, pathPrefix: '/' }] };
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const noFilters: TreeFilters = { text: '', scope: 'all', method: 'all', status: 'all', mime: 'all' };

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

test('renders exact method paths including empty segments', async () => {
  const leaf = (id: number, path: string): TargetTreeNode => ({
    id, scheme: '', host: '', port: 0, path, method: 'GET', inScope: true,
    statuses: [200], requestMimes: [], responseMimes: [], count: 1,
    lastSeen: '2026-08-20T10:05:00Z', children: [],
  });
  const exactTree: TargetTreeNode[] = [{
    id: 0, scheme: 'https', host: 'example.test', port: 443, path: '', method: '', inScope: true,
    statuses: [200], requestMimes: [], responseMimes: [], count: 3,
    lastSeen: '2026-08-20T10:05:00Z',
    children: [leaf(21, '/api'), leaf(22, '/api/'), leaf(23, '/api//x')],
  }];
  const user = userEvent.setup();
  render(<SiteMapTree nodes={exactTree} selectedId={null} filters={noFilters} onSelect={vi.fn()} />);

  await user.click(screen.getByRole('treeitem', { name: /^example\.test:443/ }));
  expect(screen.getByRole('treeitem', { name: 'GET /api' })).toBeInTheDocument();
  expect(screen.getByRole('treeitem', { name: 'GET /api/' })).toBeInTheDocument();
  expect(screen.getByRole('treeitem', { name: 'GET /api//x' })).toBeInTheDocument();
});

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

test('preserves dirty scope edits when a same-version state object rerenders', async () => {
  const onSaved = vi.fn();
  const user = userEvent.setup();
  const { rerender } = render(<ScopeEditor onSaved={onSaved} state={scope} />);
  const host = screen.getByLabelText('Host pattern rule 1');
  await user.clear(host);
  await user.type(host, 'local.test');
  rerender(<ScopeEditor onSaved={onSaved} state={{ version: 3, rules: scope.rules.map((rule) => ({ ...rule })) }} />);
  expect(screen.getByDisplayValue('local.test')).toBeInTheDocument();
});

test.each([
  ['400 response', () => response({ error: 'bad request' }, 400), /400/],
  ['500 response', () => response({ error: 'server error' }, 500), /500/],
  ['network failure', () => Promise.reject(new TypeError('network offline')), /network offline/],
])('shows a visible scope-save alert for $0', async (_name, failure, message) => {
  vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => init?.method === 'PUT' ? failure() : response(scope)));
  const user = userEvent.setup();
  const { unmount } = render(<ScopeEditor onSaved={vi.fn()} state={scope} />);
  await user.click(screen.getByRole('button', { name: 'Save scope' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(message);
  unmount();
});

test('preserves valid detail when the selected endpoint is clicked again', async () => {
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await expandExampleUsers(user);
  const selected = screen.getByRole('treeitem', { name: /^GET \/api\/users/ });
  await user.click(selected);
  expect(await screen.findByText('user.email')).toBeInTheDocument();
  await user.click(selected);
  expect(screen.getByText('user.email')).toBeInTheDocument();
});

test('clears a detail error when the selected endpoint is retried', async () => {
  const fixture = targetFetchFixture();
  let endpointAttempts = 0;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    if (path === '/api/target/endpoints/7' && endpointAttempts++ === 0) throw new Error('detail temporarily failed');
    return fixture(input, init);
  }));
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  const user = userEvent.setup();
  await expandExampleUsers(user);
  const selected = screen.getByRole('treeitem', { name: /^GET \/api\/users/ });
  await user.click(selected);
  expect(await screen.findByRole('alert')).toHaveTextContent('detail temporarily failed');
  await user.click(selected);
  expect(await screen.findByText('user.email')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

test('invalidates delayed detail after a completed rebuild removes the endpoint', async () => {
  const delayedEndpoint = deferred<Response>();
  const delayedRequests = deferred<Response>();
  const delayedParameters = deferred<Response>();
  let treeReads = 0;
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    if (path === '/api/scope/rules') return Promise.resolve(response(scope));
    if (path === '/api/target/rebuild') return Promise.resolve(response({ id: 4, scopeVersion: 3, activeScopeVersion: 3, status: 'active', processed: 3, total: 3, error: '' }));
    if (path === '/api/target/tree') return Promise.resolve(response(treeReads++ === 0 ? tree : [tree[1]]));
    if (path === '/api/target/endpoints/7') return delayedEndpoint.promise;
    if (path === '/api/target/endpoints/7/requests') return delayedRequests.promise;
    if (path === '/api/target/endpoints/7/parameters') return delayedParameters.promise;
    throw new Error(`Unexpected request: ${path}`);
  }));
  const props = { onOpenHistory: vi.fn(), onSendToRepeater: vi.fn() };
  const { rerender } = render(<TargetWorkspace {...props} refresh={{ sequence: 0, type: 'initial' }} />);
  const user = userEvent.setup();
  await expandExampleUsers(user);
  await user.click(screen.getByRole('treeitem', { name: /^GET \/api\/users/ }));
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 1, type: 'target.rebuild.completed' }} />);
  expect(await screen.findByRole('treeitem', { name: /outside\.test/ })).toBeInTheDocument();
  delayedEndpoint.resolve(response(endpoint));
  delayedRequests.resolve(response([{ exchangeId: 42, startedAt: '2026-08-20T10:05:00Z', status: 200, error: false }]));
  delayedParameters.resolve(response([{ location: 'json', name: 'user.email', valueType: 'string', firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:05:00Z', count: 3 }]));
  await waitFor(() => expect(screen.queryByText('user.email')).not.toBeInTheDocument());
  expect(screen.queryByText(/GET example\.test\/api\/users/)).not.toBeInTheDocument();
});

test('reloads surviving endpoint details after an endpoint update replaces the tree', async () => {
  const fixture = targetFetchFixture();
  let detailReads = 0;
  let parameterReads = 0;
  let requestReads = 0;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    if (path === '/api/target/endpoints/7') {
      detailReads += 1;
      return response({ ...endpoint, count: detailReads === 1 ? 3 : 4 });
    }
    if (path === '/api/target/endpoints/7/requests') {
      requestReads += 1;
      return response([{ exchangeId: requestReads === 1 ? 42 : 43, startedAt: '2026-08-20T10:06:00Z', status: 200, error: false }]);
    }
    if (path === '/api/target/endpoints/7/parameters') {
      parameterReads += 1;
      return response([{ location: 'json', name: parameterReads === 1 ? 'user.email' : 'user.name', valueType: 'string', firstSeen: '2026-08-20T10:00:00Z', lastSeen: '2026-08-20T10:06:00Z', count: parameterReads === 1 ? 3 : 4 }]);
    }
    return fixture(input, init);
  }));
  const props = { onOpenHistory: vi.fn(), onSendToRepeater: vi.fn() };
  const { rerender } = render(<TargetWorkspace {...props} refresh={{ sequence: 0, type: 'initial' }} />);
  const user = userEvent.setup();
  await expandExampleUsers(user);
  await user.click(screen.getByRole('treeitem', { name: /^GET \/api\/users/ }));
  expect(await screen.findByText('user.email')).toBeInTheDocument();
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 1, type: 'target.endpoint.updated' }} />);
  expect(await screen.findByText('user.name')).toBeInTheDocument();
  expect(screen.getByText('4 occurrences')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Open 43 in History' })).toBeInTheDocument();
  expect(detailReads).toBe(2);
  expect(requestReads).toBe(2);
  expect(parameterReads).toBe(2);
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

test('removes aggregate branches when status-plus-MIME and scope-plus-status do not match one endpoint together', async () => {
  const { rerender } = render(<SiteMapTree filters={{ ...noFilters, status: '5xx', mime: 'text/css' }} nodes={tree} onSelect={vi.fn()} selectedId={null} />);
  expect(screen.getByText('No endpoints match these filters.')).toBeInTheDocument();
  expect(screen.queryByRole('treeitem')).not.toBeInTheDocument();
  rerender(<SiteMapTree filters={{ ...noFilters, scope: 'in', status: '4xx' }} nodes={tree} onSelect={vi.fn()} selectedId={null} />);
  expect(screen.getByText('No endpoints match these filters.')).toBeInTheDocument();
  expect(screen.queryByRole('treeitem')).not.toBeInTheDocument();
});

test('moves tree focus to a fallback only when live replacement removes a focused node', async () => {
  const onSelect = vi.fn();
  const { rerender } = render(<SiteMapTree filters={noFilters} nodes={tree} onSelect={onSelect} selectedId={7} />);
  const user = userEvent.setup();
  await expandExampleUsers(user);
  const selected = screen.getByRole('treeitem', { name: /^GET \/api\/users/ });
  act(() => selected.focus());
  rerender(<SiteMapTree filters={noFilters} nodes={[tree[1]]} onSelect={onSelect} selectedId={null} />);
  expect(await screen.findByRole('treeitem', { name: /outside\.test/ })).toHaveFocus();

  rerender(<SiteMapTree filters={noFilters} nodes={tree} onSelect={onSelect} selectedId={null} />);
  const external = document.createElement('button');
  document.body.append(external);
  act(() => external.focus());
  rerender(<SiteMapTree filters={noFilters} nodes={[tree[0]]} onSelect={onSelect} selectedId={null} />);
  expect(external).toHaveFocus();
  external.remove();
});

test('moves focus from the initially focusable tree item when live replacement removes it', async () => {
  const { rerender } = render(<SiteMapTree filters={noFilters} nodes={tree} onSelect={vi.fn()} selectedId={null} />);
  const initial = screen.getByRole('treeitem', { name: /example\.test/ });
  act(() => initial.focus());
  expect(initial).toHaveFocus();
  rerender(<SiteMapTree filters={noFilters} nodes={[tree[1]]} onSelect={vi.fn()} selectedId={null} />);
  expect(await screen.findByRole('treeitem', { name: /outside\.test/ })).toHaveFocus();
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
  act(() => host.focus());
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

test('keeps scope and tree refreshes when back-to-back rebuild events supersede only status', async () => {
  const freshScope = deferred<Response>();
  const freshTree = deferred<Response>();
  const fullStatus = deferred<Response>();
  const startedStatus = deferred<Response>();
  const failedStatus = deferred<Response>();
  let scopeReads = 0;
  let treeReads = 0;
  let statusReads = 0;
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    if (path === '/api/scope/rules') return scopeReads++ === 0 ? Promise.resolve(response(scope)) : freshScope.promise;
    if (path === '/api/target/tree') return treeReads++ === 0 ? Promise.resolve(response(tree)) : freshTree.promise;
    if (path === '/api/target/rebuild') {
      const read = statusReads++;
      if (read === 0) return Promise.resolve(response({ id: 4, scopeVersion: 3, activeScopeVersion: 3, status: 'idle', processed: 0, total: 0, error: '' }));
      if (read === 1) return fullStatus.promise;
      if (read === 2) return startedStatus.promise;
      return failedStatus.promise;
    }
    throw new Error(`Unexpected request: ${path}`);
  }));
  const props = { onOpenHistory: vi.fn(), onSendToRepeater: vi.fn() };
  const { rerender } = render(<TargetWorkspace {...props} refresh={{ sequence: 0, type: 'initial' }} />);
  await screen.findByRole('treeitem', { name: /example\.test/ });

  rerender(<TargetWorkspace {...props} refresh={{ sequence: 1, type: 'scope.changed' }} />);
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 2, type: 'target.rebuild.started' }} />);
  rerender(<TargetWorkspace {...props} refresh={{ sequence: 3, type: 'target.rebuild.failed' }} />);
  freshScope.resolve(response({ version: 9, rules: [] }));
  freshTree.resolve(response([{ ...tree[0], host: 'fresh.test' }]));
  fullStatus.resolve(response({ id: 4, scopeVersion: 9, activeScopeVersion: 9, status: 'building', processed: 1, total: 3, error: '' }));
  startedStatus.resolve(response({ id: 4, scopeVersion: 9, activeScopeVersion: 9, status: 'building', processed: 2, total: 3, error: '' }));
  failedStatus.resolve(response({ id: 4, scopeVersion: 9, activeScopeVersion: 9, status: 'failed', processed: 2, total: 3, error: 'latest rebuild failure' }));

  expect(await screen.findByText('v9')).toBeInTheDocument();
  expect(screen.getByRole('treeitem', { name: /fresh\.test/ })).toBeInTheDocument();
  expect(await screen.findByText('latest rebuild failure')).toBeInTheDocument();
  expect(screen.queryByText('2 / 3')).not.toBeInTheDocument();
});

test('keeps the second StrictMode lifecycle and back-to-back resource refreshes current', async () => {
  const scopeResponses = [deferred<Response>(), deferred<Response>(), deferred<Response>()];
  const treeResponses = [deferred<Response>(), deferred<Response>(), deferred<Response>()];
  const statusResponses = [deferred<Response>(), deferred<Response>(), deferred<Response>(), deferred<Response>(), deferred<Response>()];
  let scopeReads = 0;
  let treeReads = 0;
  let statusReads = 0;
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    if (path === '/api/scope/rules') return scopeResponses[scopeReads++]?.promise ?? Promise.reject(new Error(`Unexpected scope request ${scopeReads}`));
    if (path === '/api/target/tree') return treeResponses[treeReads++]?.promise ?? Promise.reject(new Error(`Unexpected tree request ${treeReads}`));
    if (path === '/api/target/rebuild') return statusResponses[statusReads++]?.promise ?? Promise.reject(new Error(`Unexpected status request ${statusReads}`));
    throw new Error(`Unexpected request: ${path}`);
  }));
  const props = { onOpenHistory: vi.fn(), onSendToRepeater: vi.fn() };
  const { rerender } = render(<StrictMode><TargetWorkspace {...props} refresh={{ sequence: 0, type: 'initial' }} /></StrictMode>);

  scopeResponses[0].resolve(response({ version: 1, rules: [] }));
  treeResponses[0].resolve(response([{ ...tree[0], host: 'discarded.test' }]));
  statusResponses[0].resolve(response({ id: 4, scopeVersion: 1, activeScopeVersion: 1, status: 'building', processed: 1, total: 2, error: '' }));
  scopeResponses[1].resolve(response({ version: 8, rules: [] }));
  treeResponses[1].resolve(response([{ ...tree[0], host: 'strict.test' }]));
  statusResponses[1].resolve(response({ id: 4, scopeVersion: 8, activeScopeVersion: 8, status: 'building', processed: 1, total: 2, error: '' }));

  expect(await screen.findByText('v8')).toBeInTheDocument();
  expect(screen.getByRole('treeitem', { name: /strict\.test/ })).toBeInTheDocument();
  expect(screen.getByRole('status')).toHaveTextContent('1 / 2');
  expect(screen.queryByText('discarded.test')).not.toBeInTheDocument();

  rerender(<StrictMode><TargetWorkspace {...props} refresh={{ sequence: 1, type: 'scope.changed' }} /></StrictMode>);
  rerender(<StrictMode><TargetWorkspace {...props} refresh={{ sequence: 2, type: 'target.rebuild.started' }} /></StrictMode>);
  rerender(<StrictMode><TargetWorkspace {...props} refresh={{ sequence: 3, type: 'target.rebuild.failed' }} /></StrictMode>);
  scopeResponses[2].resolve(response({ version: 9, rules: [] }));
  treeResponses[2].resolve(response([{ ...tree[0], host: 'fresh.test' }]));
  statusResponses[2].resolve(response({ id: 4, scopeVersion: 9, activeScopeVersion: 9, status: 'building', processed: 1, total: 3, error: '' }));
  statusResponses[3].resolve(response({ id: 4, scopeVersion: 9, activeScopeVersion: 9, status: 'building', processed: 2, total: 3, error: '' }));
  statusResponses[4].resolve(response({ id: 4, scopeVersion: 9, activeScopeVersion: 9, status: 'failed', processed: 2, total: 3, error: 'latest strict failure' }));

  expect(await screen.findByText('v9')).toBeInTheDocument();
  expect(screen.getByRole('treeitem', { name: /fresh\.test/ })).toBeInTheDocument();
  expect(await screen.findByText('latest strict failure')).toBeInTheDocument();
  expect(screen.queryByText('2 / 3')).not.toBeInTheDocument();
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
  expect(screen.queryByRole('main', { name: 'Target workspace' })).not.toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Target workspace' })).toHaveClass('target-workspace');
  expect(screen.getByRole('region', { name: 'Scope editor' })).toHaveClass('scope-editor');
  expect(screen.getByRole('region', { name: 'Site map' })).toHaveClass('target-site-map');
  expect(screen.getByRole('region', { name: 'Endpoint details' })).toHaveClass('target-details');
  expect(document.querySelector('.target-filters')).toBeInTheDocument();
  expect(styles).toContain('@media (max-width: 720px) { body { overflow-x: hidden; }.target-workspace { grid-column: 1; grid-template-columns: 1fr; min-width: 0; }');
  expect(styles).toContain('.scope-editor, .target-site-map, .target-details { border-right: 0; border-bottom: 1px solid #2b3a37; overflow: visible; }');
});
