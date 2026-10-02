export type JwtInspection = {
  header: Record<string, unknown>;
  payload: Record<string, unknown>;
  signaturePresent: boolean;
};

function decodeSegment(segment: string): Record<string, unknown> {
  if (!/^[A-Za-z0-9_-]+$/.test(segment) || segment.length % 4 === 1) throw new Error('Invalid JWT encoding');
  const base64 = segment.replace(/-/g, '+').replace(/_/g, '/');
  let value: unknown;
  try {
    const bytes = Uint8Array.from(atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, '=')), (char) => char.charCodeAt(0));
    value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
  } catch { throw new Error('Invalid JWT encoding or JSON'); }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('JWT section must be a JSON object');
  return value as Record<string, unknown>;
}

export function inspectJwt(token: string): JwtInspection {
  if (new TextEncoder().encode(token).length > 65_536) throw new Error('JWT input exceeds 64 KiB limit');
  const sections = token.trim().split('.');
  if (sections.length !== 3) throw new Error('JWT must have three sections');
  if (!/^[A-Za-z0-9_-]*$/.test(sections[2])) throw new Error('Invalid JWT signature encoding');
  return { header: decodeSegment(sections[0]), payload: decodeSegment(sections[1]), signaturePresent: sections[2].length > 0 };
}
