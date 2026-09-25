export const displayRawRequest = (value: string) => value.replace(/\r\n/g, '\n');

export function parseRawHex(value: string): Uint8Array {
  if (!/^(?:\s*[0-9a-fA-F]{2})*\s*$/.test(value)) throw new Error('Hex request requires complete byte pairs (00-FF).');
  const compact = value.replace(/\s/g, '');
  return Uint8Array.from({ length: compact.length / 2 }, (_, index) => Number.parseInt(compact.slice(index * 2, index * 2 + 2), 16));
}

export const displayRawHex = (bytes: Uint8Array) => Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join(' ');

export function hexSelectionOffset(value: string, offset: number): number {
  const prefix = value.slice(0, offset);
  const digits = prefix.replace(/\s/g, '');
  if (digits.length % 2 !== 0 || /[^0-9a-fA-F]/.test(digits)) throw new Error('Select complete hex bytes.');
  return digits.length / 2;
}

export function canonicalEditedRequest(value: string): string {
  const boundary = value.indexOf('\n\n');
  if (boundary < 0) return value.replace(/\n/g, '\r\n');
  return value.slice(0, boundary + 2).replace(/\n/g, '\r\n') + value.slice(boundary + 2);
}

export function byteOffsetInRaw(original: string, displayOffset: number): number {
  let sourceOffset = 0;
  let shownOffset = 0;
  while (shownOffset < displayOffset && sourceOffset < original.length) {
    if (original.slice(sourceOffset, sourceOffset + 2) === '\r\n') sourceOffset += 2;
    else sourceOffset += 1;
    shownOffset += 1;
  }
  if (shownOffset !== displayOffset || sourceOffset > original.length) throw new Error('Invalid request selection.');
  if (sourceOffset > 0 && sourceOffset < original.length &&
      /[\uD800-\uDBFF]/.test(original[sourceOffset - 1]) && /[\uDC00-\uDFFF]/.test(original[sourceOffset])) {
    throw new Error('Select complete UTF-8 characters.');
  }
  return new TextEncoder().encode(original.slice(0, sourceOffset)).length;
}
