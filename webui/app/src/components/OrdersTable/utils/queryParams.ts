import type { OrderFilterState, OrderRecord } from '../../../types/api';
import { DEFAULT_FETCH_LIMIT } from '../constants';

/**
 * Creates URLSearchParams from filter state for API queries
 */
export const createOrderQueryParams = (
  filters: OrderFilterState,
  options: { includeLimit?: boolean; limit?: number } = {},
): URLSearchParams => {
  const params = new URLSearchParams();

  if (filters.order_id?.trim()) {
    params.append('orderId', filters.order_id.trim());
  }
  if (filters.bot_id?.trim()) {
    params.append('bot_id', filters.bot_id.trim());
  }
  if (filters.deal_id?.trim()) {
    params.append('deal_id', filters.deal_id.trim());
  }
  if (filters.bot_event_id?.trim()) {
    params.append('bot_event_id', filters.bot_event_id.trim());
  }

  const observedFrom = toIsoString(filters.observed_from);
  if (observedFrom) {
    params.append('observed_from', observedFrom);
  }

  const observedTo = toIsoString(filters.observed_to);
  if (observedTo) {
    params.append('observed_to', observedTo);
  }

  const includeLimit = options.includeLimit ?? true;
  if (includeLimit) {
    params.append('limit', String(options.limit ?? DEFAULT_FETCH_LIMIT));
  }

  return params;
};

/**
 * Converts a date string to ISO format
 */
export const toIsoString = (value?: string): string | undefined => {
  if (!value) {
    return undefined;
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return undefined;
  }
  return date.toISOString();
};

/**
 * Extracts OrderId from SSE event data
 */
export const extractOrderIdFromEvent = (data: string): string | null => {
  if (!data) {
    return null;
  }

  try {
    const parsed = JSON.parse(data) as Partial<
      OrderRecord & {
        orderId?: string;
        identifiers?: { hex?: string };
      }
    >;

    return parsed.order_id ?? parsed.identifiers?.hex ?? null;
  } catch {
    return null;
  }
};
