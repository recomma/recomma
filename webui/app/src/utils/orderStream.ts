const ORDER_EVENT_TYPES = [
  'message',
  'three_commas_event',
  'hyperliquid_submission',
  'hyperliquid_status',
] as const;

export type OrderEventType = (typeof ORDER_EVENT_TYPES)[number];

export type OrderStreamHandler = (event: MessageEvent<string>) => void;

export function attachOrderStreamHandlers(
  eventSource: EventSource,
  handler: OrderStreamHandler,
): () => void {
  for (const eventType of ORDER_EVENT_TYPES) {
    eventSource.addEventListener(eventType, handler as EventListener);
  }

  return () => {
    for (const eventType of ORDER_EVENT_TYPES) {
      eventSource.removeEventListener(eventType, handler as EventListener);
    }
  };
}
