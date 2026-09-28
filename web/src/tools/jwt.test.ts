import { describe, expect, it } from 'vitest';
import { inspectJwt } from './jwt';

const segment = (value: object) => btoa(JSON.stringify(value)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');

describe('JWT inspection', () => {
  it('decodes header and claims without claiming signature verification', () => {
    const token = `${segment({ alg: 'HS256', typ: 'JWT' })}.${segment({ sub: 'alice', exp: 123 })}.c2ln`;
    expect(inspectJwt(token)).toEqual({ header: { alg: 'HS256', typ: 'JWT' }, payload: { sub: 'alice', exp: 123 }, signaturePresent: true });
  });

  it('rejects malformed and oversized tokens', () => {
    expect(() => inspectJwt('not-a-jwt')).toThrow(/three/);
    expect(() => inspectJwt('e30.@@@.sig')).toThrow(/encoding/);
    expect(() => inspectJwt(`${segment({ alg: 'none' })}.${segment(['not an object'])}.`)).toThrow(/object/);
    expect(() => inspectJwt('x'.repeat(65_537))).toThrow(/limit/);
  });
});
