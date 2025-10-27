import type { DealRecord, OrderRecord } from '../../types/api';

export type StatusTone = 'success' | 'danger' | 'warning' | 'info' | 'neutral';

export const statusToneClasses: Record<StatusTone, string> = {
  success: 'bg-green-100 text-green-800 border-green-200 text-xs',
  danger: 'bg-red-100 text-red-800 border-red-200 text-xs',
  warning: 'bg-yellow-100 text-yellow-800 border-yellow-200 text-xs',
  info: 'bg-blue-100 text-blue-800 border-blue-200 text-xs',
  neutral: 'bg-gray-100 text-gray-800 border-gray-200 text-xs',
};

export type SideVariant = 'buy' | 'sell' | 'neutral';

export type OrderColumnKey =
  | 'metadata'
  | 'botId'
  | 'dealId'
  | 'orderType'
  | 'orderPosition'
  | 'side'
  | 'price'
  | 'quantity'
  | 'observedAt'
  | 'status'
  | 'historyCount'
  | 'actions';

export type OrderRow = {
  rowType: 'order';
  id: string;
  metadata: string;
  botId: string;
  dealId: string;
  orderType: string;
  orderPosition: string;
  side: string;
  sideVariant: SideVariant;
  price: number | null;
  quantity: number | null;
  observedAt: string;
  observedAtTs: number;
  status: string;
  statusTone: StatusTone;
  historyCount: number;
  actions: string;
  coin: string;
  isBuy: boolean | null;
  latest: OrderRecord;
  history: OrderRecord[];
};

export type DealGroupRow = {
  rowType: 'deal-header';
  id: string;
  dealId: string;
  botId: string;
  deal: DealRecord | null;
  orderCount: number;
  metadataHashes: Set<string>;
  metadata: string; // For column compatibility
  orderType: string;
  orderPosition: string;
  side: string;
  sideVariant: SideVariant;
  price: number | null;
  quantity: number | null;
  observedAt: string;
  observedAtTs: number;
  status: string;
  statusTone: StatusTone;
  historyCount: number;
  actions: string;
  coin: string;
  isBuy: boolean | null;
};

export type TableRow = OrderRow | DealGroupRow;

export interface OrderGroup {
  key: string;
  latest: OrderRecord;
  history: OrderRecord[];
}

export interface FetchOrdersOptions {
  showSpinner?: boolean;
  signal?: AbortSignal;
}
