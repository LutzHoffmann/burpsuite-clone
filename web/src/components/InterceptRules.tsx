import { useEffect, useState } from 'react';
import type { InterceptConfig, InterceptRule, ReplacementRule } from '../types';

export const matchAllRule = (): InterceptRule => ({ enabled: true, method: '', hostContains: '', pathContains: '', mimeContains: '', statusCode: 0 });
const emptyReplacements: ReplacementRule[] = [];
const emptyFilters: InterceptRule[] = [];
const defaultResponseRules = [matchAllRule()];
type Props = { config: InterceptConfig; disabled: boolean; onSave: (patch: Partial<InterceptConfig>) => void };
const commonFilters = [['hostContains', 'Host contains'], ['pathContains', 'Path contains'], ['mimeContains', 'MIME contains']] as const;
const filters = [['method', 'HTTP method'], ...commonFilters] as const;

function FilterEditor({ title, rules, disabled, onSave }: { title: 'Request' | 'Response'; rules: InterceptRule[]; disabled: boolean; onSave: (rules: InterceptRule[]) => void }) {
  const [draft, setDraft] = useState(rules);
  const savedRules = JSON.stringify(rules);
  useEffect(() => setDraft(JSON.parse(savedRules) as InterceptRule[]), [savedRules]);
  const change = (index: number, patch: Partial<InterceptRule>) => setDraft((current) => current.map((rule, i) => i === index ? { ...rule, ...patch } : rule));
  return <details className="intercept-rule-section">
    <summary>{title} filters</summary>
    <p>Any enabled rule can match; fields within a rule must all match. Empty fields mean any value. Only in-scope traffic is paused.</p>
    <fieldset disabled={disabled} className="intercept-rule-fields">
      {draft.map((rule, index) => <fieldset className="scope-rule" key={index}>
        <legend>{title} filter {index + 1}</legend>
        <label><input type="checkbox" checked={rule.enabled} onChange={(event) => change(index, { enabled: event.target.checked })} />Enabled</label>
        {filters.map(([key, label]) => <label key={key}>{label}<input aria-label={`${title} filter ${index + 1} ${label}`} value={rule[key]} placeholder="Any" onChange={(event) => change(index, { [key]: event.target.value })} /></label>)}
        {title === 'Response' && <label>Status code<input aria-label={`Response filter ${index + 1} Status code`} type="number" min="0" max="599" value={rule.statusCode ?? 0} onChange={(event) => change(index, { statusCode: Number(event.target.value) })} /><small>0 = any</small></label>}
        <button type="button" className="quiet-button" aria-label={`Delete ${title.toLowerCase()} filter ${index + 1}`} onClick={() => setDraft((current) => current.filter((_, i) => i !== index))}>Delete</button>
      </fieldset>)}
      <div className="scope-actions">
        <button type="button" onClick={() => setDraft((current) => [...current, matchAllRule()])}>Add {title.toLowerCase()} filter</button>
        <button type="button" className="primary-button" onClick={() => onSave(draft)}>Save {title.toLowerCase()} filters</button>
        <button type="button" onClick={() => setDraft(rules)}>Reset {title.toLowerCase()} filters</button>
      </div>
    </fieldset>
  </details>;
}

function ReplacementEditor({ rules, disabled, onSave }: { rules: ReplacementRule[]; disabled: boolean; onSave: (rules: ReplacementRule[]) => void }) {
  const [draft, setDraft] = useState(rules);
  const savedRules = JSON.stringify(rules);
  useEffect(() => setDraft(JSON.parse(savedRules) as ReplacementRule[]), [savedRules]);
  const change = (index: number, patch: Partial<ReplacementRule>) => setDraft((current) => current.map((rule, i) => i === index ? { ...rule, ...patch } : rule));
  const move = (index: number, offset: number) => setDraft((current) => {
    const next = [...current];
    [next[index], next[index + offset]] = [next[index + offset], next[index]];
    return next;
  });
  return <details className="intercept-rule-section">
    <summary>Match and replace</summary>
    <p>Runs top to bottom before manual interception, only in scope. Regex uses Go syntax and ${'{1}'} capture replacements. Changes require Save; invalid rules are rejected by the server.</p>
    <fieldset disabled={disabled} className="intercept-rule-fields">
      {draft.map((rule, index) => <fieldset className="scope-rule replacement-rule" key={index}>
        <legend>Replacement {index + 1}</legend>
        <label><input type="checkbox" checked={rule.enabled} onChange={(event) => change(index, { enabled: event.target.checked })} />Enabled</label>
        <label>ID<input value={rule.id} onChange={(event) => change(index, { id: event.target.value })} /></label>
        <label>Direction<select value={rule.direction} onChange={(event) => change(index, { direction: event.target.value as ReplacementRule['direction'], ...(event.target.value === 'response' && rule.target === 'url' ? { target: 'header' } : {}) })}><option value="request">Request</option><option value="response">Response</option></select></label>
        <label>Target<select value={rule.target} onChange={(event) => change(index, { target: event.target.value as ReplacementRule['target'] })}>{rule.direction === 'request' && <option value="url">URL</option>}<option value="header">Header</option><option value="body">Body</option></select></label>
        <label>Header<input disabled={rule.target !== 'header'} value={rule.header} onChange={(event) => change(index, { header: event.target.value })} /></label>
        <label>Pattern<input value={rule.pattern} onChange={(event) => change(index, { pattern: event.target.value })} /></label>
        <label>Replacement<input value={rule.replacement} onChange={(event) => change(index, { replacement: event.target.value })} /></label>
        <label className="rule-checkbox"><input type="checkbox" checked={rule.regex} onChange={(event) => change(index, { regex: event.target.checked })} />Regex</label>
        {commonFilters.map(([key, label]) => <label key={key}>{label}<input value={rule[key]} placeholder="Any" onChange={(event) => change(index, { [key]: event.target.value })} /></label>)}
        <div className="scope-actions">
          <button type="button" aria-label={`Move replacement ${index + 1} up`} disabled={index === 0} onClick={() => move(index, -1)}>Up</button>
          <button type="button" aria-label={`Move replacement ${index + 1} down`} disabled={index === draft.length - 1} onClick={() => move(index, 1)}>Down</button>
          <button type="button" aria-label={`Delete replacement ${index + 1}`} onClick={() => setDraft((current) => current.filter((_, i) => i !== index))}>Delete</button>
        </div>
      </fieldset>)}
      <div className="scope-actions">
        <button type="button" onClick={() => setDraft((current) => [...current, { id: crypto.randomUUID(), enabled: true, direction: 'request', target: 'header', header: '', pattern: '', replacement: '', regex: false, hostContains: '', pathContains: '', mimeContains: '' }])}>Add replacement</button>
        <button type="button" className="primary-button" onClick={() => onSave(draft)}>Save replacements</button>
        <button type="button" onClick={() => setDraft(rules)}>Reset replacements</button>
      </div>
    </fieldset>
  </details>;
}

export function InterceptRules({ config, disabled, onSave }: Props) {
  return <section className="intercept-rules" aria-label="Interception rules">
    <p>Binary, encoded, streaming and oversized bodies bypass body editing and replacements without losing bytes. Headers remain editable where protocol-safe. Dropping a response returns a local 502.</p>
    <FilterEditor title="Request" rules={config.rules ?? emptyFilters} disabled={disabled} onSave={(rules) => onSave({ rules })} />
    <FilterEditor title="Response" rules={config.responseRules === undefined ? defaultResponseRules : config.responseRules ?? emptyFilters} disabled={disabled} onSave={(responseRules) => onSave({ responseRules })} />
    <ReplacementEditor rules={config.replacementRules ?? emptyReplacements} disabled={disabled} onSave={(replacementRules) => onSave({ replacementRules })} />
  </section>;
}
