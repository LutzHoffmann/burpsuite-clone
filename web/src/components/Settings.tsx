import { Download, ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import type { StorageStatus } from '../api/client';
import { caDownloadURL } from '../api/client';
import type { StatusDTO } from '../types';

type SettingsProps = {
  status: StatusDTO;
  storage: StorageStatus | null;
  saving: boolean;
  onSaveStorage: (limitBytes: number) => Promise<void>;
};

export function Settings({ status, storage, saving, onSaveStorage }: SettingsProps) {
  const [draft, setDraft] = useState<string | null>(null);
  const [saveError, setSaveError] = useState('');
  const [saved, setSaved] = useState(false);
  const limit = draft ?? String((storage?.limitBytes ?? 1073741824) / 1048576);
  const valid = /^\d+$/.test(limit) && Number(limit) >= 1 && Number(limit) <= 1048576;
  const save = async () => {
    if (!valid || saving || !storage) return;
    setSaveError('');
    setSaved(false);
    try {
      await onSaveStorage(Number(limit) * 1048576);
      setDraft(null);
      setSaved(true);
    } catch (cause) {
      setSaveError(`Storage limit not saved: ${cause instanceof Error ? cause.message : 'request failed'}`);
    }
  };
  return (
    <section aria-label="Settings" className="settings-panel">
      <div className="utility-heading"><ShieldCheck size={16} /> Settings</div>
      <p className="settings-copy">This local tool keeps captured traffic and certificates on this device.</p>
      <h2 className="utility-heading">Capture storage</h2>
      <dl>
        <div><dt>Retained capture usage</dt><dd>{storage ? `${(storage.usedBytes / 1048576).toFixed(2)} MiB` : 'Unavailable'}</dd></div>
        <div><dt>Capture budget</dt><dd>{storage ? `${storage.limitBytes / 1048576} MiB` : 'Unavailable'}</dd></div>
        <div><dt>Capture persistence</dt><dd>{storage ? storage.paused ? 'Paused' : 'Active' : 'Unavailable'}</dd></div>
        <div><dt>Skipped records</dt><dd>{storage?.skippedRecords ?? 'Unavailable'}</dd></div>
      </dl>
      <form className="storage-editor" onSubmit={(event) => { event.preventDefault(); void save(); }}>
        <label>Capture limit (MiB)<input inputMode="numeric" value={limit} disabled={saving} aria-invalid={!valid}
          aria-describedby="storage-limit-help" onChange={(event) => { setDraft(event.target.value); setSaveError(''); setSaved(false); }} /></label>
        <p className="settings-copy" id="storage-limit-help">Use a whole number from 1 to 1048576 MiB. Default: 1024 MiB (1 GiB).</p>
        <button className="quiet-button" type="submit" disabled={!valid || saving || !storage}>{saving ? 'Saving storage limit...' : 'Save storage limit'}</button>
        {saveError && <p className="api-error" role="alert">{saveError}</p>}
        {saved && <p className="settings-copy">Storage limit saved.</p>}
      </form>
      <p className="settings-copy">This is a retained capture-data budget, not a disk-space limit. It excludes Site Map data, indexes, settings, certificates, SQLite page overhead and journal files.</p>
      <p className="settings-copy">Existing captures are never automatically deleted. Increase the budget above current usage to resume paused capture. Missed traffic cannot be recovered.</p>
      <p className="settings-copy">{status.caTrust === 'manual'
        ? 'Install and trust the local CA certificate in your operating system or browser before intercepting HTTPS traffic.'
        : 'No local CA is configured, so HTTPS interception is unavailable.'}</p>
      <dl>
        <div><dt>API address</dt><dd>{status.apiAddr}</dd></div>
        <div><dt>Proxy address</dt><dd>{status.proxyAddr}</dd></div>
        <div><dt>CA fingerprint</dt><dd>{status.caFingerprint || 'Unavailable'}</dd></div>
        <div><dt>CA trust</dt><dd className={status.caTrust === 'manual' ? 'ok' : ''}>{status.caTrust === 'manual' ? 'Manual setup required' : 'Unavailable'}</dd></div>
        <div><dt>HTTPS interception</dt><dd className={status.httpsInterception ? 'ok' : ''}>{status.httpsInterception ? 'Enabled' : 'Disabled'}</dd></div>
      </dl>
      <a className="ca-download" href={caDownloadURL}><Download size={14} /> Download CA certificate</a>
    </section>
  );
}
