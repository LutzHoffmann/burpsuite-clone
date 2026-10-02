import { useEffect, useRef, useState } from 'react';
import { getStorage, updateStorage, type StorageStatus } from './client';

export function useStorageStatus() {
  const [status, setStatus] = useState<StorageStatus | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const state = useRef({ active: false, revision: 0, saving: false });
  const refresh = async () => {
    const s = state.current;
    if (!s.active || s.saving) return;
    const revision = ++s.revision;
    try {
      const next = await getStorage();
      if (s.active && revision === s.revision) { setStatus(next); setError(''); }
    } catch (cause) {
      if (s.active && revision === s.revision) setError(`Storage status unavailable: ${cause instanceof Error ? cause.message : 'request failed'}`);
    }
  };
  useEffect(() => {
    state.current.active = true;
    void refresh();
    const timer = setInterval(() => void refresh(), 5000);
    return () => { state.current.active = false; ++state.current.revision; clearInterval(timer); };
  }, []);
  const save = async (limitBytes: number) => {
    const s = state.current;
    if (s.saving) return;
    s.saving = true;
    ++s.revision;
    setSaving(true);
    try {
      const next = await updateStorage(limitBytes);
      if (s.active) { setStatus(next); setError(''); }
    } finally {
      s.saving = false;
      if (s.active) setSaving(false);
    }
  };
  return { status, error, saving, refresh, save };
}
