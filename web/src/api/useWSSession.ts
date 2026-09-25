import { useEffect, useRef, useState } from 'react';
import type { WSRepeatRequest, WSSessionMessage, WSSessionSnapshot } from '../types';
import { closeWSSession, connectWSSession, disposeWSSession, pollWSSession, sendWSSession, WSSessionError } from './wsSessions';

const dispose = (id: string) => { void disposeWSSession(id).catch(() => { /* The server lease bounds orphan lifetime. */ }); };
const retainedBytes = (message: WSSessionMessage) => message.payloadFormat === 'hex' ? message.payload.length / 2 : new TextEncoder().encode(message.payload).length;

export function useWSSession() {
  const [snapshot, setSnapshot] = useState<WSSessionSnapshot | null>(null);
  const [operation, setOperation] = useState<'' | 'connecting' | 'sending' | 'closing'>('');
  const [error, setError] = useState('');
  const [log, setLog] = useState<{ messages: WSSessionMessage[]; evicted: number; gaps: number; lossUnknown: boolean }>({ messages: [], evicted: 0, gaps: 0, lossUnknown: false });
  const current = useRef<WSSessionSnapshot | null>(null);
  const revision = useRef(0);
  const cursor = useRef(0);
  const requests = useRef(new Set<AbortController>());
  const pending = useRef('');

  function invalidate() {
    ++revision.current;
    for (const request of requests.current) request.abort();
    requests.current.clear();
    pending.current = '';
  }
  function publish(next: WSSessionSnapshot, records = false) {
    // Mutating responses have no cursor parameter: only polls advance the log.
    const previous = current.current;
    if (previous?.state === 'closed') next = { ...next, state: 'closed', reason: next.reason === 'send_failed' ? next.reason : previous.reason };
    if (previous) next = { ...next, latestSequence: Math.max(previous.latestSequence, next.latestSequence), droppedMessages: Math.max(previous.droppedMessages, next.droppedMessages) };
    const metadata = { ...next, messages: [] };
    current.current = metadata; setSnapshot(metadata);
    if (!records) return;
    const after = cursor.current;
    const incoming = (next.messages ?? []).filter(message => message.sequence > after).sort((a, b) => a.sequence - b.sequence);
    cursor.current = Math.max(after, next.nextSequence);
    setLog(previousLog => {
      const messages = [...previousLog.messages, ...incoming];
      let bytes = messages.reduce((sum, message) => sum + retainedBytes(message), 0);
      let evicted = previousLog.evicted;
      while (messages.length > 256 || bytes > 1 << 20) { bytes -= retainedBytes(messages.shift()!); evicted++; }
      return { messages, evicted, gaps: previousLog.gaps + Math.max(0, next.oldestSequence - after - 1), lossUnknown: previousLog.lossUnknown };
    });
  }
  function reset() {
    invalidate();
    if (current.current) dispose(current.current.id);
    current.current = null; cursor.current = 0;
    setSnapshot(null); setOperation(''); setError(''); setLog({ messages: [], evicted: 0, gaps: 0, lossUnknown: false });
  }

  function recordCollectionLoss() {
    const latest = current.current?.latestSequence ?? cursor.current;
    const missing = Math.max(0, latest - cursor.current);
    setLog(previous => ({ ...previous, gaps: previous.gaps + missing, lossUnknown: true }));
    cursor.current = latest;
  }

  useEffect(() => {
    let polling: AbortController | null = null;
    const timer = setInterval(() => {
      const session = current.current;
      if (!session || (polling && !polling.signal.aborted) || pending.current === 'closing' || (session.state === 'closed' && cursor.current >= session.latestSequence)) return;
      const version = revision.current;
      const controller = new AbortController(); requests.current.add(controller); polling = controller;
      void pollWSSession(session.id, cursor.current, controller.signal).then(next => {
        if (version !== revision.current || controller.signal.aborted) return;
        publish(next, true);
      }).catch(cause => {
        if (version !== revision.current || controller.signal.aborted) return;
        if (cause instanceof WSSessionError && cause.status === 404) {
          const latest = current.current ?? session;
          recordCollectionLoss();
          publish({ ...latest, state: 'closed', reason: 'expired_or_disposed' });
        } else setError('Polling unavailable. The session may close when its 30-second lease expires; no messages are retried.');
      }).finally(() => { if (polling === controller) polling = null; requests.current.delete(controller); });
    }, 1000);
    const pagehide = () => {
      invalidate();
      if (current.current) dispose(current.current.id);
    };
    window.addEventListener('pagehide', pagehide);
    return () => { clearInterval(timer); window.removeEventListener('pagehide', pagehide); pagehide(); };
  }, []);

  async function connect(request: WSRepeatRequest) {
    if (pending.current || current.current?.state === 'connected' || current.current?.state === 'closing') return;
    reset();
    const version = revision.current;
    const controller = new AbortController(); requests.current.add(controller);
    pending.current = 'connecting'; setOperation('connecting');
    try {
      const next = await connectWSSession({ url: request.url, headers: request.headers, subprotocols: request.subprotocols }, controller.signal);
      if (version !== revision.current || controller.signal.aborted) { dispose(next.id); return; }
      publish(next, true);
    } catch {
      if (version === revision.current) setError('Connection failed. No application message was sent. A lost response is covered by the server lease.');
    } finally {
      requests.current.delete(controller);
      if (version === revision.current) { pending.current = ''; setOperation(''); }
    }
  }
  async function send(request: WSRepeatRequest) {
    const session = current.current;
    if (!session || session.state !== 'connected' || pending.current) return;
    const version = revision.current;
    const controller = new AbortController(); requests.current.add(controller);
    pending.current = 'sending'; setOperation('sending'); setError('');
    try {
      const next = await sendWSSession(session.id, { type: request.type, payload: request.payload, payloadFormat: request.payloadFormat }, controller.signal);
      if (version === revision.current && !controller.signal.aborted) publish(next);
    } catch (cause) {
      if (version !== revision.current) return;
      if (cause instanceof WSSessionError && [400, 403, 409, 413, 422, 429].includes(cause.status)) setError(`Message not sent: request rejected (${cause.status}). No automatic retry.`);
      else {
        recordCollectionLoss();
        publish({ ...session, state: 'closed', reason: 'send_failed' });
        dispose(session.id);
      }
    } finally {
      requests.current.delete(controller);
      if (version === revision.current) { pending.current = ''; setOperation(''); }
    }
  }
  async function close() {
    const session = current.current;
    if (!session || pending.current === 'closing') return;
    const wasSending = pending.current === 'sending';
    invalidate();
    const version = revision.current;
    const controller = new AbortController(); requests.current.add(controller);
    pending.current = 'closing'; setOperation('closing');
    if (wasSending) setError('Canceled send: delivery unknown. Closing cannot undo previous sends. No automatic retry.');
    try {
      const next = await closeWSSession(session.id, controller.signal);
      if (version === revision.current) publish(next);
    } catch {
      if (version === revision.current) {
        recordCollectionLoss();
        publish({ ...session, state: 'closed', reason: wasSending ? 'send_failed' : 'close_unconfirmed' });
        dispose(session.id);
        setError('Close could not be confirmed. Disposal was attempted; the server lease is the fallback.');
      }
    } finally {
      requests.current.delete(controller);
      if (version === revision.current) { pending.current = ''; setOperation(''); }
    }
  }
  function cancelConnect() { reset(); setError('Connection canceled. Any lost connection response is covered by the server lease.'); }
  return { snapshot, operation, error, log, connect, send, close, reset, cancelConnect, hasWork: !!snapshot || !!operation };
}
