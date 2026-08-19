import { BadgeCheck, FolderKanban, Radio, ShieldCheck } from 'lucide-react';
import type { StatusDTO } from '../types';

type StatusBarProps = {
  status: StatusDTO;
};

export function StatusBar({ status }: StatusBarProps) {
  return (
    <header className="status-bar">
      <div className="brand"><ShieldCheck size={17} /> intercept</div>
      <div className="status-items">
        <span><Radio size={14} /> Proxy <b>{status.proxyAddr}</b></span>
        <span><BadgeCheck size={14} /> CA <b>{status.caFingerprint ? 'ready' : 'unavailable'}</b></span>
        <span><FolderKanban size={14} /> Project <b>local</b></span>
        <span className="intercept-status"><i className={status.httpsInterception ? 'active' : ''} /> HTTPS <b>{status.httpsInterception ? 'on' : 'off'}</b></span>
      </div>
    </header>
  );
}
