import { describe, expect, it } from 'vitest';
import { byteOffsetInRaw, canonicalEditedRequest, displayRawRequest, displayRawHex, hexSelectionOffset, parseRawHex } from './intruderBytes';

describe('Intruder raw request bytes', () => {
  it('preserves body LF bytes while rebuilding edited HTTP header line endings', () => {
    expect(canonicalEditedRequest('POST / HTTP/1.1\nHost: local.test\n\nline1\nline2')).toBe('POST / HTTP/1.1\r\nHost: local.test\r\n\r\nline1\nline2');
  });

  it('maps displayed selection offsets back to original UTF-8 bytes', () => {
    const original = 'POST / HTTP/1.1\r\nHost: local.test\r\n\r\nä\nnext';
    const displayed = displayRawRequest(original);
    const selected = displayed.indexOf('ä');
    expect(byteOffsetInRaw(original, selected)).toBe(new TextEncoder().encode(original.slice(0, original.indexOf('ä'))).length);
    expect(byteOffsetInRaw(original, selected + 1)).toBe(new TextEncoder().encode(original.slice(0, original.indexOf('ä') + 1)).length);
    expect(displayed).toContain('ä\nnext');
  });

  it('round-trips arbitrary binary request bytes without text conversion', () => {
    const bytes = Uint8Array.from([0, 13, 10, 255, 128, 65]);
    const shown = displayRawHex(bytes);
    expect(parseRawHex(shown)).toEqual(bytes);
    expect(hexSelectionOffset(shown, shown.indexOf('ff'))).toBe(3);
    expect(hexSelectionOffset(shown, shown.indexOf('ff') + 2)).toBe(4);
    expect(() => hexSelectionOffset(shown, shown.indexOf('ff') + 1)).toThrow();
  });

  it('rejects incomplete or malformed hex input', () => {
    expect(() => parseRawHex('0')).toThrow();
    expect(() => parseRawHex('0g')).toThrow();
    expect(() => parseRawHex('00 f')).toThrow();
  });
});
