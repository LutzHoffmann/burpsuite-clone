import type { Exchange, HistoryItem, InterceptItem, SendRequest, SendResult, StatusDTO } from '../types';

const api = async <T>(path: string, init?: RequestInit): Promise<T> => {
  const response = await fetch(path, init);
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
  }
  return response.json() as Promise<T>;
};

export const getStatus = () => api<StatusDTO>('/api/status');

export const getHistory = () => api<HistoryItem[]>('/api/history');

export const caDownloadURL = '/api/ca.pem';

export const getExchange = (id: number) => api<Exchange>(`/api/history/${id}`);

export const sendRepeater = (id: string, request: SendRequest) =>
  api<SendResult>(`/api/repeater/sessions/${id}/send`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  });

export const forwardIntercept = (id: string, item: InterceptItem) =>
  api<void>(`/api/intercept/${id}/forward`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(item),
  });

export const dropIntercept = (id: string) =>
  api<void>(`/api/intercept/${id}/drop`, { method: 'POST' });
