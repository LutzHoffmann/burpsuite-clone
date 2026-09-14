import type { Exchange, HistoryItem, InterceptConfig, InterceptItem, RebuildStatus, ScopeRule, ScopeState, SendRequest, SendResult, StatusDTO, TargetEndpoint, TargetParameter, TargetRequestRef, TargetTreeNode } from '../types';
import type { WSConnection, WSCursor, WSMessage, WSMessageDetail, WSPage } from '../types';

export class ApiError extends Error {
  constructor(public readonly status: number, statusText: string) {
    super(`${status} ${statusText}`.trim());
    this.name = 'ApiError';
  }
}

const api = async <T>(path: string, init?: RequestInit): Promise<T> => {
  const response = await fetch(path, init);
  if (!response.ok) {
    const detail = await response.text().catch(() => '');
    throw new ApiError(response.status, detail.trim() || response.statusText);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
};

const jsonRequest = (method: string, value: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(value),
});

export const getStatus = () => api<StatusDTO>('/api/status');

function wsQuery(cursor: WSCursor) {
  const query = new URLSearchParams();
  if (cursor.beforeId) query.set('beforeId', String(cursor.beforeId));
  if (cursor.snapshotId) query.set('snapshotId', String(cursor.snapshotId));
  return query.size ? `?${query}` : '';
}
export const getWSConnections = (cursor: WSCursor = {}) => api<WSPage<WSConnection>>(`/api/websockets${wsQuery(cursor)}`);
export const getWSConnection = (id: number) => api<WSConnection>(`/api/websockets/${id}`);
export const getWSMessages = (id: number, cursor: WSCursor = {}) => api<WSPage<WSMessage>>(`/api/websockets/${id}/messages${wsQuery(cursor)}`);
export const getWSMessage = (id: number, messageId: number) => api<WSMessageDetail>(`/api/websockets/${id}/messages/${messageId}`);

export interface HistoryPage { items: HistoryItem[]; nextBeforeId: number; snapshotId: number }
export interface StorageStatus { limitBytes: number; usedBytes: number; paused: boolean; skippedRecords: number }
export const getHistoryPage = (filters: { beforeId?: number; snapshotId?: number; search?: string; method?: string; host?: string; inScope?: boolean } = {}) => {
  const query = new URLSearchParams();
  Object.entries(filters).forEach(([key, value]) => { if (value !== undefined && value !== '' && value !== 0) query.set(key, String(value)); });
  return api<HistoryPage>(`/api/history/page${query.size ? `?${query}` : ''}`);
};
export const getStorage = () => api<StorageStatus>('/api/storage');
export const updateStorage = (limitBytes: number) => api<StorageStatus>('/api/storage', jsonRequest('PUT', { limitBytes }));

export const caDownloadURL = '/api/ca.pem';

export const getExchange = (id: number) => api<Exchange>(`/api/history/${id}`);

export const sendRepeater = (id: string, request: SendRequest) =>
  api<SendResult>(`/api/repeater/sessions/${id}/send`, jsonRequest('POST', request));

export const getInterceptQueue = () => api<InterceptItem[]>('/api/intercept/queue');

export const getResponseQueue = () => api<InterceptItem[]>('/api/intercept/response-queue');

export const forwardResponse = (item: InterceptItem) =>
  api<void>(`/api/intercept/response/${encodeURIComponent(item.id)}/forward`, jsonRequest('POST', {
    statusCode: item.statusCode, headers: item.headers, body: item.body,
  }));

export const dropResponse = (id: string) =>
  api<void>(`/api/intercept/response/${encodeURIComponent(id)}/drop`, jsonRequest('POST', {}));

export const getInterceptConfig = () => api<InterceptConfig>('/api/intercept/config');

export const updateInterceptConfig = (config: InterceptConfig) =>
  api<InterceptConfig>('/api/intercept/config', jsonRequest('PUT', config));

export const forwardIntercept = (id: string, item: InterceptItem) =>
  api<void>(`/api/intercept/${id}/forward`, jsonRequest('POST', item));

export const dropIntercept = (id: string) =>
  api<void>(`/api/intercept/${id}/drop`, jsonRequest('POST', {}));

export const getScopeState = () => api<ScopeState>('/api/scope/rules');

export const replaceScopeRules = (version: number, rules: ScopeRule[]) =>
  api<ScopeState>('/api/scope/rules', jsonRequest('PUT', { version, rules }));

export const getTargetTree = () => api<TargetTreeNode[]>('/api/target/tree');

export const getTargetEndpoint = (id: number) => api<TargetEndpoint>(`/api/target/endpoints/${id}`);

export const getTargetRequests = (id: number) => api<TargetRequestRef[]>(`/api/target/endpoints/${id}/requests`);

export const getTargetParameters = (id: number) => api<TargetParameter[]>(`/api/target/endpoints/${id}/parameters`);

export const getRebuildStatus = () => api<RebuildStatus>('/api/target/rebuild');

export const retryTargetRebuild = () => api<RebuildStatus>('/api/target/rebuild', jsonRequest('POST', {}));
