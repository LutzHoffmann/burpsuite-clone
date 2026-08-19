import type { Exchange, HistoryItem, InterceptConfig, InterceptItem, SendRequest, SendResult, StatusDTO } from '../types';

const api = async <T>(path: string, init?: RequestInit): Promise<T> => {
  const response = await fetch(path, init);
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
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

export const getHistory = () => api<HistoryItem[]>('/api/history');

export const caDownloadURL = '/api/ca.pem';

export const getExchange = (id: number) => api<Exchange>(`/api/history/${id}`);

export const sendRepeater = (id: string, request: SendRequest) =>
  api<SendResult>(`/api/repeater/sessions/${id}/send`, jsonRequest('POST', request));

export const getInterceptQueue = () => api<InterceptItem[]>('/api/intercept/queue');

export const getInterceptConfig = () => api<InterceptConfig>('/api/intercept/config');

export const updateInterceptConfig = (config: InterceptConfig) =>
  api<InterceptConfig>('/api/intercept/config', jsonRequest('PUT', config));

export const forwardIntercept = (id: string, item: InterceptItem) =>
  api<void>(`/api/intercept/${id}/forward`, jsonRequest('POST', item));

export const dropIntercept = (id: string) =>
  api<void>(`/api/intercept/${id}/drop`, jsonRequest('POST', {}));
