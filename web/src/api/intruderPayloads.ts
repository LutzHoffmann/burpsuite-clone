export interface PayloadEditor { mode: 'text' | 'hex'; value: string }

function encode(bytes: Uint8Array): string {
  let binary = '';
  for (let index = 0; index < bytes.length; index += 8192) binary += String.fromCharCode(...bytes.subarray(index, index + 8192));
  return btoa(binary);
}

function decode(value: string): Uint8Array {
  return Uint8Array.from(atob(value), (character) => character.charCodeAt(0));
}

function hex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join(' ');
}

function parseHex(line: string): Uint8Array {
  if (!/^(?:[ \t]*[0-9a-fA-F]{2})*[ \t]*$/.test(line)) throw new Error('Hex payloads require complete byte pairs (00-FF).');
  const compact = line.replace(/[ \t]/g, '');
  const bytes = new Uint8Array(compact.length / 2);
  for (let index = 0; index < bytes.length; index += 1) bytes[index] = Number.parseInt(compact.slice(index * 2, index * 2 + 2), 16);
  return bytes;
}

export function encodePayloadEditor(editor: PayloadEditor): string[] {
  return editor.value.split('\n').map((line) => encode(editor.mode === 'hex' ? parseHex(line) : new TextEncoder().encode(line)));
}

export function displayPayloads(values: string[]): PayloadEditor {
  const bytes = values.map(decode);
  const decoder = new TextDecoder('utf-8', { fatal: true });
  try {
    const text = bytes.map((item) => decoder.decode(item));
    if (text.some((item) => /[\r\n]/.test(item))) throw new Error('payload contains line breaks');
    return { mode: 'text', value: text.join('\n') };
  } catch {
    return { mode: 'hex', value: bytes.map(hex).join('\n') };
  }
}

export function convertPayloadEditor(editor: PayloadEditor, mode: PayloadEditor['mode']): PayloadEditor {
  if (editor.mode === mode) return editor;
  const encoded = encodePayloadEditor(editor);
  if (mode === 'hex') return { mode, value: encoded.map((item) => hex(decode(item))).join('\n') };
  const decoded = displayPayloads(encoded);
  if (decoded.mode === 'hex') throw new Error('Binary or multiline payloads cannot be shown as text.');
  return decoded;
}
