import type { Exchange } from '../types';

export interface PassiveFinding {
  id: string;
  title: string;
  evidence: string;
  guidance: string;
}

function headerValues(headers: Record<string, string[]>, name: string): string[] {
  return Object.entries(headers)
    .filter(([key]) => key.toLowerCase() === name.toLowerCase())
    .flatMap(([, values]) => values);
}

export function analyzeExchange(exchange: Exchange): PassiveFinding[] {
  if (!exchange.inScope || exchange.error || exchange.status < 200 || exchange.status >= 400) return [];

  const findings: PassiveFinding[] = [];
  const headers = exchange.response.headers;
  if (exchange.scheme.toLowerCase() === 'https') {
    if (headerValues(headers, 'Strict-Transport-Security').length === 0) {
      findings.push({
        id: 'hsts-missing',
        title: 'HSTS header not observed',
        evidence: 'This HTTPS response has no Strict-Transport-Security header.',
        guidance: 'Check whether HSTS is set elsewhere for this host and whether HTTPS-only access is intended.',
      });
    }
    for (const [index, cookie] of headerValues(headers, 'Set-Cookie').entries()) {
      const first = cookie.split(';', 1)[0];
      const equals = first.indexOf('=');
      if (equals <= 0) continue;
      const name = first.slice(0, equals).trim();
      if (!name || /[\x00-\x20\x7f;,]/.test(name)) continue;
      if (!/(?:^|;)\s*Secure\s*(?:;|$)/i.test(cookie)) {
        findings.push({
          id: `cookie-secure:${index}:${name}`,
          title: 'Cookie without Secure attribute',
          evidence: `Set-Cookie for ${name} has no Secure attribute. The cookie value is not displayed.`,
          guidance: 'Verify the cookie is intended for HTTPS only; if so, set its Secure attribute.',
        });
      }
    }
  }

  const mime = (exchange.mimeType || headerValues(headers, 'Content-Type')[0] || '').toLowerCase();
  if (mime.startsWith('text/html') && headerValues(headers, 'Content-Security-Policy').length === 0) {
    findings.push({
      id: 'csp-missing',
      title: 'CSP header not observed',
      evidence: 'This HTML response has no Content-Security-Policy header.',
      guidance: 'Review whether a restrictive CSP is appropriate for this page. Its absence alone is not an exploitable finding.',
    });
  }
  return findings;
}
