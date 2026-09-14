import type { HistoryItem } from '../types';

type HistoryTableProps = {
  items: HistoryItem[];
  selectedId: number | null;
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

export function HistoryTable({ items, selectedId, addingToScope, onSelect, onSendToRepeater, onAddOriginToScope }: HistoryTableProps) {
  return (
    <div className="request-table">
      <table aria-label="Request history">
        <thead><tr className="table-row table-header">
          <th id="history-method" scope="col">Method</th><th id="history-host" scope="col">Host</th><th id="history-path" scope="col">Path</th><th id="history-status" scope="col">Status</th>
          <th id="history-mime" scope="col">MIME</th><th id="history-size" scope="col">Size</th><th id="history-duration" scope="col">Duration</th><th id="history-time" scope="col">Time</th><th id="history-scope" scope="col">Scope</th><th id="history-action" scope="col">Action</th>
        </tr></thead>
        <tbody>{items.map((item) => <tr className={`table-row ${item.id === selectedId ? 'selected' : ''}`} key={item.id} onClick={(event) => {
          if (!(event.target as HTMLElement).closest('button')) onSelect(item.id);
        }} onDoubleClick={(event) => {
          if (!(event.target as HTMLElement).closest('button')) onSendToRepeater(item.id);
        }}>
          <td headers="history-method"><button
            aria-label={`Select ${item.method} ${item.host}${item.path}`}
            aria-pressed={item.id === selectedId}
            className="history-row-select"
            onClick={(event) => { event.stopPropagation(); onSelect(item.id); }}
            onDoubleClick={(event) => { event.stopPropagation(); onSendToRepeater(item.id); }}
            type="button"
          ><span className={`method method-${item.method.toLowerCase()}`}>{item.method}</span></button></td>
          <td headers="history-host"><span className="table-host" title={item.host}>{item.host}</span></td>
          <td headers="history-path"><span className="table-path" title={item.path}>{item.path}</span></td>
          <td headers="history-status"><span className={`status-code ${item.error ? 'error' : ''}`}>{item.status || 'ERR'}</span></td>
          <td headers="history-mime">{item.mimeType || '-'}</td>
          <td headers="history-size">{formatBytes(item.responseSize)}</td>
          <td headers="history-duration">{item.durationMs} ms</td>
          <td headers="history-time">{formatTime(item.startedAt)}</td>
          <td headers="history-scope"><span className={`scope-badge ${item.inScope ? 'in-scope' : 'out-of-scope'}`}>{item.inScope ? 'In scope' : 'Out of scope'}</span></td>
          <td headers="history-action"><button className="add-scope-button" disabled={addingToScope} onClick={(event) => { event.stopPropagation(); onAddOriginToScope(item); }} type="button">Add {item.host} to scope</button></td>
        </tr>)}</tbody>
      </table>
    </div>
  );
}
