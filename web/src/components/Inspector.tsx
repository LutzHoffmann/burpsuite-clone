import { useState } from 'react';
import type { Exchange } from '../types';

type InspectorProps = {
  exchange: Exchange | null;
};

const tabs = ['Headers', 'Body', 'Raw', 'Cookies', 'Query', 'Timing'] as const;
type Tab = (typeof tabs)[number];

function formatHeaders(headers: Record<string, string[]>) {
  return Object.entries(headers).flatMap(([name, values]) => values.map((value) => `${name}: ${value}`)).join('\n');
}

export function Inspector({ exchange }: InspectorProps) {
  const [activeTab, setActiveTab] = useState<Tab>('Headers');

  if (!exchange) {
    return <div className="inspector-empty">Select a request to inspect its exchange.</div>;
  }

  const content: Record<Tab, string> = {
    Headers: formatHeaders(exchange.Request.Headers),
    Body: exchange.Request.Body || 'Request has no body.',
    Raw: exchange.Request.Raw,
    Cookies: exchange.Request.Headers.Cookie?.join('\n') || 'No request cookies.',
    Query: exchange.Query || 'No query parameters.',
    Timing: `Started: ${new Date(exchange.StartedAt).toLocaleTimeString()}\nDuration: ${exchange.Duration} ms`,
  };

  return (
    <>
      <div className="tab-strip" role="tablist" aria-label="Exchange details">
        {tabs.map((tab) => (
          <button aria-selected={activeTab === tab} className={`tab ${activeTab === tab ? 'active' : ''}`} key={tab} onClick={() => setActiveTab(tab)} role="tab" type="button">
            {tab}
          </button>
        ))}
      </div>
      <div className="inspector-meta"><span>{exchange.Method} {exchange.Scheme}://{exchange.Host}{exchange.Path}</span></div>
      <pre className="code-view" role="tabpanel"><code>{content[activeTab]}</code></pre>
    </>
  );
}
