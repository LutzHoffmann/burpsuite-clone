export interface APIEvent<T = unknown> {
  type: string;
  data: T;
}

export const connectEvents = (onEvent: (event: APIEvent) => void): WebSocket => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(`${protocol}//${window.location.host}/api/events`);
  socket.addEventListener('message', (message) => onEvent(JSON.parse(message.data) as APIEvent));
  return socket;
};
