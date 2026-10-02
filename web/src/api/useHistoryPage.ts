import { useEffect, useRef, useState } from 'react';
import { getHistoryPage, type HistoryPage } from './client';

type Location = { cursors: number[]; index: number; snapshot: number; newest: boolean };
const newestLocation = (): Location => ({ cursors: [0], index: 0, snapshot: 0, newest: true });
const emptyPage: HistoryPage = { items: [], snapshotId: 0, nextBeforeId: 0 };

export function useHistoryPage(search: string, scope: 'all' | 'in' | 'out', onSelection: (items: HistoryPage['items']) => void) {
  const [page, setPage] = useState<HistoryPage>(emptyPage);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [newTraffic, setNewTraffic] = useState(false);
  const [pageNumber, setPageNumber] = useState(1);
  const state = useRef({ location: newestLocation(), pending: null as Location | null,
    revision: 0, search, scope, pendingFilter: false });
  const selection = useRef(onSelection);
  selection.current = onSelection;
  const load = async (location = state.current.pending ?? state.current.location, fresh = false) => {
    const s = state.current;
    if (s.pendingFilter) return;
    s.pending = location;
    const revision = ++s.revision;
    setLoading(true);
    setError('');
    try {
      const next = await getHistoryPage({ beforeId: location.cursors[location.index], snapshotId: location.snapshot,
        search: s.search, inScope: s.scope === 'all' ? undefined : s.scope === 'in' });
      if (revision !== s.revision) return;
      // Commit navigation only after success so retries cannot skip a page.
      s.location = { ...location, snapshot: next.snapshotId };
      setPage(next);
      setPageNumber(location.index + 1);
      if (fresh) setNewTraffic(false);
      selection.current(next.items);
    } catch (cause) {
      if (revision === s.revision) setError(`History unavailable: ${cause instanceof Error ? cause.message : 'request failed'}`);
    } finally {
      if (revision === s.revision) { s.pending = null; setLoading(false); }
    }
  };
  useEffect(() => {
    const s = state.current;
    ++s.revision;
    s.pendingFilter = true;
    s.pending = null;
    s.location = newestLocation();
    setLoading(true);
    setPage(emptyPage);
    setPageNumber(1);
    setError('');
    setNewTraffic(false);
    const delay = search !== s.search ? 250 : 0;
    const timer = setTimeout(() => { s.search = search; s.scope = scope; s.pendingFilter = false; void load(newestLocation(), true); }, delay);
    return () => { clearTimeout(timer); ++s.revision; s.pendingFilter = true; };
  }, [search, scope]);
  return { ...page, loading, error, newTraffic, pageNumber,
    refresh: () => void load(newestLocation(), true),
    previous: () => {
      const s = state.current;
      if (loading || s.pending || s.location.index === 0) return;
      void load({ ...s.location, index: s.location.index - 1 });
    },
    next: () => {
      const s = state.current;
      if (loading || s.pending || !page.nextBeforeId) return;
      const location = s.location;
      void load({ ...location, newest: false, index: location.index + 1,
        cursors: [...location.cursors.slice(0, location.index + 1), page.nextBeforeId] });
    },
    event: (type: string) => {
      if (type === 'history.entry.created') {
        const location = state.current.pending ?? state.current.location;
        if (location.newest) void load(newestLocation(), true);
        else setNewTraffic(true);
      } else if (type === 'history.entry.updated' || type === 'scope.changed' || type === 'target.rebuild.completed') void load();
    },
  };
}
