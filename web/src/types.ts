export interface StatusDTO {
  apiAddr: string;
  proxyAddr: string;
  caFingerprint: string;
  caTrust: 'manual' | 'unavailable';
  httpsInterception: boolean;
  activeProject?: { id: number; name: string; createdAt: string } | null;
}

export interface HistoryItem {
  id: number;
  method: string;
  scheme: string;
  host: string;
  path: string;
  query: string;
  status: number;
  mimeType: string;
  requestSize: number;
  responseSize: number;
  durationMs: number;
  startedAt: string;
  intercepted: boolean;
  error: boolean;
}

export interface MessageDetail {
  headers: Record<string, string[]>;
  body: string;
  raw: string;
  textSafe: boolean;
  truncated: boolean;
}

export interface Exchange extends HistoryItem {
  errorMessage: string;
  requestTruncated: boolean;
  responseTruncated: boolean;
  request: MessageDetail;
  response: MessageDetail;
  tags: string[];
  note: string;
}

export interface SendRequest {
  method: string;
  url: string;
  headers: Record<string, string[]>;
  body: string;
}

export interface SendResult {
  status: number;
  headers: Record<string, string[]>;
  body: string;
  durationMs: number;
  size: number;
  truncated: boolean;
  contentType: string;
  textSafe?: boolean;
}

export interface InterceptItem {
  id: string;
  method: string;
  url: string;
  headers: Record<string, string[]>;
  body: string;
  bodyEditable: boolean;
  bodyTruncated: boolean;
}

export interface InterceptRule {
  enabled: boolean;
  method: string;
  hostContains: string;
  pathContains: string;
  mimeContains: string;
}

export interface InterceptConfig {
  enabled: boolean;
  rules: InterceptRule[];
}
