import { useState } from 'react';
import type { Exchange, MessageDetail } from '../types';

type InspectorProps = {
  exchange: Exchange | null;
};

const tabs = ['Headers', 'Body', 'Raw', 'Cookies', 'Query', 'Timing', 'Audit'] as const;
type Tab = (typeof tabs)[number];

function formatHeaders(headers: Record<string, string[]>) {
  return Object.entries(headers).flatMap(([name, values]) => values.map((value) => `${name}: ${value}`)).join('\n');
}

function formatBody(message: MessageDetail, label: string) {
  if (!message.textSafe) return `${label} body is binary and is not rendered as text.`;
  if (!message.body) return `${label} has no body.`;
  return message.body + (message.truncated ? '\n\n[Capture truncated]' : '');
}

export function Inspector({ exchange }: InspectorProps) {
  const [activeTab, setActiveTab] = useState<Tab>('Headers');
  const content: Record<Tab, string> | null = exchange
    ? {
        Headers: `Request\n${formatHeaders(exchange.request.headers)}\n\nResponse\n${formatHeaders(exchange.response.headers)}`,
        Body: `Request\n${formatBody(exchange.request, 'Request')}\n\nResponse\n${formatBody(exchange.response, 'Response')}`,
        Raw: `Request\n${exchange.request.raw || 'Raw request unavailable.'}\n\nResponse\n${exchange.response.raw || 'Raw response unavailable.'}`,
        Cookies: `Request\n${exchange.request.headers.Cookie?.join('\n') || 'No request cookies.'}\n\nResponse\n${exchange.response.headers['Set-Cookie']?.join('\n') || 'No response cookies.'}`,
        Query: exchange.query || 'No query parameters.',
        Timing: `Started: ${new Date(exchange.startedAt).toLocaleTimeString()}\nDuration: ${exchange.durationMs} ms`,
        Audit: `Request intercepted: ${exchange.intercepted ? 'Yes' : 'No'}\nResponse intercepted: ${exchange.responseIntercepted ? 'Yes' : 'No'}\nApplied replacement rule IDs (in order):\n${exchange.appliedRuleIds?.join('\n') || 'None'}\n\nHistory shows final transmitted messages; before-images are not retained.${exchange.errorMessage ? `\nError: ${exchange.errorMessage}` : ''}`,
      }
    : null;

  return (
    <>
      <div className="tab-strip" role="tablist" aria-label="Exchange details">
        {tabs.map((tab) => (
          <button aria-selected={activeTab === tab} className={`tab ${activeTab === tab ? 'active' : ''}`} key={tab} onClick={() => setActiveTab(tab)} role="tab" type="button">
            {tab}
          </button>
        ))}
      </div>
      {exchange && <div className="inspector-meta"><span>{exchange.method} {exchange.scheme}://{exchange.host}{exchange.path}</span></div>}
      {content ? (
        <pre className="code-view" role="tabpanel"><code>{content[activeTab]}</code></pre>
      ) : (
        <div className="inspector-empty" role="tabpanel">Select a request to inspect its exchange.</div>
      )}
    </>
  );
}
