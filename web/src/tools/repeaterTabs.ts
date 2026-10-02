import type { SendRequest, SendResult } from '../types';

export type RepeaterDraft = { method: string; url: string; headersText: string; body: string };
export type RepeaterTab = { id: number; draft: RepeaterDraft; result: SendResult | null; pending: boolean; error: string; sendId: number };

export const formatRepeaterHeaders = (headers: Record<string, string[]>) =>
  Object.entries(headers).flatMap(([name, values]) => values.map((value) => `${name}: ${value}`)).join('\n');

export const parseRepeaterHeaders = (text: string) => text.split('\n').reduce<Record<string, string[]>>((headers, line) => {
  const separator = line.indexOf(':');
  if (separator < 1) return headers;
  const name = line.slice(0, separator).trim();
  const value = line.slice(separator + 1).trim();
  if (name && value) headers[name] = [...(headers[name] ?? []), value];
  return headers;
}, {});

export function draftFromRequest(request: SendRequest): RepeaterDraft {
  return { method: request.method, url: request.url, headersText: formatRepeaterHeaders(request.headers), body: request.body };
}

export function requestFromDraft(draft: RepeaterDraft): SendRequest {
  return { method: draft.method, url: draft.url, headers: parseRepeaterHeaders(draft.headersText), body: draft.body };
}

export function newRepeaterTab(id: number, request: SendRequest): RepeaterTab {
  return { id, draft: draftFromRequest(request), result: null, pending: false, error: '', sendId: 0 };
}

export function duplicateRepeaterTab(id: number, source: RepeaterTab): RepeaterTab {
  return { id, draft: { ...source.draft }, result: null, pending: false, error: '', sendId: 0 };
}

export function findResponseMatches(body: string, query: string): number[] {
  if (!query) return [];
  const matches: number[] = [];
  const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const pattern = new RegExp(escaped, 'gi');
  for (const match of body.matchAll(pattern)) {
    matches.push(match.index);
    if (matches.length >= 1000) break;
  }
  return matches;
}

export function tabLabel(tab: RepeaterTab): string {
  let host = 'New request';
  try { host = new URL(tab.draft.url).host || host; } catch { /* Incomplete URLs remain editable. */ }
  return `${tab.id}: ${tab.draft.method} ${host}`;
}
