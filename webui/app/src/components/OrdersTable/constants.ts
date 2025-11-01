import type { OrderColumnKey } from './types';

export const COLUMN_ORDER: OrderColumnKey[] = [
  'orderId',
  'orderType',
  'orderPosition',
  'side',
  'price',
  'quantity',
  'observedAt',
  'status',
  'historyCount',
  'actions',
];

export const COLUMN_LABELS: Record<OrderColumnKey, string> = {
  orderId: 'Client Order ID',
  botId: 'Bot ID',
  dealId: 'Deal ID',
  orderType: 'Order Type',
  orderPosition: 'Position',
  side: 'Side',
  price: 'Price',
  quantity: 'Quantity',
  observedAt: 'Observed',
  status: 'Status',
  historyCount: 'History',
  actions: 'Actions',
};

export const REQUIRED_COLUMNS = new Set<OrderColumnKey>(['orderId', 'actions']);
export const DEFAULT_VISIBLE_COLUMNS: OrderColumnKey[] = [...COLUMN_ORDER];
export const DEFAULT_FETCH_LIMIT = 100;
