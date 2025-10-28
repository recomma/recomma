import type { OrderRecord, ThreeCommasBotEvent } from '../../../types/api';
import type { StatusTone } from '../types';
import { formatStatusLabel } from './orderFormatters';

/**
 * Gets the ThreeCommas event from an order record
 */
export const getThreeCommasEvent = (order: OrderRecord): ThreeCommasBotEvent =>
  order.three_commas.event;

/**
 * Determines the current status of an order
 */
export const determineStatus = (order: OrderRecord): string | undefined => {
  const hyperliquidStatus = order.hyperliquid?.latest_status?.status;
  if (typeof hyperliquidStatus === 'string' && hyperliquidStatus.trim().length > 0) {
    return hyperliquidStatus;
  }

  const eventStatus = getThreeCommasEvent(order)?.status;
  if (typeof eventStatus === 'string' && eventStatus.trim().length > 0) {
    return eventStatus;
  }

  const submission = order.hyperliquid?.latest_submission;
  if (submission?.kind === 'cancel') {
    return 'cancelled';
  }

  const action = getThreeCommasEvent(order)?.action;
  if (typeof action === 'string' && action.toLowerCase().includes('cancel')) {
    return 'cancelled';
  }

  return undefined;
};

/**
 * Determines the visual tone/color for a status
 */
export const determineStatusTone = (normalizedStatus?: string): StatusTone => {
  if (!normalizedStatus) {
    return 'neutral';
  }
  if (['filled', 'completed', 'executed'].includes(normalizedStatus)) {
    return 'success';
  }
  if (
    [
      'failed',
      'error',
      'cancelled',
      'canceled',
      'rejected',
      'margincanceled',
      'selftradecanceled',
      'reduceonlycanceled',
      'badalopxrejected',
      'ioccancelrejected',
    ].includes(normalizedStatus)
  ) {
    return 'danger';
  }
  if (['pending', 'processing'].includes(normalizedStatus)) {
    return 'warning';
  }
  if (['open', 'active', 'submitted', 'triggered'].includes(normalizedStatus)) {
    return 'info';
  }
  return 'neutral';
};

/**
 * Gets formatted status label and tone for an order
 */
export const getStatusInfo = (order: OrderRecord): { label: string; tone: StatusTone } => {
  const status = determineStatus(order);
  const normalized = status?.toLowerCase();
  return {
    label: formatStatusLabel(status),
    tone: determineStatusTone(normalized),
  };
};
