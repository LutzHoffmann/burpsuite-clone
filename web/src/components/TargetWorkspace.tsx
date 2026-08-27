import { useEffect, useRef, useState } from 'react';
import { getRebuildStatus, getScopeState, getTargetEndpoint, getTargetParameters, getTargetRequests, getTargetTree, retryTargetRebuild } from '../api/client';
import type { RebuildStatus, ScopeState, TargetEndpoint, TargetParameter, TargetRefresh, TargetRequestRef, TargetTreeNode } from '../types';
import { ScopeEditor } from './ScopeEditor';
import { SiteMapTree, type TreeFilters } from './SiteMapTree';

type TargetWorkspaceProps = {
  refresh: TargetRefresh;
  onOpenHistory: (exchangeId: number) => void;
  onSendToRepeater: (exchangeId: number) => void;
};

const defaultFilters: TreeFilters = { text: '', scope: 'all', method: 'all', status: 'all', mime: 'all' };

function treeHasAuthentication(nodes: readonly TargetTreeNode[]): boolean {
  return nodes.some((node) => node.statuses.some((status) => status === 401 || status === 403) || treeHasAuthentication(node.children));
}

function treeHasEndpoint(nodes: readonly TargetTreeNode[], id: number): boolean {
  return nodes.some((node) => (node.method !== '' && node.id === id) || treeHasEndpoint(node.children, id));
}

function availableValues(nodes: readonly TargetTreeNode[], values: Set<string>, field: 'method' | 'mime') {
  for (const node of nodes) {
    if (field === 'method' && node.method) values.add(node.method);
    if (field === 'mime') [...node.requestMimes, ...node.responseMimes].forEach((mime) => values.add(mime));
    availableValues(node.children, values, field);
  }
  return [...values].sort();
}

export function TargetWorkspace({ refresh, onOpenHistory, onSendToRepeater }: TargetWorkspaceProps) {
  const [scope, setScope] = useState<ScopeState | null>(null);
  const [tree, setTree] = useState<TargetTreeNode[]>([]);
  const [status, setStatus] = useState<RebuildStatus | null>(null);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [endpoint, setEndpoint] = useState<TargetEndpoint | null>(null);
  const [requests, setRequests] = useState<TargetRequestRef[]>([]);
  const [parameters, setParameters] = useState<TargetParameter[]>([]);
  const [filters, setFilters] = useState(defaultFilters);
  const [loadError, setLoadError] = useState('');
  const detailGeneration = useRef(0);
  const [detailAttempt, setDetailAttempt] = useState(0);

  const clearDetail = () => {
    detailGeneration.current += 1;
    setEndpoint(null);
    setRequests([]);
    setParameters([]);
    setLoadError('');
  };

  const replaceTree = (nextTree: TargetTreeNode[]) => {
    clearDetail();
    setTree(nextTree);
    setSelectedID((id) => id !== null && !treeHasEndpoint(nextTree, id) ? null : id);
  };

  useEffect(() => {
    let current = true;
    const load = async () => {
      try {
        setLoadError('');
        if (refresh.type === 'target.rebuild.started' || refresh.type === 'target.rebuild.progress' || refresh.type === 'target.rebuild.failed') {
          const nextStatus = await getRebuildStatus();
          if (current) setStatus(nextStatus);
          return;
        }
        if (refresh.type === 'target.endpoint.updated') {
          const nextTree = await getTargetTree();
          if (current) replaceTree(nextTree);
          return;
        }
        const [nextScope, nextTree, nextStatus] = await Promise.all([getScopeState(), getTargetTree(), getRebuildStatus()]);
        if (current) {
          setScope(nextScope);
          replaceTree(nextTree);
          setStatus(nextStatus);
        }
      } catch (error) {
        if (current) setLoadError(String(error));
      }
    };
    void load();
    return () => { current = false; };
  }, [refresh.sequence, refresh.type]);

  useEffect(() => {
    if (selectedID === null) return;
    let current = true;
    const generation = detailGeneration.current;
    const loadDetail = async () => {
      try {
        const [nextEndpoint, nextRequests, nextParameters] = await Promise.all([getTargetEndpoint(selectedID), getTargetRequests(selectedID), getTargetParameters(selectedID)]);
        if (current && generation === detailGeneration.current) {
          setEndpoint(nextEndpoint);
          setRequests(nextRequests);
          setParameters(nextParameters);
          setLoadError('');
        }
      } catch (error) {
        if (current && generation === detailGeneration.current) setLoadError(String(error));
      }
    };
    void loadDetail();
    return () => { current = false; };
  }, [selectedID, detailAttempt]);

  const stale = Boolean(status && status.scopeVersion !== status.activeScopeVersion);
  const methods = availableValues(tree, new Set(), 'method');
  const mimes = availableValues(tree, new Set(), 'mime');
  const selectEndpoint = (id: number) => {
    if (id === selectedID && endpoint) return;
    clearDetail();
    if (id === selectedID) {
      setDetailAttempt((attempt) => attempt + 1);
      return;
    }
    setSelectedID(id);
  };

  return <main className="target-workspace" aria-label="Target workspace">
    <ScopeEditor state={scope} onSaved={setScope} />
    <section aria-label="Site map" className="target-site-map">
      <div className="target-section-heading"><div><span className="eyebrow">Observed routes</span><h1>Site Map</h1></div>{stale && <span className="stale-badge">Stale site map</span>}</div>
      <div className="target-filters">
        <label>Filter site map<input aria-label="Filter site map" onChange={(event) => setFilters({ ...filters, text: event.target.value })} value={filters.text} /></label>
        <label>Scope filter<select aria-label="Scope filter" onChange={(event) => setFilters({ ...filters, scope: event.target.value as TreeFilters['scope'] })} value={filters.scope}><option value="all">All scope</option><option value="in">In scope</option><option value="out">Out of scope</option></select></label>
        <label>Method filter<select aria-label="Method filter" onChange={(event) => setFilters({ ...filters, method: event.target.value })} value={filters.method}><option value="all">All methods</option>{methods.map((method) => <option key={method}>{method}</option>)}</select></label>
        <label>Status filter<select aria-label="Status filter" onChange={(event) => setFilters({ ...filters, status: event.target.value })} value={filters.status}><option value="all">All statuses</option><option value="2xx">2xx</option><option value="3xx">3xx</option><option value="4xx">4xx</option><option value="5xx">5xx</option></select></label>
        <label>MIME filter<select aria-label="MIME filter" onChange={(event) => setFilters({ ...filters, mime: event.target.value })} value={filters.mime}><option value="all">All MIME types</option>{mimes.map((mime) => <option key={mime}>{mime}</option>)}</select></label>
      </div>
      {status?.status === 'building' && <p className="rebuild-status" role="status">Rebuilding: {status.processed} / {status.total}</p>}
      {status?.status === 'failed' && <div className="rebuild-failed" role="status"><span>{status.error || 'Rebuild failed'}</span><button onClick={() => void retryTargetRebuild().then(setStatus).catch((error) => setLoadError(String(error)))} type="button">Retry rebuild</button></div>}
      {treeHasAuthentication(tree) && <p className="auth-indicator">Authentication observed</p>}
      {loadError && <p className="api-error" role="alert">{loadError}</p>}
      {tree.length === 0 ? <div className="empty-target-state"><p>No in-scope endpoints yet.</p><p>Add an include rule, then browse through the proxy to build a Site Map.</p></div> : <SiteMapTree filters={filters} nodes={tree} onSelect={selectEndpoint} selectedId={selectedID} />}
    </section>
    <section aria-label="Endpoint details" className="target-details">
      <div className="target-section-heading"><div><span className="eyebrow">Selected route</span><h1>Endpoint</h1></div>{endpoint && <span>{endpoint.count} requests</span>}</div>
      {!endpoint ? <p className="empty-state">Select an endpoint to inspect requests and parameter metadata.</p> : <>
        <p className="endpoint-target"><strong>{endpoint.method}</strong> {endpoint.host}{endpoint.path}</p>
        <dl><div><dt>Statuses</dt><dd>{endpoint.statuses.join(', ') || '-'}</dd></div><div><dt>Last seen</dt><dd>{new Date(endpoint.lastSeen).toLocaleString()}</dd></div></dl>
        {endpoint.parseDiagnostics.length > 0 && <div className="diagnostics" aria-label="Parse diagnostics">{endpoint.parseDiagnostics.slice(0, 3).map((diagnostic) => <span key={diagnostic}>{diagnostic}</span>)}</div>}
        <h2>Parameters</h2>
        <div className="target-list">{parameters.length === 0 ? <p className="empty-state">No parameter names observed.</p> : parameters.map((parameter) => <div className="parameter-row" key={`${parameter.location}-${parameter.name}`}><strong>{parameter.name}</strong><span>{parameter.location} / {parameter.valueType}</span><span>{parameter.count} occurrences</span><small>{new Date(parameter.firstSeen).toLocaleString()} to {new Date(parameter.lastSeen).toLocaleString()}</small></div>)}</div>
        <h2>Requests</h2>
        <div className="target-list">{requests.map((request) => <div className="target-request" key={request.exchangeId}><span>{request.status || 'ERR'} at {new Date(request.startedAt).toLocaleString()}</span><div><button onClick={() => onOpenHistory(request.exchangeId)} type="button">Open {request.exchangeId} in History</button><button onClick={() => onSendToRepeater(request.exchangeId)} type="button">Send {request.exchangeId} to Repeater</button></div></div>)}</div>
      </>}
    </section>
  </main>;
}
