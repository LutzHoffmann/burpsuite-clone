import { fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { App } from './App';
import { Inspector } from './components/Inspector';

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test('loads status from api', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/api/status')) {
      return new Response(JSON.stringify({
        apiAddr: '127.0.0.1:9080',
        proxyAddr: '127.0.0.1:18080',
        caFingerprint: 'AA:BB',
        caTrust: 'manual',
        httpsInterception: true,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/history')) {
      return new Response(JSON.stringify([]), { status: 200 });
    }
    return new Response('{}', { status: 404 });
  }));
  render(<App />);
  expect(await screen.findByText('127.0.0.1:18080')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  expect(screen.getByText('Manual setup required')).toBeInTheDocument();
  expect(screen.getByText(/install and trust the local ca certificate/i)).toBeInTheDocument();
});

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

test('keeps the intercept queue outside the hidden utilities panel', () => {
  render(<App />);

  expect(screen.getByLabelText('Intercept Queue').closest('.utility-panel')).toBeNull();
});
