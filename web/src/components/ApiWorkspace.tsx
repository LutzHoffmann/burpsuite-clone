import { useState } from 'react';
import { buildApiDraft, parseOpenApi } from '../tools/openapi';
import type { ApiInventory } from '../tools/openapi';
import type { SendRequest } from '../types';

export type ApiWorkspaceState = { raw: string; baseUrl: string; inventory: ApiInventory | null };

type Props = {
  value: ApiWorkspaceState;
  onChange: (next: ApiWorkspaceState) => void;
  onSendToRepeater: (request: SendRequest) => void;
};

export function ApiWorkspace({ value, onChange, onSendToRepeater }: Props) {
  const [error, setError] = useState('');
  const [filter, setFilter] = useState('');

  const importSpec = () => {
    try {
      const inventory = parseOpenApi(value.raw);
      onChange({ ...value, inventory, baseUrl: inventory.defaultServer });
      setError('');
    } catch (cause) {
      onChange({ ...value, inventory: null });
      setError(cause instanceof Error ? cause.message : 'OpenAPI import failed');
    }
  };
  const importFile = async (file: File | undefined) => {
    if (!file) return;
    if (file.size > 1_048_576) { onChange({ ...value, inventory: null }); setError('OpenAPI document exceeds 1 MiB limit'); return; }
    try {
      onChange({ ...value, raw: await file.text(), inventory: null });
      setError('');
    } catch { onChange({ ...value, inventory: null }); setError('Could not read OpenAPI file'); }
  };
  const openDraft = (method: string, path: string, summary: string) => {
    try {
      onSendToRepeater(buildApiDraft(value.baseUrl, { method, path, summary }));
      setError('');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not create Repeater draft');
    }
  };

  const operations = value.inventory?.operations ?? [];
  const term = filter.trim().toLowerCase();
  const matches = term ? operations.filter((item) => `${item.method} ${item.path} ${item.summary}`.toLowerCase().includes(term)) : operations;

  return <section className="api-workspace" aria-label="API import workspace">
    <header className="api-header"><span className="eyebrow">Local API inventory</span><h1>OpenAPI import</h1><p>Import OpenAPI 3.0/3.1 JSON to browse endpoints. No scan or network request starts on import.</p></header>
    <div className="api-import">
      <label>OpenAPI JSON<textarea aria-label="OpenAPI JSON" value={value.raw} onChange={(event) => { onChange({ ...value, raw: event.target.value, inventory: null }); setError(''); }} spellCheck={false} /></label>
      <div className="api-import-actions"><label>Choose JSON file<input type="file" accept=".json,application/json" onChange={(event) => void importFile(event.target.files?.[0])} /></label><button type="button" onClick={importSpec}>Import specification</button></div>
      <p>Maximum 1 MiB, 500 paths, and 2,000 operations. YAML, external references, GraphQL, and SOAP are not imported.</p>
    </div>
    {error && <p className="api-error" role="alert">{error}</p>}
    {value.inventory && <div className="api-inventory">
      <div className="api-inventory-heading"><div><h2>{operations.length} operations</h2><p>Repeater drafts include supported parameters, JSON examples or simple required-field templates. Replace __name__ placeholders and review all values before sending.</p></div><label>API base URL<input aria-label="API base URL" value={value.baseUrl} onChange={(event) => onChange({ ...value, baseUrl: event.target.value })} placeholder="https://api.example.test/v1" /></label></div>
      <label className="api-filter">Filter endpoints<input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Method, path, or summary" /></label>
      <p className="api-count">Showing {Math.min(matches.length, 100)} of {matches.length} matching operations</p>
      <div className="api-operations">{matches.slice(0, 100).map((item) => <article key={`${item.method} ${item.path}`}><strong>{item.method}</strong><div><code>{item.path}</code><span>{item.summary || 'No summary'}</span></div><button type="button" onClick={() => openDraft(item.method, item.path, item.summary)} aria-label={`Send ${item.method} ${item.path} to Repeater`}>Send to Repeater</button></article>)}</div>
    </div>}
  </section>;
}
