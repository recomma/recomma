import type { OrderRecord } from '../../../types/api';
import type { OrderGroup } from '../types';
import { getIdentifiers, getOrderIdHex } from './orderFieldExtractors';

/**
 * Builds a unique identity string for an order
 */
export const buildOrderIdentity = (order: OrderRecord): string => {
  const identifiers = getIdentifiers(order);
  return [
    getOrderIdHex(order),
    identifiers.bot_event_id?.toString() ?? 'unknown-event',
    identifiers.bot_id?.toString() ?? 'unknown-bot',
    identifiers.deal_id?.toString() ?? 'unknown-deal',
  ].join(':');
};

/**
 * Parses a timestamp value into milliseconds
 */
const parseTimestamp = (value: unknown): number | null => {
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return null;
    return value > 1e12 ? value : value * 1000;
  }

  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) {
      return null;
    }

    const parsedDate = Date.parse(trimmed);
    if (!Number.isNaN(parsedDate)) {
      return parsedDate;
    }

    const numeric = Number(trimmed);
    if (!Number.isNaN(numeric)) {
      return numeric > 1e12 ? numeric : numeric * 1000;
    }
  }

  return null;
};

/**
 * Gets the timestamp of an order in milliseconds
 */
export const getOrderTimestamp = (order: OrderRecord): number => {
  const status = order.hyperliquid?.latest_status;
  const statusOrder = status?.order;
  const candidates: unknown[] = [
    status?.status_timestamp,
    statusOrder?.timestamp,
    order.observed_at,
    order.identifiers?.created_at,
  ];

  for (const candidate of candidates) {
    const parsed = parseTimestamp(candidate);
    if (parsed !== null) {
      return parsed;
    }
  }

  return Number.MIN_SAFE_INTEGER;
};

/**
 * Groups orders by their unique identity and sorts by timestamp
 */
export const groupOrders = (items: OrderRecord[]): OrderGroup[] => {
  const grouped = new Map<string, { order: OrderRecord; timestamp: number; index: number }[]>();

  items.forEach((order, index) => {
    const key = buildOrderIdentity(order);
    const timestamp = getOrderTimestamp(order);
    const entry = { order, timestamp, index };
    const bucket = grouped.get(key);
    if (bucket) {
      bucket.push(entry);
    } else {
      grouped.set(key, [entry]);
    }
  });

  const groups: OrderGroup[] = Array.from(grouped.entries()).map(([key, entries]) => {
    const sorted = entries
      .slice()
      .sort((a, b) => {
        if (a.timestamp === b.timestamp) {
          return b.index - a.index;
        }
        return b.timestamp - a.timestamp;
      });

    const history = sorted.map((entry) => entry.order);
    const latest = history[0] ?? entries[0].order;

    return {
      key,
      latest,
      history: history.length > 0 ? history : [entries[0].order],
    };
  });

  return groups.sort((a, b) => {
    const aTime = getOrderTimestamp(a.latest);
    const bTime = getOrderTimestamp(b.latest);
    return bTime - aTime;
  });
};
