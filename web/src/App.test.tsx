import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { App } from './App';
import { Inspector } from './components/Inspector';

test('renders operator shell status', () => {
  render(<App />);
  expect(screen.getByText('Proxy')).toBeInTheDocument();
  expect(screen.getByText('History')).toBeInTheDocument();
  expect(screen.getByText('Repeater')).toBeInTheDocument();
});

test('shows history columns and inspector tabs', () => {
  render(<App />);
  expect(screen.getByText('Method')).toBeInTheDocument();
  expect(screen.getByText('Host')).toBeInTheDocument();
  expect(screen.getByText('Status')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Headers' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Body' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Raw' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Cookies' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Query' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Timing' })).toBeInTheDocument();
});

test('keeps inspector tabs available when no exchange is selected', () => {
  render(<Inspector exchange={null} />);

  expect(screen.getByRole('tab', { name: 'Headers' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Timing' })).toBeInTheDocument();
  expect(screen.getByText('Select a request to inspect its exchange.')).toBeInTheDocument();
});

test('shows intercept and repeater controls', () => {
  render(<App />);
  expect(screen.getByText('Intercept Queue')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Forward' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Drop' })).toBeInTheDocument();
  expect(screen.getByText('Request Editor')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
});
