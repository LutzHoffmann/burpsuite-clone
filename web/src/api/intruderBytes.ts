export const displayRawRequest = (value: string) => value.replace(/\r\n/g, '\n');

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
