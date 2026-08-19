import { BadgeCheck, FolderKanban, Radio, ShieldCheck } from 'lucide-react';

export function StatusBar() {
  return (
    <header className="status-bar">
      <div className="brand"><ShieldCheck size={17} /> intercept</div>
      <div className="status-items">
        <span><Radio size={14} /> Proxy <b>127.0.0.1:8080</b></span>
        <span><BadgeCheck size={14} /> CA <b>ready</b></span>
        <span><FolderKanban size={14} /> Project <b>local</b></span>
        <span className="intercept-status"><i /> Intercept <b>off</b></span>
      </div>
    </header>
  );
}
