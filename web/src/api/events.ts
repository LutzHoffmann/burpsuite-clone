export interface ServerEvent<T = unknown> {
  type: string;
  data: T;
}

export const connectEvents = (onEvent: (event: ServerEvent) => void): (() => void) => {
  if (typeof WebSocket === 'undefined') {
    return () => undefined;
  }

  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(`${protocol}//${window.location.host}/api/events`);
  socket.addEventListener('message', (message) => {
    try {
      onEvent(JSON.parse(message.data) as ServerEvent);
    } catch {
      // Ignore malformed local event messages and keep the UI connected.
    }
  });
  return () => socket.close();
};
