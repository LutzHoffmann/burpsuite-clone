export type Encoding = 'url' | 'base64' | 'hex' | 'html';
export type Direction = 'encode' | 'decode';
export type DiffLine = { kind: 'equal' | 'added' | 'removed'; text: string };

const maxInputBytes = 1_048_576;
const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });

function bounded(text: string, limit = maxInputBytes): void {
  if (encoder.encode(text).length > limit) throw new Error(`Input exceeds ${limit} byte limit`);
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  for (let start = 0; start < bytes.length; start += 8192) {
    binary += String.fromCharCode(...bytes.subarray(start, start + 8192));
  }
  return btoa(binary);
}

function base64ToBytes(text: string): Uint8Array {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(text)) {
    throw new Error('Invalid Base64 input');
  }
  return Uint8Array.from(atob(text), (char) => char.charCodeAt(0));
}

function hexToBytes(text: string): Uint8Array {
  const compact = text.replace(/\s+/g, '');
  if (compact.length % 2 || !/^[0-9a-f]*$/i.test(compact)) throw new Error('Invalid hex input');
  return Uint8Array.from({ length: compact.length / 2 }, (_, index) => Number.parseInt(compact.slice(index * 2, index * 2 + 2), 16));
}

export function transform(text: string, encoding: Encoding, direction: Direction): string {
  bounded(text);
  if (encoding === 'url') return direction === 'encode' ? encodeURIComponent(text) : decodeURIComponent(text);
  if (encoding === 'base64') return direction === 'encode' ? bytesToBase64(encoder.encode(text)) : decoder.decode(base64ToBytes(text));
  if (encoding === 'hex') return direction === 'encode' ? Array.from(encoder.encode(text), (byte) => byte.toString(16).padStart(2, '0')).join('') : decoder.decode(hexToBytes(text));
  if (encoding === 'html') {
    if (direction === 'encode') return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    const area = document.createElement('textarea');
    area.innerHTML = text;
    return area.value;
  }
  throw new Error('Unsupported encoding');
}

export function compareLines(left: string, right: string): DiffLine[] {
  bounded(left, 65_536);
  bounded(right, 65_536);
  const a = left.replace(/\r\n/g, '\n').split('\n');
  const b = right.replace(/\r\n/g, '\n').split('\n');
  if (a.length > 500 || b.length > 500) throw new Error('Comparer line limit is 500 per side');
  const table = Array.from({ length: a.length + 1 }, () => new Uint16Array(b.length + 1));
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      table[i][j] = a[i] === b[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1]);
    }
  }
  const result: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      result.push({ kind: 'equal', text: a[i] });
      i++;
      j++;
    } else if (i < a.length && (j === b.length || table[i + 1][j] >= table[i][j + 1])) {
      result.push({ kind: 'removed', text: a[i++] });
    } else {
      result.push({ kind: 'added', text: b[j++] });
    }
  }
  return result;
}

export function analyzeToken(text: string): { length: number; unique: number; entropy: number } {
  bounded(text);
  const chars = Array.from(text);
  const counts = new Map<string, number>();
  for (const char of chars) counts.set(char, (counts.get(char) ?? 0) + 1);
  let entropy = 0;
  for (const count of counts.values()) {
    const probability = count / chars.length;
    entropy -= probability * Math.log2(probability);
  }
  return { length: chars.length, unique: counts.size, entropy };
}
