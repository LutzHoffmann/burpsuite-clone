import type { SendRequest } from '../types';

export type ApiParameter = { name: string; location: 'query' | 'header'; value: string };
export type ApiOperation = { method: string; path: string; summary: string; parameters?: ApiParameter[]; jsonBody?: string };
export type ApiInventory = { defaultServer: string; operations: ApiOperation[] };

const methods = ['DELETE', 'GET', 'PATCH', 'POST', 'PUT'];
const maxBytes = 1_048_576;
const blockedHeaders = new Set(['host', 'connection', 'content-length', 'transfer-encoding', 'te', 'trailer', 'upgrade', 'proxy-authorization', 'proxy-authenticate', 'keep-alive', 'content-type']);

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

function parameterList(value: unknown): ApiParameter[] {
  if (!Array.isArray(value)) return [];
  return value.slice(0, 50).flatMap((item): ApiParameter[] => {
    if (!record(item) || typeof item.name !== 'string' || !/^[A-Za-z0-9_.-]{1,64}$/.test(item.name)) return [];
    if (item.in !== 'query' && item.in !== 'header') return [];
    if (item.in === 'header' && blockedHeaders.has(item.name.toLowerCase())) return [];
    const example = item.example ?? (record(item.schema) ? item.schema.default : undefined);
    const value = typeof example === 'string' || typeof example === 'number' || typeof example === 'boolean'
      ? String(example) : `__${item.name}__`;
    if (value.length > 2048 || /[\r\n\x00]/.test(value)) return [];
    return [{ name: item.name, location: item.in, value }];
  });
}

function schemaTemplate(schema: unknown, name: string, depth = 0): unknown {
  if (!record(schema) || '$ref' in schema || depth > 4) return undefined;
  if (schema.type === 'string') return `__${name}__`;
  if (schema.type === 'integer' || schema.type === 'number') return 0;
  if (schema.type === 'boolean') return false;
  if (schema.type === 'array') return [];
  if (schema.type !== 'object' || !record(schema.properties)) return undefined;
  const required = Array.isArray(schema.required) ? schema.required.slice(0, 32) : [];
  const result: Record<string, unknown> = {};
  for (const key of required) {
    if (typeof key !== 'string' || key === '__proto__' || key.length > 128 || !Object.hasOwn(schema.properties, key)) continue;
    const value = schemaTemplate(schema.properties[key], key, depth + 1);
    if (value !== undefined) result[key] = value;
  }
  return result;
}

function jsonExample(operation: Record<string, unknown>): string | undefined {
  const requestBody = operation.requestBody;
  if (!record(requestBody) || !record(requestBody.content)) return undefined;
  const media = requestBody.content['application/json'];
  if (!record(media)) return undefined;
  let example = media.example;
  if (example === undefined && record(media.examples)) {
    for (const candidate of Object.values(media.examples)) {
      if (record(candidate) && 'value' in candidate) { example = candidate.value; break; }
    }
  }
  if (example === undefined) example = schemaTemplate(media.schema, 'value');
  if (example === undefined) return undefined;
  const body = JSON.stringify(example, null, 2);
  return body && body.length <= 65_536 ? body : undefined;
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
      const parameters = new Map<string, ApiParameter>();
      for (const parameter of [...parameterList(item.parameters), ...parameterList(operation.parameters)]) {
        parameters.set(`${parameter.location}:${parameter.name.toLowerCase()}`, parameter);
      }
      const entry: ApiOperation = { method, path, summary: typeof operation.summary === 'string' ? operation.summary.slice(0, 200) : '' };
      if (parameters.size) entry.parameters = [...parameters.values()];
      const body = jsonExample(operation);
      if (body !== undefined) entry.jsonBody = body;
      operations.push(entry);
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
  const headers: Record<string, string[]> = {};
  for (const parameter of operation.parameters ?? []) {
    if (!/^[A-Za-z0-9_.-]{1,64}$/.test(parameter.name) || parameter.value.length > 2048 || /[\r\n\x00]/.test(parameter.value)) continue;
    if (parameter.location === 'query') target.searchParams.append(parameter.name, parameter.value);
    if (parameter.location === 'header' && !blockedHeaders.has(parameter.name.toLowerCase()) && parameter.name !== '__proto__') {
      headers[parameter.name] = [parameter.value];
    }
  }
  const body = operation.jsonBody && operation.jsonBody.length <= 65_536 ? operation.jsonBody : '';
  if (body) headers['Content-Type'] = ['application/json'];
  return { method: operation.method, url: target.toString(), headers, body };
}
