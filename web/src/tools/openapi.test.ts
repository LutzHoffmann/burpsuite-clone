import { describe, expect, it } from 'vitest';
import { buildApiDraft, parseOpenApi } from './openapi';

const document = JSON.stringify({
  openapi: '3.1.0',
  servers: [{ url: 'https://api.example.test/v1' }],
  paths: {
    '/users/{id}': { get: { summary: 'Read user' }, delete: { summary: 'Delete user' }, parameters: [{ name: 'id', in: 'path' }] },
    '/users': { post: { summary: 'Create user' } },
  },
});

describe('OpenAPI import', () => {
  it('extracts supported operations without making requests', () => {
    expect(parseOpenApi(document)).toEqual({
      defaultServer: 'https://api.example.test/v1',
      operations: [
        { method: 'DELETE', path: '/users/{id}', summary: 'Delete user' },
        { method: 'GET', path: '/users/{id}', summary: 'Read user' },
        { method: 'POST', path: '/users', summary: 'Create user' },
      ],
    });
  });

  it('builds an editable request draft with visible path placeholders', () => {
    const operation = parseOpenApi(document).operations[1];
    expect(buildApiDraft('https://api.example.test/v1', operation)).toEqual({
      method: 'GET', url: 'https://api.example.test/v1/users/__id__', headers: {}, body: '',
    });
  });

  it('prepares query, header, and JSON example values without sending', () => {
    const spec = JSON.stringify({ openapi: '3.0.0', paths: { '/orders/{id}': {
      parameters: [{ name: 'page', in: 'query', example: 1 }, { name: 'X-Tenant', in: 'header', example: 'acme' }],
      post: { parameters: [{ name: 'page', in: 'query', example: 2 }, { name: 'filter', in: 'query' }],
        requestBody: { content: { 'application/json': { example: { label: 'test', active: true } } } } },
    } } });
    const operation = parseOpenApi(spec).operations[0];
    expect(buildApiDraft('https://api.example.test', operation)).toEqual({
      method: 'POST', url: 'https://api.example.test/orders/__id__?page=2&filter=__filter__',
      headers: { 'X-Tenant': ['acme'], 'Content-Type': ['application/json'] },
      body: '{\n  "label": "test",\n  "active": true\n}',
    });
  });

  it('does not include host or transport headers from a specification', () => {
    const spec = JSON.stringify({ openapi: '3.1.0', paths: { '/x': { get: {
      parameters: [{ name: 'Host', in: 'header', example: 'evil.test' }, { name: 'Connection', in: 'header', example: 'close' }, { name: 'X-Safe', in: 'header' }],
    } } } });
    expect(buildApiDraft('https://api.example.test', parseOpenApi(spec).operations[0]).headers).toEqual({ 'X-Safe': ['__X-Safe__'] });
  });

  it('ignores external references and examples beyond the draft size limit', () => {
    const spec = JSON.stringify({ openapi: '3.0.0', paths: { '/x': { post: {
      parameters: [{ $ref: '#/components/parameters/Token' }],
      requestBody: { content: { 'application/json': { example: 'a'.repeat(70_000) } } },
    } } } });
    const draft = buildApiDraft('https://api.example.test', parseOpenApi(spec).operations[0]);
    expect(draft.body).toBe('');
    expect(draft.headers).toEqual({});
  });

  it('creates a bounded JSON template from direct required schema fields', () => {
    const spec = JSON.stringify({ openapi: '3.1.0', paths: { '/users': { post: {
      requestBody: { content: { 'application/json': { schema: { type: 'object', required: ['name', 'active', 'profile'], properties: {
        name: { type: 'string' }, active: { type: 'boolean' }, profile: { type: 'object', required: ['age'], properties: { age: { type: 'integer' } } },
        optional: { type: 'string' },
      } } } } },
    } } } });
    const draft = buildApiDraft('https://api.example.test', parseOpenApi(spec).operations[0]);
    expect(JSON.parse(draft.body)).toEqual({ name: '__name__', active: false, profile: { age: 0 } });
    expect(draft.headers['Content-Type']).toEqual(['application/json']);
  });

  it('does not fabricate bodies from external schema references', () => {
    const spec = JSON.stringify({ openapi: '3.0.0', paths: { '/x': { post: { requestBody: { content: { 'application/json': { schema: { $ref: '#/components/schemas/X' } } } } } } } });
    expect(buildApiDraft('https://api.example.test', parseOpenApi(spec).operations[0]).body).toBe('');
  });

  it('never changes the base origin for a double-slash base path', () => {
    const draft = buildApiDraft('https://api.example.test//other', { method: 'GET', path: '/items', summary: '' });
    expect(new URL(draft.url).origin).toBe('https://api.example.test');
    expect(new URL(draft.url).pathname).toBe('//other/items');
  });

  it('requires an explicit base when server variables are unresolved', () => {
    const inventory = parseOpenApi(JSON.stringify({ openapi: '3.0.0', servers: [{ url: 'https://api.example.test/{version}' }], paths: { '/x': { get: {} } } }));
    expect(inventory.defaultServer).toBe('');
    expect(() => buildApiDraft('https://api.example.test/{version}', inventory.operations[0])).toThrow(/variables/);
  });

  it('rejects invalid documents and unsafe endpoint shapes', () => {
    expect(() => parseOpenApi('{')).toThrow(/JSON/);
    expect(() => parseOpenApi(JSON.stringify({ swagger: '2.0', paths: {} }))).toThrow(/OpenAPI 3/);
    expect(() => parseOpenApi(JSON.stringify({ openapi: '3.0.0', paths: { '//evil': { get: {} } } }))).toThrow(/path/);
    expect(() => parseOpenApi('x'.repeat(1_048_577))).toThrow(/limit/);
    expect(() => buildApiDraft('javascript:alert(1)', { method: 'GET', path: '/x', summary: '' })).toThrow(/HTTP/);
    expect(() => buildApiDraft('https://user:pass@example.test', { method: 'GET', path: '/x', summary: '' })).toThrow(/credentials/);
  });

  it('bounds endpoint count', () => {
    const paths = Object.fromEntries(Array.from({ length: 501 }, (_, index) => [`/p${index}`, { get: {} }]));
    expect(() => parseOpenApi(JSON.stringify({ openapi: '3.0.0', paths }))).toThrow(/limit/);
  });
});
