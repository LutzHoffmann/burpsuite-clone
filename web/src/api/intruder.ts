export type AttackType = 'sniper' | 'battering_ram' | 'pitchfork' | 'cluster_bomb';
export type JobState = 'draft' | 'running' | 'pausing' | 'paused' | 'aborting' | 'aborted' | 'completed' | 'failed';

export interface IntruderPosition { ID: string; Start: number; End: number; PayloadSetID: string }
export interface IntruderPayloadSet { ID: string; Payloads: string[] }
export interface IntruderConfig {
  attack: AttackType;
  template: { method: string; url: string; raw: string };
  positions: IntruderPosition[];
  payloadSets: IntruderPayloadSet[];
  requestLimit: number;
  concurrency: number;
  ratePerSecond: number;
  timeoutMs: number;
}
export interface IntruderJob {
  id: string; config: IntruderConfig; state: JobState; stateReason: string; revision: number;
  totalRequests: number; nextSequence: number; completedCount: number; errorCount: number;
}
export interface IntruderJobSummary {
  ID: string; Attack: AttackType; State: JobState; StateReason: string; Revision: number;
  TotalRequests: number; CompletedCount: number; ErrorCount: number; UpdatedAt: string;
}
export interface IntruderResult {
  Sequence: number; URL: string; Status: number; ErrorCategory: string; Duration: number;
  ResponseSize: number; MIMEType: string; ResponseCapture: string; StorageStatus: string;
}
export interface IntruderResultPage { Results: IntruderResult[]; NextBeforeSequence: number | null }

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { cache: 'no-store', ...init });
  if (!response.ok) {
    const detail = await response.text().catch(() => '');
    throw new Error(`${response.status}: ${detail.trim() || response.statusText}`);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
const json = (method: string, body: unknown): RequestInit => ({ method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

export const listIntruderJobs = (signal?: AbortSignal) => request<IntruderJobSummary[]>('/api/intruder/jobs', { signal });
export const getIntruderJob = (id: string, signal?: AbortSignal) => request<IntruderJob>(`/api/intruder/jobs/${id}`, { signal });
export const createIntruderJob = (config: IntruderConfig) => request<IntruderJob>('/api/intruder/jobs', json('POST', { config }));
export const updateIntruderJob = (job: IntruderJob, config: IntruderConfig) => request<IntruderJob>(`/api/intruder/jobs/${job.id}`, json('PUT', { revision: job.revision, config }));
export const controlIntruderJob = (job: IntruderJob, action: 'start' | 'pause' | 'resume' | 'abort') => request<IntruderJob>(`/api/intruder/jobs/${job.id}/${action}`, json('POST', { revision: job.revision }));
export const deleteIntruderJob = (id: string) => request<void>(`/api/intruder/jobs/${id}`, { method: 'DELETE' });
export const listIntruderResults = (id: string, beforeSequence?: number, signal?: AbortSignal) => {
  const query = new URLSearchParams({ limit: '100' });
  if (beforeSequence !== undefined) query.set('beforeSequence', String(beforeSequence));
  return request<IntruderResultPage>(`/api/intruder/jobs/${id}/results?${query}`, { signal });
};
