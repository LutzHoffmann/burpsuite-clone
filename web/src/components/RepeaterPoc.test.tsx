import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { Repeater } from './Repeater';

it('generates a manual CSRF form from the current Repeater draft', async () => {
  render(<Repeater initialRequest={{ method: 'POST', url: 'https://example.test/update', headers: { 'Content-Type': ['application/x-www-form-urlencoded'] }, body: 'name=Alice' }} result={null} onSend={() => {}} />);
  await userEvent.click(screen.getByRole('button', { name: 'Generate CSRF PoC' }));
  expect((screen.getByLabelText('CSRF PoC HTML') as HTMLTextAreaElement).value).toContain('name="name" value="Alice"');
  expect(screen.getByText(/manual submission/i)).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText('Body Text-safe editing only'), '&extra=1');
  expect(screen.queryByLabelText('CSRF PoC HTML')).not.toBeInTheDocument();
});

it('explains when a request cannot be represented by an HTML form', async () => {
  render(<Repeater initialRequest={{ method: 'PUT', url: 'https://example.test/update', headers: {}, body: '{}' }} result={null} onSend={() => {}} />);
  await userEvent.click(screen.getByRole('button', { name: 'Generate CSRF PoC' }));
  expect(screen.getByRole('alert')).toHaveTextContent(/GET or POST/);
});
