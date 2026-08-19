import type { HistoryItem } from '../types';

type HistoryTableProps = {
  items: HistoryItem[];
  selectedId: number | null;
  onSelect: (id: number) => void;
  onSendToRepeater: (id: number) => void;
};

function formatBytes(bytes: number) {
  return bytes >= 1024 ? `${(bytes / 1024).toFixed(1)} kB` : `${bytes} B`;
}

function formatTime(startedAt: string) {
  return new Date(startedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export function HistoryTable({ items, selectedId, onSelect, onSendToRepeater }: HistoryTableProps) {
  return (
    <div className="request-table" role="table" aria-label="Request history">
      <div className="table-row table-header" role="row">
        <span>Method</span><span>Host</span><span>Path</span><span>Status</span>
        <span>MIME</span><span>Size</span><span>Duration</span><span>Time</span>
      </div>
      {items.map((item) => (
        <button
          className={`table-row ${item.id === selectedId ? 'selected' : ''}`}
          key={item.id}
          onClick={() => onSelect(item.id)}
          onDoubleClick={() => onSendToRepeater(item.id)}
          role="row"
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
      ))}
    </div>
  );
}
