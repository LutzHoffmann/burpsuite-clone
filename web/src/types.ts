export interface StatusDTO {
  apiAddr: string;
  proxyAddr: string;
  caFingerprint: string;
  caTrust: 'manual' | 'unavailable';
  httpsInterception: boolean;
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

export interface Exchange {
  ID: number;
  Method: string;
  Scheme: string;
  Host: string;
  Path: string;
  Query: string;
  Status: number;
  MIMEType: string;
  RequestSize: number;
  ResponseSize: number;
  Duration: number;
  StartedAt: string;
  Intercepted: boolean;
  Error: boolean;
  ErrorMessage: string;
  RequestTruncated: boolean;
  ResponseTruncated: boolean;
  Request: { Headers: Record<string, string[]>; Body: string; Raw: string };
  Response: { Headers: Record<string, string[]>; Body: string; Raw: string };
  Tags: string[];
  Note: string;
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
}

export interface InterceptItem {
  id: string;
  method: string;
  url: string;
  headers: Record<string, string[]>;
  body: string;
}
