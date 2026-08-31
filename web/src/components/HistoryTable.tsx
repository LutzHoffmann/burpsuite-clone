import type { HistoryItem } from '../types';

type HistoryTableProps = {
  items: HistoryItem[];
  selectedId: number | null;
  query: string;
  scopeFilter: 'all' | 'in' | 'out';
  addingToScope: boolean;
  onSelect: (id: number) => void;
  onSendToRepeater: (id: number) => void;
  onAddOriginToScope: (item: HistoryItem) => void;
};

function formatBytes(bytes: number) {
  return bytes >= 1024 ? `${(bytes / 1024).toFixed(1)} kB` : `${bytes} B`;
}

function formatTime(startedAt: string) {
  return new Date(startedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export function HistoryTable({ items, selectedId, query, scopeFilter, addingToScope, onSelect, onSendToRepeater, onAddOriginToScope }: HistoryTableProps) {
  const normalizedQuery = query.trim().toLowerCase();
  const filteredItems = items.filter((item) => {
    const matchesQuery = !normalizedQuery || [item.method, item.host, item.path, item.query].some((value) => value.toLowerCase().includes(normalizedQuery));
    const matchesScope = scopeFilter === 'all' || (scopeFilter === 'in' ? item.inScope : !item.inScope);
    return matchesQuery && matchesScope;
  });

  return (
    <div className="request-table">
      <table aria-label="Request history">
        <thead><tr className="table-row table-header">
          <th scope="col">Method</th><th scope="col">Host</th><th scope="col">Path</th><th scope="col">Status</th>
          <th scope="col">MIME</th><th scope="col">Size</th><th scope="col">Duration</th><th scope="col">Time</th><th scope="col">Scope</th><th scope="col">Action</th>
        </tr></thead>
        <tbody>{filteredItems.map((item) => <tr className={`table-row ${item.id === selectedId ? 'selected' : ''}`} key={item.id}>
          <td className="history-cells" colSpan={8}><button
            aria-label={`Select ${item.method} ${item.host}${item.path}`}
            aria-pressed={item.id === selectedId}
            className="history-row-select"
            onClick={() => onSelect(item.id)}
            onDoubleClick={() => onSendToRepeater(item.id)}
            type="button"
          >
            <span className={`method method-${item.method.toLowerCase()}`}>{item.method}</span>
            <span className="table-host" title={item.host}>{item.host}</span>
            <span className="table-path" title={item.path}>{item.path}</span>
            <span className={`status-code ${item.error ? 'error' : ''}`}>{item.status || 'ERR'}</span>
            <span>{item.mimeType || '-'}</span>
            <span>{formatBytes(item.responseSize)}</span>
            <span>{item.durationMs} ms</span>
            <span>{formatTime(item.startedAt)}</span>
          </button></td>
          <td><span className={`scope-badge ${item.inScope ? 'in-scope' : 'out-of-scope'}`}>{item.inScope ? 'In scope' : 'Out of scope'}</span></td>
          <td><button className="add-scope-button" disabled={addingToScope} onClick={() => onAddOriginToScope(item)} type="button">Add {item.host} to scope</button></td>
        </tr>)}</tbody>
      </table>
    </div>
  );
}
