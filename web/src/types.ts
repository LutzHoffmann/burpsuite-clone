export interface WSConnection {
  id: number; url: string; inScope: boolean; openedAt: string; closedAt: string | null;
  state: string; gaps: number; captureIncomplete: boolean;
}

export interface WSMessage {
  id: number; connectionId: number; sequence: number; direction: string; observedAt: string;
  type: string; size: number; truncated: boolean; complete: boolean; encoding: string;
}

export interface WSMessageDetail extends WSMessage { payload: string; payloadFormat: 'text' | 'hex' }
export interface WSPage<T> { items: T[]; nextBeforeId: number; snapshotId: number }
export interface WSCursor { beforeId?: number; snapshotId?: number }

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
  inScope: boolean;
  scopeVersion: number;
  scopeRuleId: number | null;
}

export interface MessageDetail {
  headers: Record<string, string[]>;
  body: string;
  raw: string;
  textSafe: boolean;
  truncated: boolean;
}

export interface Exchange extends HistoryItem {
  appliedRuleIds?: string[];
  responseIntercepted?: boolean;
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
  saved: boolean;
  storageWarning?: string;
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
  phase?: 'request' | 'response';
  statusCode?: number;
  id: string;
  method: string;
  url: string;
  headers: Record<string, string[]>;
  body: string;
  bodyEditable: boolean;
  bodyTruncated: boolean;
}

export interface InterceptRule {
  statusCode?: number;
  enabled: boolean;
  method: string;
  hostContains: string;
  pathContains: string;
  mimeContains: string;
}

export interface InterceptConfig {
  enabled: boolean;
  rules: InterceptRule[];
  responseEnabled?: boolean;
  responseRules?: InterceptRule[];
  replacementRules?: ReplacementRule[];
}

export interface ReplacementRule {
  id: string;
  enabled: boolean;
  direction: 'request' | 'response';
  target: 'url' | 'header' | 'body';
  header: string;
  pattern: string;
  replacement: string;
  regex: boolean;
  hostContains: string;
  pathContains: string;
  mimeContains: string;
}

export type ScopeAction = 'include' | 'exclude';

export interface ScopeRule {
  id: number;
  enabled: boolean;
  action: ScopeAction;
  scheme: '' | 'http' | 'https';
  hostPattern: string;
  port: number;
  pathPrefix: string;
}

export interface ScopeState {
  version: number;
  rules: ScopeRule[];
}

export interface TargetTreeNode {
  id: number;
  scheme: string;
  host: string;
  port: number;
  path: string;
  method: string;
  inScope: boolean;
  statuses: number[];
  requestMimes: string[];
  responseMimes: string[];
  count: number;
  lastSeen: string;
  children: TargetTreeNode[];
}

export interface TargetEndpoint {
  id: number;
  scheme: string;
  host: string;
  port: number;
  path: string;
  method: string;
  inScope: boolean;
  firstSeen: string;
  lastSeen: string;
  count: number;
  statuses: number[];
  requestMimes: string[];
  responseMimes: string[];
  parseDiagnostics: string[];
  errorSeen: boolean;
  latestExchangeId: number;
}

export interface TargetParameter {
  location: string;
  name: string;
  valueType: string;
  firstSeen: string;
  lastSeen: string;
  count: number;
}

export interface TargetRequestRef {
  exchangeId: number;
  startedAt: string;
  status: number;
  error: boolean;
}

export interface RebuildStatus {
  id: number;
  scopeVersion: number;
  activeScopeVersion: number;
  status: 'idle' | 'building' | 'active' | 'failed' | 'cancelled';
  processed: number;
  total: number;
  error: string;
}

export interface TargetRefresh {
  sequence: number;
  type: 'initial' | 'scope.changed' | 'target.endpoint.updated' | 'target.rebuild.started' | 'target.rebuild.progress' | 'target.rebuild.completed' | 'target.rebuild.failed';
}

export interface WSRepeatRequest {
  url: string;
  type: 'text' | 'binary';
  payload: string;
  payloadFormat: 'text' | 'hex';
  headers: Record<string, string[]>;
  subprotocols: string[];
}

export interface WSRepeatResult {
  sent: boolean;
  outcome: string;
  messages: { type: string; payload: string; payloadFormat: string; size: number; truncated: boolean }[];
  durationMs: number;
  subprotocol: string;
}

export interface WSSessionMessage {
  sequence: number;
  direction: 'client-to-server' | 'server-to-client';
  timestamp: string;
  type: string;
  payload: string;
  payloadFormat: 'text' | 'hex';
  size: number;
  truncated: boolean;
  complete: boolean;
}

export interface WSSessionSnapshot {
  id: string;
  url: string;
  state: 'connected' | 'closing' | 'closed';
  reason: string;
  subprotocol: string;
  messages: WSSessionMessage[];
  oldestSequence: number;
  latestSequence: number;
  nextSequence: number;
  droppedMessages: number;
}
