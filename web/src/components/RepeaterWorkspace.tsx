import { Repeater } from './Repeater';
import { requestFromDraft, tabLabel } from '../tools/repeaterTabs';
import type { RepeaterDraft, RepeaterTab } from '../tools/repeaterTabs';
import type { SendRequest } from '../types';

type Props = {
  tabs: RepeaterTab[];
  activeId: number;
  onSelect: (id: number) => void;
  onNew: () => void;
  onClose: (id: number) => void;
  onDraftChange: (id: number, draft: RepeaterDraft) => void;
  onSend: (id: number, request: SendRequest) => void;
  onSendToIntruder: (request: SendRequest) => void;
};

export function RepeaterWorkspace({ tabs, activeId, onSelect, onNew, onClose, onDraftChange, onSend, onSendToIntruder }: Props) {
  const active = tabs.find((tab) => tab.id === activeId) ?? tabs[0];
  return <div className="repeater-workspace">
    <div className="repeater-tabbar">
      <div className="repeater-tabs" role="tablist" aria-label="Repeater tabs">
        {tabs.map((tab) => <div className="repeater-tab-item" key={tab.id}>
          <button type="button" role="tab" aria-selected={tab.id === active.id} onClick={() => onSelect(tab.id)}>{tabLabel(tab)}</button>
          <button type="button" aria-label={`Close Repeater tab ${tab.id}`} disabled={tabs.length === 1 || tab.pending} onClick={() => onClose(tab.id)}>&times;</button>
        </div>)}
      </div>
      <button type="button" className="quiet-button" disabled={tabs.length >= 20} onClick={onNew}>New tab</button>
    </div>
    {active.error && <p className="api-error" role="alert">{active.error}</p>}
    <Repeater key={active.id} initialRequest={requestFromDraft(active.draft)} initialDraft={active.draft} result={active.result} pending={active.pending} onDraftChange={(draft) => onDraftChange(active.id, draft)} onSend={(request) => onSend(active.id, request)} onSendToIntruder={onSendToIntruder} />
  </div>;
}
