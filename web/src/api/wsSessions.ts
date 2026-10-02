import type { WSRepeatRequest, WSSessionSnapshot } from '../types';

const root = '/api/websocket-repeater/sessions';
export class WSSessionError extends Error {
  constructor(public status: number) { super(`Session request rejected (${status}).`); }
}
async function request<T>(path: string, init: RequestInit): Promise<T> {
  const response = await fetch(path, { ...init, cache: 'no-store' });
  if (!response.ok) throw new WSSessionError(response.status);
  return response.status === 204 ? undefined as T : response.json();
}
const json = (body: unknown, signal: AbortSignal): RequestInit => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal });
export const connectWSSession = (body: Pick<WSRepeatRequest, 'url' | 'headers' | 'subprotocols'>, signal: AbortSignal) => request<WSSessionSnapshot>(root, json(body, signal));
export const sendWSSession = (id: string, body: Pick<WSRepeatRequest, 'type' | 'payload' | 'payloadFormat'>, signal: AbortSignal) => request<WSSessionSnapshot>(`${root}/${id}/send`, json(body, signal));
export const pollWSSession = (id: string, afterSequence = 0, signal?: AbortSignal) => request<WSSessionSnapshot>(`${root}/${id}?afterSequence=${afterSequence}`, { signal });
export const closeWSSession = (id: string, signal: AbortSignal) => request<WSSessionSnapshot>(`${root}/${id}/close`, { method: 'POST', signal });
export const disposeWSSession = (id: string) => request<void>(`${root}/${id}`, { method: 'DELETE', keepalive: true });
