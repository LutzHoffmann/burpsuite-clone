import { describe, expect, it } from 'vitest';
import { byteOffsetInRaw, canonicalEditedRequest, displayRawRequest } from './intruderBytes';

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
});
