import { fireEvent, render, screen, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { expect, test, vi } from 'vitest';
import { InterceptRules } from './InterceptRules';
import type { InterceptConfig, ReplacementRule } from '../types';

const rule = (id: string): ReplacementRule => ({ id, enabled: true, direction: 'request', target: 'body', header: '', pattern: 'before', replacement: 'after', regex: false, hostContains: '', pathContains: '', mimeContains: '' });
const config: InterceptConfig = { enabled: true, rules: [], responseEnabled: false, responseRules: [], replacementRules: [rule('first'), rule('second')] };

test('empty legacy null rule lists remain editable after reload', () => {
  const save = vi.fn();
  render(<InterceptRules config={{ ...config, rules: null, responseRules: null, replacementRules: null } as unknown as InterceptConfig} disabled={false} onSave={save} />);
  fireEvent.click(screen.getByText('Request filters'));
  fireEvent.click(screen.getByRole('button', { name: 'Save request filters' }));
  expect(save).toHaveBeenCalledWith({ rules: [] });
  fireEvent.click(screen.getByText('Response filters'));
  fireEvent.click(screen.getByRole('button', { name: 'Save response filters' }));
  expect(save).toHaveBeenCalledWith({ responseRules: [] });
});

test('edits and saves response filters independently', () => {
  const save = vi.fn();
  render(<InterceptRules config={config} disabled={false} onSave={save} />);
  fireEvent.click(screen.getByText('Response filters'));
  fireEvent.click(screen.getByRole('button', { name: 'Add response filter' }));
  fireEvent.change(screen.getByLabelText('Response filter 1 Status code'), { target: { value: '404' } });
  fireEvent.change(screen.getByLabelText('Response filter 1 Host contains'), { target: { value: 'example.test' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save response filters' }));
  expect(save).toHaveBeenCalledWith({ responseRules: [expect.objectContaining({ statusCode: 404, hostContains: 'example.test' })] });
});

test('edits, orders, enables and deletes replacement rules before explicit save', () => {
  const save = vi.fn();
  render(<InterceptRules config={config} disabled={false} onSave={save} />);
  fireEvent.click(screen.getByText('Match and replace'));
  fireEvent.click(screen.getByRole('button', { name: 'Move replacement 2 up' }));
  const first = screen.getByRole('group', { name: 'Replacement 1' });
  expect(within(first).getByLabelText('ID')).toHaveValue('second');
  fireEvent.click(within(first).getByLabelText('Enabled'));
  fireEvent.click(within(first).getByLabelText('Regex'));
  fireEvent.change(within(first).getByLabelText('Direction'), { target: { value: 'response' } });
  fireEvent.change(within(first).getByLabelText('Target'), { target: { value: 'header' } });
  fireEvent.change(within(first).getByLabelText('Header'), { target: { value: 'X-Test' } });
  fireEvent.change(within(first).getByLabelText('Pattern'), { target: { value: '(before)' } });
  fireEvent.change(within(first).getByLabelText('Replacement'), { target: { value: '${1}-after' } });
  fireEvent.change(within(first).getByLabelText('Path contains'), { target: { value: '/api' } });
  fireEvent.change(within(first).getByLabelText('MIME contains'), { target: { value: 'json' } });
  fireEvent.click(screen.getByRole('button', { name: 'Delete replacement 2' }));
  expect(save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Save replacements' }));
  expect(save).toHaveBeenCalledWith({ replacementRules: [expect.objectContaining({ id: 'second', enabled: false, regex: true, direction: 'response', target: 'header', header: 'X-Test', pattern: '(before)', replacement: '${1}-after', pathContains: '/api', mimeContains: 'json' })] });
});

test('new replacement IDs are distinct and response URL targeting is unavailable', () => {
  render(<InterceptRules config={{ enabled: false, rules: [] }} disabled={false} onSave={vi.fn()} />);
  fireEvent.click(screen.getByText('Match and replace'));
  fireEvent.click(screen.getByRole('button', { name: 'Add replacement' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add replacement' }));
  const ids = screen.getAllByLabelText('ID') as HTMLInputElement[];
  expect(ids[0].value).not.toBe(ids[1].value);
  const first = screen.getByRole('group', { name: 'Replacement 1' });
  fireEvent.change(within(first).getByLabelText('Target'), { target: { value: 'url' } });
  fireEvent.change(within(first).getByLabelText('Direction'), { target: { value: 'response' } });
  expect(within(first).getByLabelText('Target')).toHaveValue('header');
  expect(within(first).queryByRole('option', { name: 'URL' })).not.toBeInTheDocument();
});
