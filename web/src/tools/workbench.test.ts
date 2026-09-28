import { describe, expect, it } from 'vitest';
import { transform, compareLines, analyzeToken } from './workbench';

describe('Decoder transforms', () => {
  it('round-trips Unicode through Base64 and hex', () => {
    expect(transform(transform('Grüße', 'base64', 'encode'), 'base64', 'decode')).toBe('Grüße');
    expect(transform(transform('Grüße', 'hex', 'encode'), 'hex', 'decode')).toBe('Grüße');
  });

  it('handles URL and HTML escaping', () => {
    expect(transform('a b&c', 'url', 'encode')).toBe('a%20b%26c');
    expect(transform('a%20b%26c', 'url', 'decode')).toBe('a b&c');
    expect(transform('<b title="x">&', 'html', 'encode')).toBe('&lt;b title=&quot;x&quot;&gt;&amp;');
    expect(transform('&lt;b&gt;&amp;', 'html', 'decode')).toBe('<b>&');
  });

  it('rejects malformed encodings rather than silently replacing bytes', () => {
    expect(() => transform('A===', 'base64', 'decode')).toThrow();
    expect(() => transform('ff', 'hex', 'decode')).toThrow();
    expect(() => transform('%GG', 'url', 'decode')).toThrow();
  });

  it('bounds input size', () => {
    expect(() => transform('a'.repeat(1_048_577), 'url', 'encode')).toThrow(/limit/i);
  });
});

describe('Comparer', () => {
  it('shows unchanged, removed, and added lines', () => {
    expect(compareLines('alpha\nbeta\ngamma', 'alpha\ndelta\ngamma')).toEqual([
      { kind: 'equal', text: 'alpha' },
      { kind: 'removed', text: 'beta' },
      { kind: 'added', text: 'delta' },
      { kind: 'equal', text: 'gamma' },
    ]);
  });

  it('bounds line count', () => {
    expect(() => compareLines('x\n'.repeat(501), '')).toThrow(/limit/i);
  });
});

describe('Token analysis', () => {
  it('reports basic distribution metrics without judging security', () => {
    expect(analyzeToken('aaaa')).toEqual({ length: 4, unique: 1, entropy: 0 });
    expect(analyzeToken('ab')).toEqual({ length: 2, unique: 2, entropy: 1 });
  });

  it('rejects oversized input', () => {
    expect(() => analyzeToken('x'.repeat(1_048_577))).toThrow(/limit/i);
  });
});
