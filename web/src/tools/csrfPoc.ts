import type { SendRequest } from '../types';

const htmlEscape = (value: string) => value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');

function validateEncoded(value: string): void {
  if (/%(?![0-9a-fA-F]{2})/.test(value)) throw new Error('Invalid URL encoding');
  try { decodeURIComponent(value.replace(/\+/g, ' ')); }
  catch { throw new Error('Invalid URL encoding'); }
}

export function generateCsrfPoc(request: SendRequest): string {
  const method = request.method.toUpperCase();
  if (method !== 'GET' && method !== 'POST') throw new Error('CSRF form PoC supports GET or POST only');
  if (new TextEncoder().encode(request.url).length > 4096 || new TextEncoder().encode(request.body).length > 65_536) {
    throw new Error('Request exceeds PoC input limit');
  }
  let target: URL;
  try { target = new URL(request.url); }
  catch { throw new Error('Invalid HTTP target URL'); }
  if (target.protocol !== 'http:' && target.protocol !== 'https:') throw new Error('PoC requires an HTTP or HTTPS target');
  if (target.username || target.password) throw new Error('URL credentials are not supported');
  if (target.hash) throw new Error('URL fragments are not valid request targets');
  validateEncoded(target.search);

  let fields: URLSearchParams;
  if (method === 'GET') {
    if (request.body) throw new Error('GET form PoC cannot represent a request body');
    fields = new URLSearchParams(target.searchParams);
    target.search = '';
  } else {
    const contentType = Object.entries(request.headers).find(([name]) => name.toLowerCase() === 'content-type')?.[1]?.[0]?.split(';')[0]?.trim().toLowerCase();
    if (contentType !== 'application/x-www-form-urlencoded') throw new Error('POST PoC requires a form-urlencoded body');
    validateEncoded(request.body);
    fields = new URLSearchParams(request.body);
  }
  const entries = Array.from(fields.entries());
  if (entries.length > 100) throw new Error('PoC field limit is 100');
  const inputs = entries.map(([name, value]) => `    <input type="hidden" name="${htmlEscape(name)}" value="${htmlEscape(value)}">`).join('\n');
  return `<!doctype html>\n<html lang="en">\n<head><meta charset="utf-8"><title>Manual CSRF proof of concept</title></head>\n<body>\n  <h1>Manual CSRF proof of concept</h1>\n  <p>Submit only against a system you are authorized to test. Custom headers, cookies, and authorization are not included.</p>\n  <form method="${method.toLowerCase()}" action="${htmlEscape(target.toString())}">\n${inputs ? `${inputs}\n` : ''}    <button type="submit">Submit test request</button>\n  </form>\n</body>\n</html>\n`;
}
