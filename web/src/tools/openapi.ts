import type { SendRequest } from '../types';

export type ApiOperation = { method: string; path: string; summary: string };
export type ApiInventory = { defaultServer: string; operations: ApiOperation[] };

const methods = ['DELETE', 'GET', 'PATCH', 'POST', 'PUT'];
const maxBytes = 1_048_576;

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function validPath(path: string): boolean {
  return path.startsWith('/') && !path.startsWith('//') && path.length <= 2048 && !/[?#\\\x00-\x1f]/.test(path)
    && path.split('/').every((part) => {
      let decoded: string;
      try { decoded = decodeURIComponent(part); }
      catch { return false; }
      return decoded !== '.' && decoded !== '..' && !decoded.includes('/') && !decoded.includes('\\');
    });
}

function parseHTTPBase(raw: string): URL {
  if (/[{}]/.test(raw)) throw new Error('Base URL contains unresolved variables');
  let url: URL;
  try { url = new URL(raw); }
  catch { throw new Error('Base URL must be an absolute HTTP or HTTPS URL'); }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new Error('Base URL must be HTTP or HTTPS');
  if (url.username || url.password) throw new Error('URL credentials are not supported');
  if (url.search || url.hash) throw new Error('Base URL cannot contain a query or fragment');
  return url;
}

export function parseOpenApi(raw: string): ApiInventory {
  if (new TextEncoder().encode(raw).length > maxBytes) throw new Error('OpenAPI document exceeds 1 MiB limit');
  let parsed: unknown;
  try { parsed = JSON.parse(raw); }
  catch { throw new Error('Invalid OpenAPI JSON'); }
  if (!record(parsed) || typeof parsed.openapi !== 'string' || !/^3\.(0|1)\./.test(parsed.openapi)) {
    throw new Error('Only OpenAPI 3.0/3.1 JSON is supported');
  }
  if (!record(parsed.paths)) throw new Error('OpenAPI paths object is missing');
  const paths = Object.entries(parsed.paths);
  if (paths.length > 500) throw new Error('OpenAPI path limit is 500');
  const operations: ApiOperation[] = [];
  for (const [path, item] of paths) {
    if (!validPath(path) || !record(item)) throw new Error('Invalid OpenAPI path item');
    for (const method of methods) {
      const operation = item[method.toLowerCase()];
      if (operation === undefined) continue;
      if (!record(operation)) throw new Error('Invalid OpenAPI operation');
      operations.push({ method, path, summary: typeof operation.summary === 'string' ? operation.summary.slice(0, 200) : '' });
      if (operations.length > 2000) throw new Error('OpenAPI operation limit is 2000');
    }
  }
  let defaultServer = '';
  if (Array.isArray(parsed.servers)) {
    const first = parsed.servers[0];
    if (record(first) && typeof first.url === 'string') {
      try { defaultServer = parseHTTPBase(first.url).toString().replace(/\/$/, ''); }
      catch { /* A relative or templated server URL needs an explicit operator base. */ }
    }
  }
  return { defaultServer, operations };
}

export function buildApiDraft(base: string, operation: ApiOperation): SendRequest {
  const server = parseHTTPBase(base);
  if (!methods.includes(operation.method) || !validPath(operation.path)) throw new Error('Invalid API operation');
  const path = operation.path.replace(/\{([A-Za-z0-9_.-]{1,64})\}/g, (_match, name: string) => `__${name}__`);
  if (/[{}]/.test(path)) throw new Error('Unsupported path parameter name');
  const prefix = server.pathname.replace(/\/$/, '');
  const target = new URL(server.toString());
  target.pathname = `${prefix}${path}`;
  return { method: operation.method, url: target.toString(), headers: {}, body: '' };
}
