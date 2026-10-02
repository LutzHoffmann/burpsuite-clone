import { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { ApiWorkspace } from './ApiWorkspace';
import type { ApiWorkspaceState } from './ApiWorkspace';
import type { SendRequest } from '../types';

const spec = JSON.stringify({ openapi: '3.0.3', servers: [{ url: 'https://api.example.test/v1' }], paths: { '/items/{id}': { get: { summary: 'Read item' } } } });

it('imports OpenAPI JSON and hands an editable draft to Repeater without sending traffic', async () => {
  const sent: SendRequest[] = [];
  const network = vi.fn();
  vi.stubGlobal('fetch', network);
  function Fixture() {
    const [value, onChange] = useState<ApiWorkspaceState>({ raw: '', baseUrl: '', inventory: null });
    return <ApiWorkspace value={value} onChange={onChange} onSendToRepeater={(draft) => sent.push(draft)} />;
  }
  render(<Fixture />);
  fireEvent.change(screen.getByLabelText('OpenAPI JSON'), { target: { value: spec } });
  await userEvent.click(screen.getByRole('button', { name: 'Import specification' }));
  expect(screen.getByText('Read item')).toBeInTheDocument();
  expect(screen.getByLabelText('API base URL')).toHaveValue('https://api.example.test/v1');
  await userEvent.click(screen.getByRole('button', { name: 'Send GET /items/{id} to Repeater' }));
  expect(sent).toEqual([{ method: 'GET', url: 'https://api.example.test/v1/items/__id__', headers: {}, body: '' }]);
  expect(network).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});

it('reports unsupported documents without showing stale endpoints', async () => {
  function Fixture() {
    const [value, onChange] = useState<ApiWorkspaceState>({ raw: '', baseUrl: '', inventory: null });
    return <ApiWorkspace value={value} onChange={onChange} onSendToRepeater={() => {}} />;
  }
  render(<Fixture />);
  fireEvent.change(screen.getByLabelText('OpenAPI JSON'), { target: { value: '{"swagger":"2.0","paths":{}}' } });
  await userEvent.click(screen.getByRole('button', { name: 'Import specification' }));
  expect(screen.getByRole('alert')).toHaveTextContent(/OpenAPI 3/);
  expect(screen.queryByText('Read item')).not.toBeInTheDocument();
});

it('loads a selected JSON file into the import editor', async () => {
  function Fixture() {
    const [value, onChange] = useState<ApiWorkspaceState>({ raw: '', baseUrl: '', inventory: null });
    return <ApiWorkspace value={value} onChange={onChange} onSendToRepeater={() => {}} />;
  }
  render(<Fixture />);
  const file = new File([spec], 'api.json', { type: 'application/json' });
  Object.defineProperty(file, 'text', { value: async () => spec });
  await userEvent.upload(screen.getByLabelText('Choose JSON file'), file);
  expect(await screen.findByLabelText('OpenAPI JSON')).toHaveValue(spec);
});

it('clears a prior inventory when a replacement file exceeds the limit', async () => {
  function Fixture() {
    const [value, onChange] = useState<ApiWorkspaceState>({ raw: spec, baseUrl: '', inventory: null });
    return <ApiWorkspace value={value} onChange={onChange} onSendToRepeater={() => {}} />;
  }
  render(<Fixture />);
  await userEvent.click(screen.getByRole('button', { name: 'Import specification' }));
  expect(screen.getByText('Read item')).toBeInTheDocument();
  const file = new File(['x'.repeat(1_048_577)], 'too-large.json', { type: 'application/json' });
  await userEvent.upload(screen.getByLabelText('Choose JSON file'), file);
  expect(screen.getByRole('alert')).toHaveTextContent(/limit/);
  expect(screen.queryByText('Read item')).not.toBeInTheDocument();
});
