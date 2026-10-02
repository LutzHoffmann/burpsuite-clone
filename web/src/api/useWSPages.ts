import { useEffect, useRef, useState } from 'react';
import type { WSCursor, WSPage } from '../types';

type Location = { cursors: number[]; index: number; snapshot: number; live: boolean };
const first = (): Location => ({ cursors: [0], index: 0, snapshot: 0, live: true });

// Each mounted list owns its cursor history. Polls may update live pages, never pinned pages.
export function useWSPages<T>(fetchPage: (cursor: WSCursor) => Promise<WSPage<T>>) {
  const [page, setPage] = useState<WSPage<T>>({ items: [], nextBeforeId: 0, snapshotId: 0 });
  const [pageNumber, setPageNumber] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [newTraffic, setNewTraffic] = useState(false);
  const [refreshed, setRefreshed] = useState(false);
  const fetcher = useRef(fetchPage);
  fetcher.current = fetchPage;
  const state = useRef({ location: first(), revision: 0, busy: false, polling: false, active: false });

  const load = async (location: Location, refresh = false) => {
    const s = state.current;
    const revision = ++s.revision;
    s.busy = true;
    setLoading(true);
    setError('');
    try {
      const next = await fetcher.current({ beforeId: location.cursors[location.index], snapshotId: location.snapshot });
      if (!s.active || revision !== s.revision) return;
      s.location = { ...location, snapshot: next.snapshotId };
      setPage(next);
      setPageNumber(location.index + 1);
      if (refresh) { setNewTraffic(false); setRefreshed(true); }
    } catch (cause) {
      if (s.active && revision === s.revision) setError(`WebSocket history unavailable: ${cause instanceof Error ? cause.message : 'request failed'}`);
    } finally {
      if (s.active && revision === s.revision) { s.busy = false; setLoading(false); }
    }
  };

  useEffect(() => {
    const s = state.current;
    s.active = true;
    void load(first());
    const timer = setInterval(async () => {
      if (s.busy || s.polling) return;
      s.polling = true;
      const revision = s.revision;
      try {
        const next = await fetcher.current({});
        if (!s.active || revision !== s.revision) return;
        if (s.location.live) {
          s.location.snapshot = next.snapshotId;
          setPage(next);
          setRefreshed(true);
        } else if (next.snapshotId > s.location.snapshot) setNewTraffic(true);
        setError('');
      } catch (cause) {
        if (s.active && revision === s.revision) setError(`WebSocket refresh failed: ${cause instanceof Error ? cause.message : 'request failed'}`);
      } finally { s.polling = false; }
    }, 5000);
    return () => { s.active = false; ++s.revision; clearInterval(timer); };
  }, []);

  return { ...page, pageNumber, loading, error, newTraffic, refreshed,
    refresh: () => { if (!state.current.busy) void load(first(), true); },
    previous: () => {
      const s = state.current;
      if (!s.busy && s.location.index > 0) void load({ ...s.location, index: s.location.index - 1 });
    },
    next: () => {
      const s = state.current;
      if (!s.busy && page.nextBeforeId) void load({ ...s.location, live: false, index: s.location.index + 1,
        cursors: [...s.location.cursors.slice(0, s.location.index + 1), page.nextBeforeId] });
    },
  };
}
