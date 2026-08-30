import type { HistoryItem } from '../types';

type HistoryTableProps = {
  items: HistoryItem[];
  selectedId: number | null;
  query: string;
  scopeFilter: 'all' | 'in' | 'out';
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

export function HistoryTable({ items, selectedId, query, scopeFilter, onSelect, onSendToRepeater, onAddOriginToScope }: HistoryTableProps) {
  const normalizedQuery = query.trim().toLowerCase();
  const filteredItems = items.filter((item) => {
    const matchesQuery = !normalizedQuery || [item.method, item.host, item.path, item.query].some((value) => value.toLowerCase().includes(normalizedQuery));
    const matchesScope = scopeFilter === 'all' || (scopeFilter === 'in' ? item.inScope : !item.inScope);
    return matchesQuery && matchesScope;
  });

  return (
    <div className="request-table" role="table" aria-label="Request history">
      <div className="table-row table-header" role="row">
        <span>Method</span><span>Host</span><span>Path</span><span>Status</span>
        <span>MIME</span><span>Size</span><span>Duration</span><span>Time</span><span>Scope</span><span>Action</span>
      </div>
      {filteredItems.map((item) => {
        return <div
          className={`table-row ${item.id === selectedId ? 'selected' : ''}`}
          key={item.id}
          role="row"
        >
          <button
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
          </button>
          <span className={`scope-badge ${item.inScope ? 'in-scope' : 'out-of-scope'}`}>{item.inScope ? 'In scope' : 'Out of scope'}</span>
          <button className="add-scope-button" onClick={() => onAddOriginToScope(item)} type="button">Add {item.host} to scope</button>
        </div>;
      })}
    </div>
  );
}
