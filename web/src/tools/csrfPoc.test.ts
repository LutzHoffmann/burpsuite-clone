import { describe, expect, it } from 'vitest';
import { generateCsrfPoc } from './csrfPoc';
import type { SendRequest } from '../types';

const request = (overrides: Partial<SendRequest> = {}): SendRequest => ({ method: 'POST', url: 'https://example.test/update?mode=1', headers: { 'Content-Type': ['application/x-www-form-urlencoded; charset=UTF-8'] }, body: 'name=Alice&name=Bob', ...overrides });

describe('CSRF PoC generator', () => {
  it('creates a manual POST form and preserves repeated body parameters', () => {
    const html = generateCsrfPoc(request());
    expect(html).toContain('method="post"');
    expect(html).toContain('action="https://example.test/update?mode=1"');
    expect(html).toContain('name="name" value="Alice"');
    expect(html).toContain('name="name" value="Bob"');
    expect(html).not.toContain('<script');
  });

  it('turns GET query parameters into hidden fields', () => {
    const html = generateCsrfPoc(request({ method: 'GET', url: 'https://example.test/search?q=one&q=two', body: '', headers: {} }));
    expect(html).toContain('method="get"');
    expect(html).toContain('action="https://example.test/search"');
    expect(html).toContain('name="q" value="one"');
    expect(html).toContain('name="q" value="two"');
  });

  it('escapes attacker-controlled values and never auto-submits', () => {
    const html = generateCsrfPoc(request({ body: 'x=%22%3E%3Cscript%3Ealert(1)%3C%2Fscript%3E' }));
    expect(html).toContain('value="&quot;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"');
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('submit()');
    expect(html).not.toContain('Authorization');
  });

  it('rejects unsupported and unsafe request shapes', () => {
    expect(() => generateCsrfPoc(request({ method: 'PUT' }))).toThrow(/GET or POST/);
    expect(() => generateCsrfPoc(request({ url: 'file:///tmp/x' }))).toThrow(/HTTP/);
    expect(() => generateCsrfPoc(request({ url: 'https://user:pass@example.test/x' }))).toThrow(/credentials/);
    expect(() => generateCsrfPoc(request({ headers: { 'Content-Type': ['application/json'] } }))).toThrow(/form-urlencoded/);
    expect(() => generateCsrfPoc(request({ body: 'x=%GG' }))).toThrow(/encoding/);
    expect(() => generateCsrfPoc(request({ body: 'x=1'.repeat(22_000) }))).toThrow(/limit/);
  });
});
