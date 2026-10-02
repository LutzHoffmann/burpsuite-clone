import { useLayoutEffect } from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, expect, test, vi } from 'vitest';
import { InterceptPanel } from './InterceptPanel';
import type { InterceptItem } from '../types';

afterEach(cleanup);
const item: InterceptItem = { id: 'early', phase: 'response', method: 'GET', url: 'https://example.test/', statusCode: 201, headers: {}, body: 'before', bodyEditable: true, bodyTruncated: false };

test.each([
  ['Response status early', '202'],
  ['Response body early', 'first edit'],
  ['Response headers early', 'X-Edit: first'],
])('initial passive effects cannot overwrite an early edit to %s', async (label, value) => {
  function EarlyEdit() {
    useLayoutEffect(() => {
      // Dispatch before passive effects without nesting Testing Library's act wrapper.
      const field = screen.getByLabelText(label) as HTMLInputElement | HTMLTextAreaElement;
      const prototype = field instanceof HTMLInputElement ? HTMLInputElement.prototype : HTMLTextAreaElement.prototype;
      Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(field, value);
      field.dispatchEvent(new Event('input', { bubbles: true }));
    }, []);
    return <InterceptPanel phase="response" enabled items={[item]} onEnabledChange={vi.fn()} onForward={vi.fn()} onDrop={vi.fn()} />;
  }
  await act(async () => { render(<EarlyEdit />); });
  expect(screen.getByLabelText(label)).toHaveValue(label.includes('status') ? Number(value) : value);
});

test('refresh keeps edits for same id while a new queue id initializes a new editor', () => {
  const props = { phase: 'response' as const, enabled: true, onEnabledChange: vi.fn(), onForward: vi.fn(), onDrop: vi.fn() };
  const { rerender } = render(<InterceptPanel {...props} items={[item]} />);
  fireEvent.change(screen.getByLabelText('Response status early'), { target: { value: '202' } });
  rerender(<InterceptPanel {...props} items={[{ ...item, statusCode: 203 }]} />);
  expect(screen.getByLabelText('Response status early')).toHaveValue(202);
  rerender(<InterceptPanel {...props} items={[{ ...item, id: 'new', statusCode: 204 }]} />);
  expect(screen.getByLabelText('Response status new')).toHaveValue(204);
});
