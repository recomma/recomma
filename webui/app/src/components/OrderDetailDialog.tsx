import { useEffect, useState, type MouseEvent } from 'react';
import type {
  HyperliquidAction,
  HyperliquidCancelAction,
  HyperliquidCreateOrder,
  HyperliquidWsOrder,
  ListBotsResponse,
  ListDealsResponse,
  ListOrdersResponse,
  OrderIdentifiers,
  OrderLogEntry,
  OrderRecord,
  ThreeCommasBotEvent,
  UnknownRecord,
} from '../types/api';
import { asRecord, coerceNumber, coerceString } from '../types/api';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from './ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';
import { Card } from './ui/card';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { ScrollArea } from './ui/scroll-area';
import {
  Activity,
  ArrowRight,
  Bot,
  CheckCircle2,
  Clock,
  Code,
  ExternalLink,
  XCircle,
} from 'lucide-react';
import { buildOpsApiUrl } from '../config/opsApi';

interface OrderDetailDialogProps {
  order: OrderRecord;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type StatusTone = 'success' | 'danger' | 'warning' | 'info' | 'neutral';

const statusToneClasses: Record<StatusTone, string> = {
  success: 'border-green-200 bg-gradient-to-r from-green-50 to-green-100',
  danger: 'border-red-200 bg-gradient-to-r from-red-50 to-red-100',
  warning: 'border-yellow-200 bg-gradient-to-r from-yellow-50 to-yellow-100',
  info: 'border-blue-200 bg-gradient-to-r from-blue-50 to-blue-100',
  neutral: 'border-gray-200 bg-gradient-to-r from-gray-50 to-gray-100',
};

const statusBadgeClasses: Record<StatusTone, string> = {
  success: 'bg-green-100 text-green-800 border-green-200 text-xs',
  danger: 'bg-red-100 text-red-800 border-red-200 text-xs',
  warning: 'bg-yellow-100 text-yellow-800 border-yellow-200 text-xs',
  info: 'bg-blue-100 text-blue-800 border-blue-200 text-xs',
  neutral: 'bg-gray-100 text-gray-800 border-gray-200 text-xs',
};

const formatDateTime = (value: unknown): string => {
  if (typeof value === 'number' && Number.isFinite(value)) {
    const ms = value > 1e12 ? value : value * 1000;
    return new Date(ms).toLocaleString();
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) return '—';
    const parsed = new Date(trimmed);
    if (!Number.isNaN(parsed.getTime())) {
      return parsed.toLocaleString();
    }
    const numeric = Number(trimmed);
    if (Number.isFinite(numeric)) {
      const ms = numeric > 1e12 ? numeric : numeric * 1000;
      return new Date(ms).toLocaleString();
    }
  }
  return '—';
};

const formatFixed = (value: number, minimum: number, maximum = minimum): string => {
  return value.toLocaleString('en-US', {
    minimumFractionDigits: minimum,
    maximumFractionDigits: maximum,
  });
};

const formatCurrency = (value: number, minimum = 2, maximum = 2): string => {
  return `$${formatFixed(value, minimum, maximum)}`;
};

const toTitleCase = (value: string): string => {
  return value
    .replace(/_/g, ' ')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .toLowerCase()
    .replace(/\b\w/g, (ch) => ch.toUpperCase());
};

const parseNumeric = (value: string | number | undefined | null): number | undefined => {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) {
      return undefined;
    }
    const parsed = Number(trimmed);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return undefined;
};

const isCreateOrModifyAction = (
  action: HyperliquidAction | undefined,
): action is HyperliquidAction & { order: HyperliquidCreateOrder } =>
  action?.kind === 'create' || action?.kind === 'modify';

const getSubmissionOrder = (
  action: HyperliquidAction | undefined,
): HyperliquidCreateOrder | undefined => {
  if (isCreateOrModifyAction(action)) {
    return action.order;
  }
  return undefined;
};

const getCancelPayload = (
  action: HyperliquidAction | undefined,
): HyperliquidCancelAction['cancel'] | undefined => {
  if (action?.kind === 'cancel') {
    return action.cancel;
  }
  return undefined;
};

const normalizeStatusTone = (normalizedStatus: string): StatusTone => {
  if (['filled', 'completed', 'executed'].includes(normalizedStatus)) {
    return 'success';
  }
  if (
    [
      'error',
      'failed',
      'rejected',
      'canceled',
      'cancelled',
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

const formatStatusLabel = (value?: string): string => {
  if (!value) return 'Unknown';
  return value
    .replace(/_/g, ' ')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .toLowerCase()
    .replace(/\b\w/g, (ch) => ch.toUpperCase())
    .trim();
};

const normalizeSide = (value?: string | null): string | null => {
  if (!value) return null;
  const normalized = value.toUpperCase();
  if (normalized === 'A' || normalized === 'ASK') return 'SELL';
  if (normalized === 'B' || normalized === 'BID') return 'BUY';
  if (normalized === 'BUY' || normalized === 'SELL') return normalized;
  return normalized;
};

const formatHyperliquidOrderType = (orderType?: HyperliquidCreateOrder['order_type']): string => {
  if (!orderType) return 'Market';
  if (orderType.limit) {
    const tif = orderType.limit.tif ? orderType.limit.tif.toUpperCase() : 'GTC';
    return `Limit (${tif})`;
  }
  if (orderType.trigger) {
    const mode = orderType.trigger.tpsl ? orderType.trigger.tpsl.toUpperCase() : 'TP';
    return orderType.trigger.is_market ? `Market ${mode}` : `Trigger (${mode})`;
  }
  return 'Market';
};

const formatLogEntryType = (entryType: OrderLogEntry['type']): string => {
  if (entryType === 'three_commas_event') return '3Commas Event';
  if (entryType === 'hyperliquid_submission') return 'Hyperliquid Submission';
  if (entryType === 'hyperliquid_status') return 'Hyperliquid Status';
  return toTitleCase(entryType);
};

export function OrderDetailDialog({ order, open, onOpenChange }: OrderDetailDialogProps) {
  const [detailedOrder, setDetailedOrder] = useState<OrderRecord>(order);
  const [botPayload, setBotPayload] = useState<UnknownRecord | null>(null);
  const [dealPayload, setDealPayload] = useState<UnknownRecord | null>(null);
  const [loading, setLoading] = useState(false);

  const oidHex = detailedOrder.order_id ?? detailedOrder.identifiers.hex;

  useEffect(() => {
    if (!open) {
      return;
    }

    const identifiers = order.identifiers;
    const order_id = order.order_id ?? identifiers.hex;

    setLoading(true);

    Promise.all([
      fetch(buildOpsApiUrl(`/api/orders?order_id=${order_id}&include_log=true`))
        .then(async (response) => {
          if (!response.ok) throw new Error('API not available');
          const data: ListOrdersResponse = await response.json();
          if (data.items && data.items.length > 0) {
            setDetailedOrder(data.items[0]);
          } else {
            setDetailedOrder(order);
          }
        })
        .catch(() => {
          setDetailedOrder(order);
        }),
      fetch(buildOpsApiUrl(`/api/bots?bot_id=${identifiers.bot_id}`))
        .then(async (response) => {
          if (!response.ok) throw new Error('API not available');
          const data: ListBotsResponse = await response.json();
          if (data.items && data.items.length > 0) {
            setBotPayload(asRecord(data.items[0].payload));
          } else {
            setBotPayload({
              name: `Bot ${identifiers.bot_id}`,
            });
          }
        })
        .catch(() => {
          setBotPayload({
            name: `Bot ${identifiers.bot_id}`,
          });
        }),
      fetch(buildOpsApiUrl(`/api/deals?deal_id=${identifiers.deal_id}`))
        .then(async (response) => {
          if (!response.ok) throw new Error('API not available');
          const data: ListDealsResponse = await response.json();
          if (data.items && data.items.length > 0) {
            setDealPayload(asRecord(data.items[0].payload));
          } else {
            setDealPayload(null);
          }
        })
        .catch(() => {
          setDealPayload(null);
        }),
    ]).finally(() => {
      setLoading(false);
    });
  }, [open, order]);

  const identifiers: OrderIdentifiers = detailedOrder.identifiers;
  const event: ThreeCommasBotEvent = detailedOrder.three_commas.event;
  const hyperliquidState = detailedOrder.hyperliquid;
  const submissionAction = hyperliquidState?.latest_submission;
  const submissionOrder = getSubmissionOrder(submissionAction);
  const cancelAction = getCancelPayload(submissionAction);
  const status: HyperliquidWsOrder | undefined = hyperliquidState?.latest_status;
  const statusOrder = status?.order;

  const primaryStatusRaw = status?.status || event.status || (submissionAction?.kind === 'cancel' ? 'canceled' : '');
  const statusLabel = formatStatusLabel(primaryStatusRaw);
  const normalizedStatus = statusLabel.toLowerCase();
  const statusTone = normalizeStatusTone(normalizedStatus);

  const coin = event.coin || submissionOrder?.coin || statusOrder?.coin || 'N/A';
  const priceValue =
    submissionOrder?.price ?? parseNumeric(statusOrder?.limit_px) ?? parseNumeric(event.price) ?? 0;
  const sizeValue =
    submissionOrder?.size ?? parseNumeric(statusOrder?.orig_size) ?? parseNumeric(event.size) ?? 0;
  const volumeValue = parseNumeric(event.quote_volume) ?? 0;

  const orderTypeLabel = event.order_type ? toTitleCase(event.order_type) : 'N/A';
  const actionLabel = event.action || 'Event';
  const descriptionText = event.text || '';
  const eventCreatedAtDisplay = formatDateTime(event.created_at);

  const submissionExists = Boolean(submissionAction && submissionAction.kind !== 'none');
  const submissionCoin = submissionOrder?.coin || cancelAction?.coin || coin;
  const submissionPriceValue =
    submissionOrder?.price ?? parseNumeric(statusOrder?.limit_px) ?? undefined;
  const submissionSizeValue =
    submissionOrder?.size ?? parseNumeric(statusOrder?.orig_size) ?? undefined;

  let submissionSideLabel = '—';
  let submissionIsBuy: boolean | null = null;
  if (submissionOrder) {
    submissionIsBuy = submissionOrder.is_buy;
    submissionSideLabel = submissionOrder.is_buy ? 'BUY' : 'SELL';
  } else if (statusOrder) {
    const normalized = normalizeSide(statusOrder.side);
    if (normalized) {
      submissionSideLabel = normalized;
      submissionIsBuy = normalized === 'BUY';
    }
  } else if (event.type) {
    const normalized = normalizeSide(event.type);
    if (normalized) {
      submissionSideLabel = normalized;
      submissionIsBuy = normalized === 'BUY';
    }
  }

  const submissionOrderTypeLabel = submissionOrder
    ? formatHyperliquidOrderType(submissionOrder.order_type)
    : submissionAction?.kind === 'cancel'
      ? 'Cancel'
      : '—';
  const submissionReduceOnly = submissionOrder?.reduce_only ?? false;
  const submissionClientOrderId =
    submissionOrder?.cloid ||
    cancelAction?.cloid ||
    statusOrder?.cloid ||
    '—';

  const statusTimestampDisplay = formatDateTime(status?.status_timestamp ?? statusOrder?.timestamp);
  const createdAtDisplay = formatDateTime(identifiers.created_at);
  const observedAtDisplay = formatDateTime(detailedOrder.observed_at);

  const statusOrderId = statusOrder?.oid ? statusOrder.oid.toString() : '—';
  const statusOrderSide = normalizeSide(statusOrder?.side);
  const statusOrderIsBuy = statusOrderSide === 'BUY';
  const statusOrderLimit = parseNumeric(statusOrder?.limit_px) ?? 0;
  const statusOrderOrigSize = parseNumeric(statusOrder?.orig_size) ?? 0;
  const statusOrderFilledSize = parseNumeric(statusOrder?.size) ?? 0;
  const statusOrderCoin = statusOrder?.coin ?? 'N/A';

  const statusOrderClasses = statusOrderIsBuy
    ? 'bg-green-100 text-green-800 border-green-200'
    : 'bg-red-100 text-red-800 border-red-200';

  const statusCardClass = statusToneClasses[statusTone];
  const statusBadgeClass = statusBadgeClasses[statusTone];

  const botName = coerceString(botPayload?.name, `Bot ${identifiers.bot_id}`);
  const botAccount = coerceString(botPayload?.account_name, 'N/A');
  const botStrategy = coerceString(botPayload?.strategy, 'N/A');
  const botEnabledValue = botPayload?.is_enabled;
  const botEnabled = typeof botEnabledValue === 'boolean' ? botEnabledValue : undefined;

  const dealPair = coerceString(dealPayload?.pair, 'N/A');
  const dealStatus = coerceString(dealPayload?.status, '');
  const dealProfitValue = (() => {
    const primary = coerceNumber(dealPayload?.actual_profit, Number.NaN);
    if (!Number.isNaN(primary)) return primary;
    const fallback = coerceNumber(dealPayload?.final_profit, Number.NaN);
    return fallback;
  })();
  const hasDealProfit = Number.isFinite(dealProfitValue);
  const dealProfitCurrency = coerceString(dealPayload?.profit_currency, 'USDT');

  const logEntries: OrderLogEntry[] = detailedOrder.log_entries ?? [];
  const submissionBadgeTone: StatusTone = submissionAction?.kind === 'cancel'
    ? 'danger'
    : submissionExists
      ? 'info'
      : 'neutral';

  const submissionBadgeClass = statusBadgeClasses[submissionBadgeTone];

  const isErrorStatus = ['danger'].includes(statusTone);

  const submissionSizeDisplay =
    submissionSizeValue !== undefined ? formatFixed(submissionSizeValue, 4, 4) : '—';
  const submissionPriceDisplay =
    submissionPriceValue !== undefined ? formatCurrency(submissionPriceValue, 5, 5) : '—';

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="!max-w-6xl max-h-[85vh]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            <span>Order Details</span>
            {normalizedStatus === 'filled' && (
              <CheckCircle2 className="h-5 w-5 text-green-600" />
            )}
            {isErrorStatus && (
              <XCircle className="h-5 w-5 text-red-600" />
            )}
          </DialogTitle>
          <DialogDescription>
            In-depth breakdown of the selected order across 3Commas and Hyperliquid.
          </DialogDescription>
          <div className="flex items-center gap-2 mt-2 flex-wrap">
            <code className="text-xs bg-gray-100 px-2 py-1 rounded font-mono">
              {oidHex}
            </code>
            <Button
              variant="ghost"
              size="sm"
              onClick={(event: MouseEvent<HTMLButtonElement>) => {
                event.stopPropagation();
                window.open(`https://app.3commas.io/bots/${identifiers.bot_id}`, '_blank');
              }}
              className="h-7 text-xs"
            >
              <ExternalLink className="h-3 w-3 mr-1" />
              3Commas Bot {identifiers.bot_id}
            </Button>

            <Button
              variant="ghost"
              size="sm"
              onClick={(event: MouseEvent<HTMLButtonElement>) => {
                event.stopPropagation();
                window.open(`https://app.3commas.io/deals/${identifiers.deal_id}`, '_blank');
              }}
              className="h-7 text-xs"
            >
              <ExternalLink className="h-3 w-3 mr-1" />
              3Commas Deal {identifiers.deal_id}
            </Button>
          </div>
        </DialogHeader>

        <Tabs defaultValue="overview" className="w-full">
          <TabsList className="w-full">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="flow">Trade Flow</TabsTrigger>
            <TabsTrigger value="eventlog">Event Log</TabsTrigger>
            <TabsTrigger value="raw">Raw Data</TabsTrigger>
          </TabsList>

          <ScrollArea className="h-[55vh] mt-4">
            <TabsContent value="overview" className="space-y-3">
              {loading && (
                <p className="text-xs text-gray-500 px-1">
                  Refreshing latest order details…
                </p>
              )}
              <Card className="p-3 bg-gradient-to-r from-blue-50 to-purple-50 border-blue-200">
                <div className="flex items-start justify-between mb-2">
                  <div>
                    <h3 className="text-gray-900 mb-0.5">Trade Summary</h3>
                    <p className="text-xs text-gray-600">
                      {descriptionText || 'No description available'}
                    </p>
                  </div>
                  {submissionIsBuy !== null ? (
                    <Badge
                      className={
                        submissionIsBuy
                          ? 'bg-green-100 text-green-800 border-green-200'
                          : 'bg-red-100 text-red-800 border-red-200'
                      }
                    >
                      {submissionSideLabel}
                    </Badge>
                  ) : (
                    <Badge variant="outline" className="text-xs">
                      {submissionSideLabel}
                    </Badge>
                  )}
                </div>

                <div className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2">
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Coin</div>
                    <div className="text-gray-900">{coin}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Price</div>
                    <div className="text-gray-900">
                      {priceValue ? formatCurrency(priceValue, 5, 5) : '—'}
                    </div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Size</div>
                    <div className="text-gray-900">
                      {sizeValue ? formatFixed(sizeValue, 4, 4) : '—'}
                    </div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Volume (USDT)</div>
                    <div className="text-gray-900">
                      {volumeValue ? formatCurrency(volumeValue, 2, 2) : '—'}
                    </div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Order Type</div>
                    <div className="text-xs text-gray-900">{orderTypeLabel}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Action</div>
                    <div className="text-xs text-gray-900">{actionLabel}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Status</div>
                    <Badge className={statusBadgeClass}>{statusLabel}</Badge>
                  </div>
                </div>
              </Card>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                {botPayload && (
                  <Card className="p-3">
                    <div className="flex items-center gap-2 mb-3">
                      <Bot className="h-4 w-4 text-blue-600" />
                      <h3 className="text-gray-900">Bot Context</h3>
                    </div>
                    <div className="space-y-2">
                      <div className="flex justify-between text-xs">
                        <span className="text-gray-600">Name:</span>
                        <span className="text-gray-900 truncate ml-2">{botName}</span>
                      </div>
                      <div className="flex justify-between text-xs">
                        <span className="text-gray-600">Account:</span>
                        <span className="text-gray-900 truncate ml-2">{botAccount}</span>
                      </div>
                      <div className="flex justify-between text-xs">
                        <span className="text-gray-600">Strategy:</span>
                        <span className="text-gray-900">{botStrategy}</span>
                      </div>
                      {botEnabled !== undefined && (
                        <div className="flex justify-between text-xs">
                          <span className="text-gray-600">Status:</span>
                          {botEnabled ? (
                            <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">
                              Active
                            </Badge>
                          ) : (
                            <Badge variant="outline" className="text-xs">
                              Inactive
                            </Badge>
                          )}
                        </div>
                      )}
                    </div>
                  </Card>
                )}

                {dealPayload && (
                  <Card className="p-3">
                    <div className="flex items-center gap-2 mb-3">
                      <Activity className="h-4 w-4 text-purple-600" />
                      <h3 className="text-gray-900">Deal Context</h3>
                    </div>
                    <div className="space-y-2">
                      <div className="flex justify-between text-xs">
                        <span className="text-gray-600">Pair:</span>
                        <span className="text-gray-900">{dealPair}</span>
                      </div>
                      {dealStatus && (
                        <div className="flex justify-between text-xs">
                          <span className="text-gray-600">Status:</span>
                          <span className="text-gray-900">{dealStatus}</span>
                        </div>
                      )}
                      {hasDealProfit && (
                        <div className="flex justify-between text-xs">
                          <span className="text-gray-600">Profit:</span>
                          <span className={dealProfitValue >= 0 ? 'text-green-600' : 'text-red-600'}>
                            {formatFixed(dealProfitValue, 2, 2)} {dealProfitCurrency}
                          </span>
                        </div>
                      )}
                    </div>
                  </Card>
                )}
              </div>

              <Card className="p-3">
                <h3 className="text-gray-900 mb-3">Timeline</h3>
                <div className="space-y-2">
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Created:</span>
                    <span className="text-gray-900">{createdAtDisplay}</span>
                  </div>
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Observed:</span>
                    <span className="text-gray-900">{observedAtDisplay}</span>
                  </div>
                  {statusTimestampDisplay !== '—' && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Status Update:</span>
                      <span className="text-gray-900">{statusTimestampDisplay}</span>
                    </div>
                  )}
                </div>
              </Card>
            </TabsContent>

            <TabsContent value="flow" className="space-y-3">
              <div className="relative">
                <Card className="p-3 border-blue-200 bg-gradient-to-r from-blue-50 to-blue-100">
                  <div className="flex items-start justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <Activity className="h-5 w-5 text-blue-600" />
                      <h3 className="text-gray-900">1. 3Commas Event</h3>
                    </div>
                    <Badge className="bg-blue-100 text-blue-800 border-blue-200">{event.action}</Badge>
                  </div>
                  <div className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2">
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Coin</div>
                      <div className="text-gray-900">{event.coin}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Price</div>
                      <div className="text-gray-900">{formatCurrency(parseNumeric(event.price) ?? 0, 5, 5)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Size</div>
                      <div className="text-gray-900">{formatFixed(parseNumeric(event.size) ?? 0, 4, 4)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Order Type</div>
                      <div className="text-xs text-gray-900">{orderTypeLabel}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Status</div>
                      <div className="text-xs text-gray-900">{formatStatusLabel(event.status)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Quote Volume</div>
                      <div className="text-xs text-gray-900">{formatFixed(parseNumeric(event.quote_volume) ?? 0, 2, 2)}</div>
                    </div>
                    <div className="md:col-span-2">
                      <div className="text-xs text-gray-600 mb-0.5">Created At</div>
                      <div className="text-xs text-gray-900">{eventCreatedAtDisplay}</div>
                    </div>
                  </div>
                </Card>

                <div className="flex justify-center my-3">
                  <div className="bg-purple-100 p-2 rounded-full">
                    <ArrowRight className="h-5 w-5 text-purple-600 rotate-90" />
                  </div>
                </div>

                <Card className={`p-3 border-purple-200 bg-gradient-to-r from-purple-50 to-purple-100`}>
                  <div className="flex items-start justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <ArrowRight className="h-5 w-5 text-purple-600" />
                      <h3 className="text-gray-900">2. Hyperliquid Submission</h3>
                    </div>
                    <Badge className={submissionBadgeClass}>
                      {submissionAction?.kind === 'cancel' ? 'Cancelled' : submissionExists ? 'Submitted' : 'Not Submitted'}
                    </Badge>
                  </div>

                  {submissionExists ? (
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2">
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Coin</div>
                        <div className="text-gray-900">{submissionCoin}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Price</div>
                        <div className="text-gray-900">{submissionPriceDisplay}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Size</div>
                        <div className="text-gray-900">{submissionSizeDisplay}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Side</div>
                        <div>
                          {submissionIsBuy !== null ? (
                            <Badge
                              className={
                                submissionIsBuy
                                  ? 'bg-green-100 text-green-800 border-green-200 text-xs'
                                  : 'bg-red-100 text-red-800 border-red-200 text-xs'
                              }
                            >
                              {submissionSideLabel}
                            </Badge>
                          ) : (
                            <Badge variant="outline" className="text-xs">
                              {submissionSideLabel}
                            </Badge>
                          )}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Order Type</div>
                        <div className="text-xs text-gray-900">{submissionOrderTypeLabel}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Reduce Only</div>
                        <div className="text-xs text-gray-900">
                          {submissionReduceOnly ? 'Yes' : 'No'}
                        </div>
                      </div>
                      <div className="md:col-span-2">
                        <div className="text-xs text-gray-600 mb-0.5">Client Order ID</div>
                        <div className="text-xs text-gray-900">{submissionClientOrderId}</div>
                      </div>
                    </div>
                  ) : (
                    <p className="text-xs text-gray-500">No submission data available</p>
                  )}
                </Card>

                <div className="flex justify-center my-3">
                  <div className="bg-green-100 p-2 rounded-full">
                    <ArrowRight className="h-5 w-5 text-green-600 rotate-90" />
                  </div>
                </div>

                <Card className={`p-3 ${statusCardClass}`}>
                  <div className="flex items-start justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <Clock className="h-5 w-5 text-green-600" />
                      <h3 className="text-gray-900">3. Hyperliquid Status</h3>
                    </div>
                    <Badge className={statusOrderClasses}>{statusLabel}</Badge>
                  </div>

                  {status ? (
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2">
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Status</div>
                        <div className="text-gray-900">{statusLabel}</div>
                      </div>
                      {statusTimestampDisplay !== '—' && (
                        <div className="col-span-3">
                          <div className="text-xs text-gray-600 mb-0.5">Status Timestamp</div>
                          <div className="text-gray-900">{statusTimestampDisplay}</div>
                        </div>
                      )}
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Order ID</div>
                        <div className="text-gray-900">{statusOrderId}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Side</div>
                        <div>
                          {statusOrderSide ? (
                            <Badge className={statusOrderClasses}>{statusOrderSide}</Badge>
                          ) : (
                            <Badge variant="outline" className="text-xs">
                              —
                            </Badge>
                          )}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Limit Price</div>
                        <div className="text-gray-900">
                          {statusOrderLimit ? formatCurrency(statusOrderLimit, 5, 5) : '—'}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Original Size</div>
                        <div className="text-gray-900">
                          {statusOrderOrigSize ? formatFixed(statusOrderOrigSize, 4, 4) : '—'}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Filled Size</div>
                        <div className="text-gray-900">
                          {statusOrderFilledSize ? formatFixed(statusOrderFilledSize, 4, 4) : '—'}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Coin</div>
                        <div className="text-gray-900">{statusOrderCoin}</div>
                      </div>
                    </div>
                  ) : (
                    <p className="text-xs text-gray-500">No status data available</p>
                  )}
                </Card>
              </div>
            </TabsContent>

            <TabsContent value="eventlog" className="space-y-3">
              {logEntries.length > 0 ? (
                <Card className="p-3">
                  <div className="flex items-center gap-2 mb-3">
                    <Activity className="h-4 w-4 text-purple-600" />
                    <div>
                      <h3 className="text-gray-900">Event Timeline</h3>
                      <p className="text-xs text-gray-500">
                        Chronological log of all events for this order
                      </p>
                    </div>
                  </div>
                  <div className="space-y-2">
                    {logEntries.map((entry) => (
                      <div key={`${entry.type}-${entry.sequence ?? entry.observed_at}`} className="border rounded p-2 bg-gray-50">
                        <div className="flex items-center justify-between text-xs mb-1">
                          <Badge variant="secondary" className="text-[10px]">
                            {formatLogEntryType(entry.type)}
                          </Badge>
                          <span className="text-gray-500">{formatDateTime(entry.observed_at)}</span>
                        </div>
                        <pre className="text-[11px] text-gray-700 whitespace-pre-wrap">
                          {JSON.stringify(entry, null, 2)}
                        </pre>
                      </div>
                    ))}
                  </div>
                </Card>
              ) : (
                <Card className="p-3">
                  <p className="text-xs text-gray-500 text-center">No event log entries available</p>
                </Card>
              )}
            </TabsContent>

            <TabsContent value="raw" className="space-y-3">
              <Card className="p-3">
                <div className="flex items-center gap-2 mb-3">
                  <Code className="h-4 w-4 text-blue-600" />
                  <h3 className="text-gray-900">3Commas Event</h3>
                </div>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {JSON.stringify(event, null, 2)}
                  </pre>
                </ScrollArea>
              </Card>

              <Card className="p-3">
                <div className="flex items-center gap-2 mb-3">
                  <Code className="h-4 w-4 text-purple-600" />
                  <h3 className="text-gray-900">Hyperliquid Submission</h3>
                </div>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {JSON.stringify(submissionAction ?? null, null, 2)}
                  </pre>
                </ScrollArea>
              </Card>

              <Card className="p-3">
                <div className="flex items-center gap-2 mb-3">
                  <Clock className="h-4 w-4 text-green-600" />
                  <h3 className="text-gray-900">Hyperliquid Status</h3>
                </div>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {JSON.stringify(status ?? null, null, 2)}
                  </pre>
                </ScrollArea>
              </Card>

              {logEntries.length > 0 && (
                <Card className="p-3">
                  <div className="flex items-center gap-2 mb-3">
                    <Code className="h-4 w-4 text-gray-600" />
                    <h3 className="text-gray-900">Stream Log Entries</h3>
                  </div>
                  <ScrollArea className="w-full">
                    <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                      {JSON.stringify(logEntries, null, 2)}
                    </pre>
                  </ScrollArea>
                </Card>
              )}
            </TabsContent>
          </ScrollArea>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
