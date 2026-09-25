import { describe, expect, it } from 'vitest';
import { historyIntruderDraft, repeaterIntruderDraft } from './intruderDraft';
import type { Exchange } from '../types';

describe('Intruder draft handoff', () => {
  it('uses the complete captured raw History request without reconstructing it', () => {
    const raw = 'POST /search?q=x HTTP/1.1\r\nHost: example.test\r\nX-Order: one\r\nX-Order: two\r\n\r\nbody';
    const exchange = {
      scheme: 'https', host: 'example.test', path: '/search', query: 'q=x', method: 'POST',
      requestTruncated: false, request: { raw, truncated: false },
    } as Exchange;
    expect(historyIntruderDraft(exchange)).toEqual({ url: 'https://example.test/search?q=x', method: 'POST', raw });
    expect(() => historyIntruderDraft({ ...exchange, requestTruncated: true })).toThrow(/incomplete/);
  });

  it('builds an origin-form request from the edited Repeater draft', () => {
    expect(repeaterIntruderDraft({
      method: 'POST', url: 'https://example.test:8443/path?q=x', body: 'hi',
      headers: { Host: ['wrong.test'], 'Content-Length': ['999'], 'X-Test': ['one', 'two'] },
    })).toEqual({
      method: 'POST', url: 'https://example.test:8443/path?q=x',
      raw: 'POST /path?q=x HTTP/1.1\r\nHost: example.test:8443\r\nX-Test: one\r\nX-Test: two\r\n\r\nhi',
    });
    expect(() => repeaterIntruderDraft({ method: 'GET', url: 'http://example.test/', headers: { 'X-Test': ['ok\r\nEvil: yes'] }, body: '' })).toThrow(/line breaks/);
  });
});
