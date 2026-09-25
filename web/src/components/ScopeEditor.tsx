import { useEffect, useRef, useState } from 'react';
import { ApiError, getScopeState, replaceScopeRules } from '../api/client';
import type { ScopeAction, ScopeRule, ScopeState } from '../types';

type ScopeEditorProps = {
  state: ScopeState | null;
  onSaved: (state: ScopeState) => void;
};

const newRule = (action: ScopeAction): ScopeRule => ({
  id: 0, enabled: true, action, scheme: '', hostPattern: '', port: 0, pathPrefix: '/',
});

export function ScopeEditor({ state, onSaved }: ScopeEditorProps) {
  const [draft, setDraft] = useState<ScopeRule[]>([]);
  const [conflict, setConflict] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const appliedVersion = useRef<number | null>(null);

  useEffect(() => {
    const version = state?.version ?? null;
    if (appliedVersion.current !== version) {
      setDraft(state?.rules ?? []);
      appliedVersion.current = version;
    }
  }, [state?.version]);

  const updateRule = (index: number, changes: Partial<ScopeRule>) => {
    setDraft(draft.map((rule, ruleIndex) => ruleIndex === index ? { ...rule, ...changes } : rule));
  };

  const save = async () => {
    if (!state) return;
    setSaving(true);
    setConflict(false);
    setSaveError('');
    try {
      const saved = await replaceScopeRules(state.version, draft);
      setDraft(saved.rules);
      appliedVersion.current = saved.version;
      onSaved(saved);
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
        try {
          const latest = await getScopeState();
          setDraft(latest.rules);
          appliedVersion.current = latest.version;
          onSaved(latest);
        } catch (reloadError) {
          setSaveError(reloadError instanceof Error ? reloadError.message : 'Unable to reload scope rules');
        }
      } else {
        setSaveError(error instanceof Error ? error.message : 'Unable to save scope rules');
      }
    } finally {
      setSaving(false);
    }
  };

  return <section aria-label="Scope editor" className="scope-editor">
    <div className="target-section-heading"><div><span className="eyebrow">Authorized targets</span><h1>Scope</h1></div><small>v{state?.version ?? '-'}</small></div>
    {conflict && <p className="scope-conflict" role="alert">Scope changed in another session</p>}
    {saveError && <p className="scope-conflict" role="alert">Scope save failed: {saveError}</p>}
    <div className="scope-rules">
      {draft.map((rule, index) => <fieldset className="scope-rule" key={`${rule.id}-${index}`}>
        <legend>{rule.action}</legend>
        <label><input checked={rule.enabled} onChange={(event) => updateRule(index, { enabled: event.target.checked })} type="checkbox" /> Enabled</label>
        <label>Action<select value={rule.action} onChange={(event) => updateRule(index, { action: event.target.value as ScopeAction })}><option value="include">include</option><option value="exclude">exclude</option></select></label>
        <label>Scheme<select value={rule.scheme} onChange={(event) => updateRule(index, { scheme: event.target.value as ScopeRule['scheme'] })}><option value="">Any</option><option value="http">http</option><option value="https">https</option></select></label>
        <label>{rule.id === 0 ? 'Host pattern' : `Host pattern rule ${index + 1}`}<input value={rule.hostPattern} onChange={(event) => updateRule(index, { hostPattern: event.target.value })} /></label>
        <label>Port<input min="0" type="number" value={rule.port} onChange={(event) => updateRule(index, { port: Number(event.target.value) })} /></label>
        <label>Path prefix<input value={rule.pathPrefix} onChange={(event) => updateRule(index, { pathPrefix: event.target.value })} /></label>
        <button className="quiet-button" onClick={() => setDraft(draft.filter((_, ruleIndex) => ruleIndex !== index))} type="button">Remove rule</button>
      </fieldset>)}
    </div>
    <div className="scope-actions"><button onClick={() => setDraft([...draft, newRule('include')])} type="button">Add include rule</button><button onClick={() => setDraft([...draft, newRule('exclude')])} type="button">Add exclude rule</button><button className="primary-button" disabled={!state || saving} onClick={() => void save()} type="button">Save scope</button></div>
  </section>;
}
