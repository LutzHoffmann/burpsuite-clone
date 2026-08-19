import { Download, ShieldCheck } from 'lucide-react';
import { caDownloadURL } from '../api/client';
import type { StatusDTO } from '../types';

type SettingsProps = {
  status: StatusDTO;
};

export function Settings({ status }: SettingsProps) {
  return (
    <section aria-label="Settings" className="settings-panel">
      <div className="utility-heading"><ShieldCheck size={16} /> Settings</div>
      <p className="settings-copy">This local tool keeps captured traffic and certificates on this device.</p>
      <dl>
        <div><dt>API address</dt><dd>{status.apiAddr}</dd></div>
        <div><dt>Proxy address</dt><dd>{status.proxyAddr}</dd></div>
        <div><dt>CA fingerprint</dt><dd>{status.caFingerprint || 'Unavailable'}</dd></div>
        <div><dt>HTTPS interception</dt><dd className={status.httpsInterception ? 'ok' : ''}>{status.httpsInterception ? 'Enabled' : 'Disabled'}</dd></div>
      </dl>
      <a className="ca-download" href={caDownloadURL}><Download size={14} /> Download CA certificate</a>
    </section>
  );
}
