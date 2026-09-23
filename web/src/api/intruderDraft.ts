import type { Exchange, SendRequest } from '../types';

export interface IntruderSource { url: string; method: string; raw: string; revision: number }

export function historyIntruderDraft(exchange: Exchange): Omit<IntruderSource, 'revision'> {
  if (exchange.requestTruncated || exchange.request.truncated || !exchange.request.raw) {
    throw new Error('History request is incomplete or not available as text.');
  }
  const query = exchange.query ? `?${exchange.query}` : '';
  return {
    url: `${exchange.scheme}://${exchange.host}${exchange.path}${query}`,
    method: exchange.method,
    raw: exchange.request.raw,
  };
}

export function repeaterIntruderDraft(request: SendRequest): Omit<IntruderSource, 'revision'> {
  const url = new URL(request.url);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) {
    throw new Error('Intruder supports HTTP/HTTPS URLs without userinfo or fragments.');
  }
  if (!/^[A-Z]+$/.test(request.method)) throw new Error('Invalid HTTP method.');
  const lines = [`${request.method} ${url.pathname}${url.search} HTTP/1.1`, `Host: ${url.host}`];
  for (const [name, values] of Object.entries(request.headers)) {
    if (/^(host|content-length|transfer-encoding)$/i.test(name)) continue;
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name)) throw new Error('Invalid header name.');
    for (const value of values) {
      if (/[\r\n]/.test(value)) throw new Error('Header values must not contain line breaks.');
      lines.push(`${name}: ${value}`);
    }
  }
  return { url: url.toString(), method: request.method, raw: `${lines.join('\r\n')}\r\n\r\n${request.body}` };
}
