import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, expect, test, vi } from 'vitest';
import { WSRepeater } from './WSRepeater';

const api = vi.hoisted(() => ({ send: vi.fn(), draft: vi.fn() }));
vi.mock('../api/client', () => ({ sendWSRepeater: api.send, getWSRepeaterDraft: api.draft }));
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.restoreAllMocks(); });
const draft = { url: 'ws://test.example/socket', type: 'text', payload: 'hello', payloadFormat: 'text', headers: {}, subprotocols: [] };
const result = { sent: true, outcome: 'peer_closed', durationMs: 50, subprotocol: '', messages: [{ type: 'text', payload: '<img src=x onerror=alert(1)>', payloadFormat: 'text', size: 26, truncated: false }] };
const change = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

test('imports explicit draft, edits and sends on independent connection, renders safely', async () => {
 api.draft.mockResolvedValue(draft); api.send.mockResolvedValue(result);
 render(<WSRepeater source={{ connectionId: 1, messageId: 2, revision: 1 }} />);
 await act(async () => {});
 expect(screen.getByLabelText('WebSocket URL')).toHaveValue(draft.url);
 change('Message payload', 'changed');
 change('Handshake headers', 'Authorization: Bearer test\nCookie: session=test');
 change('Subprotocols', 'one, two');
 fireEvent.click(screen.getByRole('button', { name: 'Send WebSocket message' }));
 await act(async () => {});
 expect(api.send).toHaveBeenCalledWith(expect.objectContaining({ payload: 'changed', headers: { Authorization: ['Bearer test'], Cookie: ['session=test'] }, subprotocols: ['one', 'two'] }), expect.any(AbortSignal));
 const response = screen.getByLabelText('WebSocket repeater results');
 expect(response).toHaveTextContent('<img src=x onerror=alert(1)>');
 expect(response.querySelector('img')).toBeNull();
 expect(response).toHaveTextContent('peer_closed');
});

test('cancel aborts and ignores a late response', async () => {
 let complete!: (value: typeof result) => void;
 api.send.mockImplementation(() => new Promise((resolve) => { complete = resolve; }));
 render(<WSRepeater />);
 change('WebSocket URL', draft.url);
 fireEvent.click(screen.getByRole('button', { name: 'Send WebSocket message' }));
 await act(async () => {});
 expect(screen.getByRole('button', { name: 'Send WebSocket message' })).toBeDisabled();
 fireEvent.click(screen.getByRole('button', { name: 'Cancel send' }));
 expect(api.send.mock.calls[0][1].aborted).toBe(true);
 await act(async () => complete(result));
 expect(screen.queryByText('<img src=x onerror=alert(1)>')).not.toBeInTheDocument();
 expect(screen.getByText(/Canceled/)).toBeInTheDocument();
});

test('invalid hex is rejected before sending', async () => {
 render(<WSRepeater />); change('WebSocket URL', draft.url);
 change('Payload format', 'hex'); change('Message payload', 'zz');
 fireEvent.click(screen.getByRole('button', { name: 'Send WebSocket message' }));
 await act(async () => {});
 expect(api.send).not.toHaveBeenCalled();
 expect(screen.getByRole('status')).toHaveTextContent(/hex/i);
});

test('dirty editor is retained when source replacement is declined', async () => {
 const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
 const { rerender } = render(<WSRepeater />);
 change('Message payload', 'keep this');
 rerender(<WSRepeater source={{ connectionId: 1, messageId: 2, revision: 1 }} />);
 await act(async () => {});
 expect(confirm).toHaveBeenCalled();
 expect(api.draft).not.toHaveBeenCalled();
 expect(screen.getByLabelText('Message payload')).toHaveValue('keep this');
});

test('unmount aborts active send and navigation guard warns on unsent edits', async () => {
 api.send.mockImplementation(() => new Promise(() => {}));
 vi.spyOn(window, 'confirm').mockReturnValue(false);
 const guard = { current: () => true };
 const { unmount } = render(<WSRepeater leaveGuard={guard} />);
 change('WebSocket URL', draft.url);
 expect(guard.current()).toBe(false);
 fireEvent.click(screen.getByRole('button', { name: 'Send WebSocket message' }));
 await act(async () => {});
 unmount();
 expect(api.send.mock.calls[0][1].aborted).toBe(true);
});
