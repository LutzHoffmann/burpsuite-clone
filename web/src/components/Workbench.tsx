import { useState } from 'react';
import { analyzeToken, compareLines, transform } from '../tools/workbench';
import type { DiffLine, Direction, Encoding } from '../tools/workbench';

type Tool = 'decoder' | 'comparer' | 'token';

function message(error: unknown): string {
  return error instanceof Error ? error.message : 'Operation failed';
}

export function Workbench() {
  const [tool, setTool] = useState<Tool>('decoder');
  const [input, setInput] = useState('');
  const [output, setOutput] = useState('');
  const [encoding, setEncoding] = useState<Encoding>('url');
  const [direction, setDirection] = useState<Direction>('encode');
  const [left, setLeft] = useState('');
  const [right, setRight] = useState('');
  const [diff, setDiff] = useState<DiffLine[] | null>(null);
  const [token, setToken] = useState('');
  const [metrics, setMetrics] = useState<ReturnType<typeof analyzeToken> | null>(null);
  const [error, setError] = useState('');

  const choose = (next: Tool) => { setTool(next); setError(''); };
  const runTransform = () => {
    try { setOutput(transform(input, encoding, direction)); setError(''); }
    catch (cause) { setOutput(''); setError(message(cause)); }
  };
  const runCompare = () => {
    try { setDiff(compareLines(left, right)); setError(''); }
    catch (cause) { setDiff(null); setError(message(cause)); }
  };
  const runAnalyze = () => {
    try { setMetrics(analyzeToken(token)); setError(''); }
    catch (cause) { setMetrics(null); setError(message(cause)); }
  };

  return <section className="workbench" aria-label="Local tools">
    <header className="workbench-header">
      <span className="eyebrow">Offline utilities</span>
      <h1>Workbench</h1>
      <p>Decode data, compare text, and inspect token distributions. These tools run in your browser; inputs are not uploaded or saved.</p>
    </header>
    <nav className="workbench-tabs" aria-label="Workbench tools">
      <button type="button" aria-current={tool === 'decoder' ? 'page' : undefined} onClick={() => choose('decoder')}>Decoder</button>
      <button type="button" aria-current={tool === 'comparer' ? 'page' : undefined} onClick={() => choose('comparer')}>Comparer</button>
      <button type="button" aria-current={tool === 'token' ? 'page' : undefined} onClick={() => choose('token')}>Token analysis</button>
    </nav>
    {error && <p className="workbench-error" role="alert">{error}</p>}
    {tool === 'decoder' && <section className="workbench-panel" aria-label="Decoder">
      <div className="workbench-controls">
        <label>Format<select value={encoding} onChange={(event) => { setEncoding(event.target.value as Encoding); setOutput(''); }}><option value="url">URL</option><option value="base64">Base64</option><option value="hex">Hex UTF-8</option><option value="html">HTML entities</option></select></label>
        <label>Direction<select aria-label="Direction" value={direction} onChange={(event) => { setDirection(event.target.value as Direction); setOutput(''); }}><option value="encode">Encode</option><option value="decode">Decode</option></select></label>
        <button type="button" onClick={runTransform}>Transform</button>
      </div>
      <div className="workbench-columns">
        <label>Input<textarea aria-label="Input text" value={input} onChange={(event) => { setInput(event.target.value); setOutput(''); }} spellCheck={false} /></label>
        <label>Output<textarea aria-label="Output text" value={output} readOnly spellCheck={false} /></label>
      </div>
      <button className="quiet-button" type="button" onClick={() => { setInput(output); setOutput(''); setError(''); }} disabled={!output}>Use output as input</button>
      <p className="workbench-hint">UTF-8 text only. Input limit: 1 MiB. Invalid encodings are rejected.</p>
    </section>}
    {tool === 'comparer' && <section className="workbench-panel" aria-label="Comparer">
      <div className="workbench-columns">
        <label>Left<textarea aria-label="Left text" value={left} onChange={(event) => { setLeft(event.target.value); setDiff(null); }} spellCheck={false} /></label>
        <label>Right<textarea aria-label="Right text" value={right} onChange={(event) => { setRight(event.target.value); setDiff(null); }} spellCheck={false} /></label>
      </div>
      <button type="button" onClick={runCompare}>Compare text</button>
      <p className="workbench-hint">Line comparison, up to 64 KiB and 500 lines per side. No semantic or binary comparison.</p>
      {diff && <div className="workbench-diff" aria-label="Line differences">{diff.map((line, index) => <div className={`workbench-diff-${line.kind}`} key={index}>{line.kind === 'added' ? '+' : line.kind === 'removed' ? '-' : ' '} {line.text}</div>)}</div>}
    </section>}
    {tool === 'token' && <section className="workbench-panel" aria-label="Token analysis">
      <label>Token<textarea aria-label="Token text" value={token} onChange={(event) => { setToken(event.target.value); setMetrics(null); }} spellCheck={false} /></label>
      <button type="button" onClick={runAnalyze}>Analyze token</button>
      {metrics && <dl className="workbench-metrics"><div><dt>Length</dt><dd>{metrics.length} characters</dd></div><div><dt>Distinct characters</dt><dd>{metrics.unique}</dd></div><div><dt>Observed Shannon entropy</dt><dd>{metrics.entropy.toFixed(2)} bits/character</dd></div></dl>}
      <p className="workbench-hint">A single sample's character distribution is not a security guarantee. This does not test the token generator or predictability across samples.</p>
    </section>}
  </section>;
}
