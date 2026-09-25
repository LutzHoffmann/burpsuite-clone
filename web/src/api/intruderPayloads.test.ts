import { describe, expect, it } from 'vitest';
import { convertPayloadEditor, displayPayloads, encodePayloadEditor } from './intruderPayloads';

describe('Intruder payload byte modes', () => {
  it('encodes complete hex byte pairs and preserves empty payloads', () => {
    expect(encodePayloadEditor({ mode: 'hex', value: '00 ff\n\n41 42' })).toEqual(['AP8=', '', 'QUI=']);
    expect(() => encodePayloadEditor({ mode: 'hex', value: '0 f' })).toThrow(/complete byte pairs/);
    expect(() => encodePayloadEditor({ mode: 'hex', value: 'zz' })).toThrow(/complete byte pairs/);
  });

  it('does not discard binary payloads when loading or switching modes', () => {
    expect(displayPayloads(['AP8=', 'QUI='])).toEqual({ mode: 'hex', value: '00 ff\n41 42' });
    expect(() => convertPayloadEditor({ mode: 'hex', value: '00 ff' }, 'text')).toThrow(/Binary/);
    expect(convertPayloadEditor({ mode: 'text', value: 'ä' }, 'hex')).toEqual({ mode: 'hex', value: 'c3 a4' });
    expect(convertPayloadEditor({ mode: 'hex', value: 'c3 a4' }, 'text')).toEqual({ mode: 'text', value: 'ä' });
  });
});
