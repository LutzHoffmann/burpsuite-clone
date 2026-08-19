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
