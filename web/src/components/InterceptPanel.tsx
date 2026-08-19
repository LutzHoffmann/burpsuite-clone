import type { InterceptItem } from '../types';

type InterceptPanelProps = {
  items: InterceptItem[];
  onForward: (id: string) => void;
  onDrop: (id: string) => void;
};

const bodySize = (body: string) => new Blob([body]).size;

export function InterceptPanel({ items, onForward, onDrop }: InterceptPanelProps) {
  return (
    <section className="intercept-panel" aria-label="Intercept Queue">
      <div className="utility-heading">
        Intercept Queue <span>{items.length}</span>
      </div>
      {items.length === 0 ? (
        <div className="intercept-empty">
          <p>No requests are waiting for action.</p>
          <div className="intercept-actions">
            <button type="button" disabled>Forward</button>
            <button type="button" disabled>Drop</button>
          </div>
        </div>
      ) : items.map((item) => (
        <article className="intercept-item" key={item.id}>
          <div className="intercept-request"><b className={`method method-${item.method.toLowerCase()}`}>{item.method}</b><span>{item.url}</span></div>
          <div className="intercept-meta">{Object.keys(item.headers).length} headers · {bodySize(item.body)} B body</div>
          <div className="intercept-actions">
            <button type="button" onClick={() => onForward(item.id)}>Forward</button>
            <button className="danger-button" type="button" onClick={() => onDrop(item.id)}>Drop</button>
          </div>
        </article>
      ))}
    </section>
  );
}
