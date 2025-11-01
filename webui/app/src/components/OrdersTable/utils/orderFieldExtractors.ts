import type {
  HyperliquidAction,
  HyperliquidCancelAction,
  HyperliquidCreateAction,
  HyperliquidCreateOrder,
  HyperliquidModifyAction,
  OrderIdentifiers,
  OrderRecord,
} from '../../../types/api';
import type { SideVariant } from '../types';
import { getThreeCommasEvent } from './orderStatus';

/**
 * Type guard for create or modify actions
 */
const isCreateOrModifyAction = (
  action: HyperliquidAction | undefined,
): action is HyperliquidCreateAction | HyperliquidModifyAction =>
  action?.kind === 'create' || action?.kind === 'modify';

/**
 * Gets the submission order from an action
 */
const getSubmissionOrder = (
  action: HyperliquidAction | undefined,
): HyperliquidCreateOrder | undefined => {
  if (isCreateOrModifyAction(action)) {
    return action.order;
  }
  return undefined;
};

/**
 * Gets cancel payload from an action
 */
const getCancelPayload = (
  action: HyperliquidAction | undefined,
): HyperliquidCancelAction['cancel'] | undefined => {
  if (action?.kind === 'cancel') {
    return action.cancel;
  }
  return undefined;
};

/**
 * Gets OrderId hex from order
 */
export const getOrderIdHex = (order: OrderRecord): string =>
  order.order_id ?? order.identifiers.hex;

/**
 * Gets identifiers from order
 */
export const getIdentifiers = (order: OrderRecord): OrderIdentifiers => order.identifiers;

/**
 * Coerces a value to a number
 */
const coerceToNumber = (value: unknown): number | undefined => {
  if (value === null || value === undefined) return undefined;
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : undefined;
  }

  if (typeof value === 'string') {
    const parsed = parseFloat(value.replace(/,/g, ''));
    return Number.isFinite(parsed) ? parsed : undefined;
  }

  return undefined;
};

/**
 * Picks the first numeric value from a list of candidates
 */
const pickNumericValue = (...values: unknown[]): number | undefined => {
  for (const value of values) {
    const numeric = coerceToNumber(value);
    if (numeric !== undefined) {
      return numeric;
    }
  }

  return undefined;
};

/**
 * Extracts order position (e.g., "1/3")
 */
export const extractOrderPosition = (order: OrderRecord): string => {
  const event = getThreeCommasEvent(order);
  const position = typeof event.order_position === 'number' ? event.order_position : undefined;
  const size = typeof event.order_size === 'number' ? event.order_size : undefined;

  if (position !== undefined && size !== undefined && size > 0) {
    return `${position}/${size}`;
  }
  if (position !== undefined) {
    return position.toString();
  }
  return '-';
};

/**
 * Extracts order side (BUY/SELL)
 */
export const extractSide = (order: OrderRecord): string => {
  const submission = order.hyperliquid?.latest_submission;
  if (isCreateOrModifyAction(submission)) {
    return submission.order.is_buy ? 'BUY' : 'SELL';
  }

  const statusSide = order.hyperliquid?.latest_status?.order?.side;
  if (typeof statusSide === 'string') {
    const normalized = statusSide.toUpperCase();
    if (normalized === 'B') return 'BUY';
    if (normalized === 'A' || normalized === 'S') return 'SELL';
    return normalized;
  }

  const cancel = getCancelPayload(submission);
  if (cancel?.coin) {
    const eventType = getThreeCommasEvent(order)?.type;
    if (typeof eventType === 'string' && eventType.trim().length > 0) {
      return eventType.toUpperCase();
    }
  }

  const event = getThreeCommasEvent(order);
  if (typeof event.type === 'string' && event.type.trim().length > 0) {
    return event.type.toUpperCase();
  }

  if (typeof event.action === 'string') {
    const normalizedAction = event.action.toLowerCase();
    if (normalizedAction.includes('sell')) return 'SELL';
    if (normalizedAction.includes('buy')) return 'BUY';
  }

  return '-';
};

/**
 * Extracts order type (base, safety, take_profit, etc.)
 */
export const extractOrderType = (order: OrderRecord): string => {
  const eventOrderType = getThreeCommasEvent(order)?.order_type;
  if (typeof eventOrderType === 'string' && eventOrderType.trim()) {
    return eventOrderType.replace(/_/g, ' ');
  }
  return '-';
};

/**
 * Extracts coin/asset from order
 */
export const extractCoin = (order: OrderRecord): string => {
  const event = getThreeCommasEvent(order);
  const submissionOrder = getSubmissionOrder(order.hyperliquid?.latest_submission);
  const statusOrder = order.hyperliquid?.latest_status?.order;
  const cancelPayload = getCancelPayload(order.hyperliquid?.latest_submission);

  return (
    event?.coin ||
    submissionOrder?.coin ||
    statusOrder?.coin ||
    cancelPayload?.coin ||
    'N/A'
  );
};

/**
 * Extracts boolean is_buy flag from order
 */
export const extractIsBuy = (order: OrderRecord): boolean | null => {
  const submissionOrder = getSubmissionOrder(order.hyperliquid?.latest_submission);
  if (submissionOrder) {
    return submissionOrder.is_buy;
  }

  const statusSide = order.hyperliquid?.latest_status?.order?.side;
  if (typeof statusSide === 'string') {
    const normalized = statusSide.toUpperCase();
    if (normalized === 'B') return true;
    if (normalized === 'A' || normalized === 'S') return false;
  }

  const event = getThreeCommasEvent(order);
  if (typeof event.type === 'string') {
    const normalized = event.type.toUpperCase();
    if (normalized === 'BUY') return true;
    if (normalized === 'SELL') return false;
  }

  return null;
};

/**
 * Gets numeric price from order
 */
export const getNumericPrice = (order: OrderRecord): number | undefined => {
  const statusOrder = order.hyperliquid?.latest_status?.order;
  const submissionOrder = getSubmissionOrder(order.hyperliquid?.latest_submission);
  const event = getThreeCommasEvent(order);
  return pickNumericValue(statusOrder?.limit_px, submissionOrder?.price, event?.price);
};

/**
 * Gets numeric quantity from order
 */
export const getNumericQuantity = (order: OrderRecord): number | undefined => {
  const statusOrder = order.hyperliquid?.latest_status?.order;
  const submissionOrder = getSubmissionOrder(order.hyperliquid?.latest_submission);
  const event = getThreeCommasEvent(order);
  return pickNumericValue(
    statusOrder?.size,
    statusOrder?.orig_size,
    submissionOrder?.size,
    event?.size,
  );
};

/**
 * Gets side variant for styling
 */
export const getSideVariant = (side: string): SideVariant => {
  if (side.toUpperCase() === 'BUY') {
    return 'buy';
  }
  if (side.toUpperCase() === 'SELL') {
    return 'sell';
  }
  return 'neutral';
};
