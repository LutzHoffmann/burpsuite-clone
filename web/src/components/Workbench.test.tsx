import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { Workbench } from './Workbench';

it('encodes and decodes text locally', async () => {
  render(<Workbench />);
  await userEvent.type(screen.getByLabelText('Input text'), 'hello world');
  await userEvent.click(screen.getByRole('button', { name: 'Transform' }));
  expect(screen.getByLabelText('Output text')).toHaveValue('hello%20world');
  await userEvent.click(screen.getByRole('button', { name: 'Use output as input' }));
  await userEvent.selectOptions(screen.getByLabelText('Direction'), 'decode');
  await userEvent.click(screen.getByRole('button', { name: 'Transform' }));
  expect(screen.getByLabelText('Output text')).toHaveValue('hello world');
});

it('shows invalid decoding errors without misleading output', async () => {
  render(<Workbench />);
  await userEvent.type(screen.getByLabelText('Input text'), '%GG');
  await userEvent.selectOptions(screen.getByLabelText('Direction'), 'decode');
  await userEvent.click(screen.getByRole('button', { name: 'Transform' }));
  expect(screen.getByRole('alert')).toHaveTextContent(/invalid|malformed/i);
  expect(screen.getByLabelText('Output text')).toHaveValue('');
});

it('compares text and reports added and removed lines', async () => {
  render(<Workbench />);
  await userEvent.click(screen.getByRole('button', { name: 'Comparer' }));
  await userEvent.type(screen.getByLabelText('Left text'), 'old');
  await userEvent.type(screen.getByLabelText('Right text'), 'new');
  await userEvent.click(screen.getByRole('button', { name: 'Compare text' }));
  expect(screen.getByText('- old')).toBeInTheDocument();
  expect(screen.getByText('+ new')).toBeInTheDocument();
});

it('shows token metrics with an explicit security caveat', async () => {
  render(<Workbench />);
  await userEvent.click(screen.getByRole('button', { name: 'Token analysis' }));
  await userEvent.type(screen.getByLabelText('Token text'), 'abab');
  await userEvent.click(screen.getByRole('button', { name: 'Analyze token' }));
  expect(screen.getByText('4 characters')).toBeInTheDocument();
  expect(screen.getByText('1.00 bits/character')).toBeInTheDocument();
  expect(screen.getByText(/not a security guarantee/i)).toBeInTheDocument();
});

it('inspects JWT claims without claiming signature verification', async () => {
  const segment = (value: object) => btoa(JSON.stringify(value)).replace(/=/g, '');
  render(<Workbench />);
  await userEvent.click(screen.getByRole('button', { name: 'JWT inspector' }));
  await userEvent.type(screen.getByLabelText('JWT text'), `${segment({ alg: 'HS256' })}.${segment({ sub: 'alice' })}.c2ln`);
  await userEvent.click(screen.getByRole('button', { name: 'Inspect JWT' }));
  expect(screen.getByText(/"sub": "alice"/)).toBeInTheDocument();
  expect(screen.getByText(/signature is not verified/i)).toBeInTheDocument();
});
