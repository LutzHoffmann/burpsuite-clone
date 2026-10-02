import { describe, expect, it } from 'vitest';
import { analyzeExchange } from './passiveFindings';
import type { Exchange } from '../types';

function exchange(overrides: Partial<Exchange> = {}): Exchange {
  return {
    id: 1, method: 'GET', scheme: 'https', host: 'example.test', path: '/', query: '',
    status: 200, mimeType: 'text/html', requestSize: 0, responseSize: 0, durationMs: 1,
    startedAt: '', intercepted: false, error: false, inScope: true, scopeVersion: 1,
    scopeRuleId: 1, errorMessage: '', requestTruncated: false, responseTruncated: false,
    request: { headers: {}, body: '', raw: '', textSafe: true, truncated: false },
    response: { headers: {}, body: '', raw: '', textSafe: true, truncated: false },
    tags: [], note: '', ...overrides,
  };
}

describe('passive response observations', () => {
  it('limits checks to successful captured in-scope responses', () => {
    expect(analyzeExchange(exchange({ inScope: false }))).toEqual([]);
    expect(analyzeExchange(exchange({ error: true }))).toEqual([]);
    expect(analyzeExchange(exchange({ status: 404 }))).toEqual([]);
  });

  it('reports missing HTTPS and HTML policies conservatively', () => {
    expect(analyzeExchange(exchange()).map((item) => item.id)).toEqual(['hsts-missing', 'csp-missing']);
    const safe = exchange({ response: { headers: { 'strict-transport-security': ['max-age=31536000'], 'CONTENT-SECURITY-POLICY': ["default-src 'self'"] }, body: '', raw: '', textSafe: true, truncated: false } });
    expect(analyzeExchange(safe)).toEqual([]);
  });

  it('checks cookie flags without exposing cookie values', () => {
    const item = exchange({ mimeType: 'application/json', response: { headers: { 'Set-Cookie': ['session=secret-value; HttpOnly', 'safe=another-secret; Secure; HttpOnly'] }, body: '', raw: '', textSafe: true, truncated: false } });
    const findings = analyzeExchange(item);
    expect(findings.map((finding) => finding.id)).toEqual(['hsts-missing', 'cookie-secure:0:session']);
    expect(JSON.stringify(findings)).not.toContain('secret-value');
    expect(JSON.stringify(findings)).not.toContain('another-secret');
  });

  it('does not apply HTTPS checks to HTTP', () => {
    expect(analyzeExchange(exchange({ scheme: 'http', mimeType: 'application/json' }))).toEqual([]);
  });
});
