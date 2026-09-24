import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { Inspector } from './Inspector';
import type { Exchange } from '../types';

function sample(inScope = true): Exchange {
  return {
    id: 1, method: 'GET', scheme: 'https', host: 'example.test', path: '/', query: '',
    status: 200, mimeType: 'text/html', requestSize: 0, responseSize: 0, durationMs: 1,
    startedAt: '', intercepted: false, error: false, inScope, scopeVersion: 1,
    scopeRuleId: 1, errorMessage: '', requestTruncated: false, responseTruncated: false,
    request: { headers: {}, body: '', raw: '', textSafe: true, truncated: false },
    response: { headers: { 'Set-Cookie': ['session=private-value; HttpOnly'] }, body: '', raw: '', textSafe: true, truncated: false },
    tags: [], note: '',
  };
}

it('shows passive observations without cookie secrets and honors captured scope', async () => {
  const { rerender } = render(<Inspector exchange={sample()} />);
  await userEvent.click(screen.getByRole('tab', { name: 'Findings' }));
  const panel = screen.getByRole('tabpanel');
  expect(panel).toHaveTextContent('Cookie without Secure attribute');
  expect(panel).toHaveTextContent('session');
  expect(panel).not.toHaveTextContent('private-value');
  rerender(<Inspector exchange={sample(false)} />);
  expect(screen.getByRole('tabpanel')).toHaveTextContent('outside scope');
  expect(screen.getByRole('tabpanel')).not.toHaveTextContent('Cookie without Secure attribute');
});
