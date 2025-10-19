import { useEffect, useState, type MouseEvent } from 'react';
import type {
  ListBotsResponse,
  ListDealsResponse,
  ListOrdersResponse,
  OrderRecord,
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

const getValue = (
  record: UnknownRecord | null | undefined,
  ...keys: string[]
): unknown => {
  if (!record) return undefined;
  for (const key of keys) {
    if (key in record) {
      return record[key];
    }
  }
  return undefined;
};

const pickString = (fallback: string, ...values: unknown[]): string => {
  for (const value of values) {
    const str = coerceString(value, '').trim();
    if (str.length > 0) {
      return str;
    }
  }
  return fallback;
};

const pickNumber = (fallback: number, ...values: unknown[]): number => {
  for (const value of values) {
    const numeric = coerceNumber(value, Number.NaN);
    if (!Number.isNaN(numeric)) {
      return numeric;
    }
  }
  return fallback;
};

const formatDateTime = (value: unknown): string => {
  const candidate = coerceString(value, '');
  if (candidate.length === 0) return '—';
  const parsed = new Date(candidate);
  if (Number.isNaN(parsed.getTime())) return '—';
  return parsed.toLocaleString();
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

export function OrderDetailDialog({ order, open, onOpenChange }: OrderDetailDialogProps) {
  const [detailedOrder, setDetailedOrder] = useState<OrderRecord>(order);
  const [botPayload, setBotPayload] = useState<UnknownRecord | null>(null);
  const [dealPayload, setDealPayload] = useState<UnknownRecord | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open) {
      return;
    }

    setLoading(true);

    Promise.all([
      fetch(buildOpsApiUrl(`/api/orders?metadata=${order.metadata_hex}&include_log=true`))
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
      fetch(buildOpsApiUrl(`/api/bots?bot_id=${order.bot_id}`))
        .then(async (response) => {
          if (!response.ok) throw new Error('API not available');
          const data: ListBotsResponse = await response.json();
          if (data.items && data.items.length > 0) {
            setBotPayload(asRecord(data.items[0].payload));
          } else {
            setBotPayload({
              name: `Bot ${order.bot_id}`,
            });
          }
        })
        .catch(() => {
          setBotPayload({
            name: `Bot ${order.bot_id}`,
          });
        }),
      fetch(buildOpsApiUrl(`/api/deals?deal_id=${order.deal_id}`))
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

  const eventPayload = asRecord(detailedOrder.bot_event_payload);
  const submissionPayload = detailedOrder.latest_submission
    ? asRecord(detailedOrder.latest_submission)
    : null;
  const statusPayload = detailedOrder.latest_status
    ? asRecord(detailedOrder.latest_status)
    : null;
  const statusOrderPayload = statusPayload?.order
    ? asRecord(statusPayload.order)
    : null;

  const statusText = pickString(
    '',
    getValue(statusPayload, 'status'),
    getValue(eventPayload, 'Status'),
    getValue(eventPayload, 'status'),
  );
  const normalizedStatus = statusText.toLowerCase();
  const statusLabel =
    statusText.length > 0
      ? statusText.replace(/_/g, ' ').replace(/\b\w/g, (ch) => ch.toUpperCase())
      : 'Unknown';

  const coin = pickString(
    'N/A',
    getValue(eventPayload, 'Coin'),
    getValue(eventPayload, 'coin'),
    getValue(submissionPayload, 'Coin'),
    getValue(submissionPayload, 'coin'),
  );
  const priceValue = pickNumber(
    0,
    getValue(eventPayload, 'Price'),
    getValue(eventPayload, 'price'),
    getValue(submissionPayload, 'Price'),
    getValue(statusOrderPayload, 'limitPx'),
    getValue(statusOrderPayload, 'limit_px'),
  );
  const sizeValue = pickNumber(
    0,
    getValue(eventPayload, 'Size'),
    getValue(eventPayload, 'size'),
    getValue(submissionPayload, 'Size'),
    getValue(statusOrderPayload, 'origSz'),
    getValue(statusOrderPayload, 'orig_sz'),
  );
  const volumeValue = pickNumber(
    0,
    getValue(eventPayload, 'QuoteVolume'),
    getValue(eventPayload, 'quoteVolume'),
    getValue(submissionPayload, 'QuoteVolume'),
  );

  const orderTypeLabel = pickString(
    'N/A',
    getValue(eventPayload, 'OrderType'),
    getValue(eventPayload, 'orderType'),
    getValue(submissionPayload, 'OrderType'),
    getValue(submissionPayload, 'orderType'),
  );
  const actionLabel = pickString(
    'Event',
    getValue(eventPayload, 'Action'),
    getValue(eventPayload, 'action'),
  );
  const descriptionText = pickString(
    '',
    getValue(eventPayload, 'Text'),
    getValue(eventPayload, 'text'),
  );
  const eventType = pickString(
    'N/A',
    getValue(eventPayload, 'Type'),
    getValue(eventPayload, 'type'),
  );
  const eventCreatedAtDisplay = formatDateTime(
    pickString('', getValue(eventPayload, 'CreatedAt'), getValue(eventPayload, 'created_at')),
  );

  const submissionExists = Boolean(submissionPayload);
  const submissionCoin = pickString(
    'N/A',
    getValue(submissionPayload, 'Coin'),
    getValue(submissionPayload, 'coin'),
  );
  const submissionPrice = pickNumber(
    0,
    getValue(submissionPayload, 'Price'),
    getValue(submissionPayload, 'price'),
  );
  const submissionSize = pickNumber(
    0,
    getValue(submissionPayload, 'Size'),
    getValue(submissionPayload, 'size'),
  );
  const submissionSideValue = pickString(
    '',
    getValue(submissionPayload, 'Side'),
    getValue(submissionPayload, 'side'),
  ).toUpperCase();
  const submissionIsBuyValue = getValue(submissionPayload, 'IsBuy');
  const submissionIsBuy =
    typeof submissionIsBuyValue === 'boolean'
      ? submissionIsBuyValue
      : submissionSideValue === 'BUY';
  const submissionSideLabel = submissionIsBuy ? 'BUY' : 'SELL';
  const submissionOrderTypeRaw = getValue(
    submissionPayload,
    'OrderType',
    'orderType',
  ) as UnknownRecord | string | undefined;
  const submissionOrderIsLimit =
    typeof submissionOrderTypeRaw === 'string'
      ? submissionOrderTypeRaw.toLowerCase().includes('limit')
      : !!submissionOrderTypeRaw && 'limit' in submissionOrderTypeRaw;
  const submissionOrderTypeLabel = submissionExists
    ? submissionOrderIsLimit
      ? 'Limit (GTC)'
      : 'Market'
    : '—';
  const submissionClientOrderId = pickString(
    '—',
    getValue(submissionPayload, 'ClientOrderID'),
    getValue(submissionPayload, 'client_order_id'),
    getValue(submissionPayload, 'clientOrderId'),
  );
  const submissionReduceOnly = Boolean(
    getValue(submissionPayload, 'ReduceOnly'),
  );

  const statusTimestampDisplay = formatDateTime(
    pickString(
      '',
      getValue(statusPayload, 'statusTimestamp'),
      getValue(statusPayload, 'status_timestamp'),
    ),
  );
  const createdAtDisplay = formatDateTime(detailedOrder.created_at);
  const observedAtDisplay = formatDateTime(detailedOrder.observed_at);

  const statusOrderId = pickString(
    '—',
    getValue(statusOrderPayload, 'oid'),
    getValue(statusOrderPayload, 'id'),
  );
  const statusOrderSideValue = pickString('', getValue(statusOrderPayload, 'side')).toUpperCase();
  const statusOrderIsBuy =
    statusOrderSideValue === 'B' || statusOrderSideValue === 'BUY';
  const statusOrderLimit = pickNumber(
    0,
    getValue(statusOrderPayload, 'limitPx'),
    getValue(statusOrderPayload, 'limit_px'),
  );
  const statusOrderOrigSize = pickNumber(
    0,
    getValue(statusOrderPayload, 'origSz'),
    getValue(statusOrderPayload, 'orig_sz'),
  );
  const statusOrderFilledSize = pickNumber(
    0,
    getValue(statusOrderPayload, 'sz'),
    getValue(statusOrderPayload, 'filledSz'),
    getValue(statusOrderPayload, 'filled_sz'),
  );
  const statusOrderCoin = pickString('N/A', getValue(statusOrderPayload, 'coin'));

  const statusCardClass =
    normalizedStatus === 'filled'
      ? 'border-green-200 bg-gradient-to-r from-green-50 to-green-100'
      : normalizedStatus === 'error'
        ? 'border-red-200 bg-gradient-to-r from-red-50 to-red-100'
        : normalizedStatus === 'pending'
          ? 'border-yellow-200 bg-gradient-to-r from-yellow-50 to-yellow-100'
          : 'border-gray-200 bg-gradient-to-r from-gray-50 to-gray-100';

  const botName = pickString(`Bot ${detailedOrder.bot_id}`, getValue(botPayload, 'name'));
  const botAccount = pickString('N/A', getValue(botPayload, 'account_name'));
  const botStrategy = pickString('N/A', getValue(botPayload, 'strategy'));
  const botEnabledValue = getValue(botPayload, 'is_enabled');
  const botEnabled =
    typeof botEnabledValue === 'boolean' ? botEnabledValue : undefined;

  const dealPair = pickString('N/A', getValue(dealPayload, 'pair'));
  const dealStatus = pickString('', getValue(dealPayload, 'status'));
  const dealProfitValue = pickNumber(
    Number.NaN,
    getValue(dealPayload, 'actual_profit'),
    getValue(dealPayload, 'final_profit'),
  );
  const hasDealProfit = Number.isFinite(dealProfitValue);
  const dealProfitCurrency = pickString('USDT', getValue(dealPayload, 'profit_currency'));

  const statusOrderClasses = statusOrderIsBuy
    ? 'bg-green-100 text-green-800 border-green-200'
    : 'bg-red-100 text-red-800 border-red-200';

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="!max-w-6xl max-h-[85vh]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            <span>Order Details</span>
            {normalizedStatus === 'filled' && (
              <CheckCircle2 className="h-5 w-5 text-green-600" />
            )}
            {normalizedStatus === 'error' && (
              <XCircle className="h-5 w-5 text-red-600" />
            )}
          </DialogTitle>
          <DialogDescription>
            In-depth breakdown of the selected order across 3Commas and Hyperliquid.
          </DialogDescription>
          <div className="flex items-center gap-2 mt-2 flex-wrap">
            <code className="text-xs bg-gray-100 px-2 py-1 rounded font-mono">
              {detailedOrder.metadata_hex}
            </code>
            <Button
              variant="ghost"
              size="sm"
              onClick={(event: MouseEvent<HTMLButtonElement>) => {
                event.stopPropagation();
                window.open(`https://app.3commas.io/bots/${detailedOrder.bot_id}`, '_blank');
              }}
              className="h-7 text-xs"
            >
              <ExternalLink className="h-3 w-3 mr-1" />
              3Commas Bot {detailedOrder.bot_id}
            </Button>

            <Button
              variant="ghost"
              size="sm"
              onClick={(event: MouseEvent<HTMLButtonElement>) => {
                event.stopPropagation();
                window.open(`https://app.3commas.io/deals/${detailedOrder.deal_id}`, '_blank');
              }}
              className="h-7 text-xs"
            >
              <ExternalLink className="h-3 w-3 mr-1" />
              3Commas Deal {detailedOrder.deal_id}
            </Button>
          </div>
        </DialogHeader>

        <Tabs defaultValue="overview" className="w-full">
          <TabsList className="grid w-full grid-cols-3">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="flow">Trade Flow</TabsTrigger>
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
                  <Badge
                    className={
                      submissionIsBuy
                        ? 'bg-green-100 text-green-800 border-green-200'
                        : 'bg-red-100 text-red-800 border-red-200'
                    }
                  >
                    {submissionSideLabel}
                  </Badge>
                </div>

                <div className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2">
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Coin</div>
                    <div className="text-gray-900">{coin}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Price</div>
                    <div className="text-gray-900">{formatCurrency(priceValue, 5, 5)}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Size</div>
                    <div className="text-gray-900">{formatFixed(sizeValue, 4, 4)}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-0.5">Volume (USDT)</div>
                    <div className="text-gray-900">{formatCurrency(volumeValue, 2, 2)}</div>
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
                    {normalizedStatus === 'filled' && (
                      <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">
                        Filled
                      </Badge>
                    )}
                    {normalizedStatus === 'error' && (
                      <Badge className="bg-red-100 text-red-800 border-red-200 text-xs">
                        Error
                      </Badge>
                    )}
                    {normalizedStatus === 'pending' && (
                      <Badge className="bg-yellow-100 text-yellow-800 border-yellow-200 text-xs">
                        Pending
                      </Badge>
                    )}
                    {!statusText && (
                      <Badge variant="outline" className="text-xs">
                        Unknown
                      </Badge>
                    )}
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
                    <div>
                      <div className="flex items-center gap-2 mb-1">
                        <Activity className="h-5 w-5 text-blue-600" />
                        <h3 className="text-gray-900">1. 3Commas Bot Event</h3>
                      </div>
                      {descriptionText && (
                        <p className="text-xs text-gray-600 mt-1">{descriptionText}</p>
                      )}
                    </div>
                    <Badge className="bg-blue-100 text-blue-800 border-blue-200">
                      {actionLabel}
                    </Badge>
                  </div>

                  <div className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2">
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Coin</div>
                      <div className="text-gray-900">{coin}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Price</div>
                      <div className="text-gray-900">{formatCurrency(priceValue, 5, 5)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Size</div>
                      <div className="text-gray-900">{formatFixed(sizeValue, 4, 4)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Volume (USDT)</div>
                      <div className="text-gray-900">{formatCurrency(volumeValue, 2, 2)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-600 mb-0.5">Type</div>
                      <div className="text-xs text-gray-900">{eventType}</div>
                    </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Order Type</div>
                        <div className="text-xs text-gray-900">{orderTypeLabel}</div>
                      </div>
                    <div>
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

                <Card className="p-3 border-purple-200 bg-gradient-to-r from-purple-50 to-purple-100">
                  <div className="flex items-start justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <ArrowRight className="h-5 w-5 text-purple-600" />
                      <h3 className="text-gray-900">2. Hyperliquid Submission</h3>
                    </div>
                    <Badge className="bg-purple-100 text-purple-800 border-purple-200">
                      Submitted
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
                        <div className="text-gray-900">{formatCurrency(submissionPrice, 5, 5)}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Size</div>
                        <div className="text-gray-900">{formatFixed(submissionSize, 4, 4)}</div>
                      </div>
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Side</div>
                        <div>
                          <Badge
                            className={
                              submissionIsBuy
                                ? 'bg-green-100 text-green-800 border-green-200 text-xs'
                                : 'bg-red-100 text-red-800 border-red-200 text-xs'
                            }
                          >
                            {submissionSideLabel}
                          </Badge>
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

                  {statusPayload ? (
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2">
                      <div>
                        <div className="text-xs text-gray-600 mb-0.5">Status</div>
                        <div className="text-gray-900 capitalize">
                          {statusLabel.toLowerCase()}
                        </div>
                      </div>
                      {statusTimestampDisplay !== '—' && (
                        <div className="col-span-3">
                          <div className="text-xs text-gray-600 mb-0.5">Status Timestamp</div>
                          <div className="text-gray-900">{statusTimestampDisplay}</div>
                        </div>
                      )}
                      {statusOrderPayload && (
                        <>
                          <div>
                            <div className="text-xs text-gray-600 mb-0.5">Order ID</div>
                            <div className="text-gray-900">{statusOrderId}</div>
                          </div>
                          <div>
                            <div className="text-xs text-gray-600 mb-0.5">Side</div>
                            <div>
                              <Badge className={statusOrderClasses}>
                                {statusOrderIsBuy ? 'BUY' : 'SELL'}
                              </Badge>
                            </div>
                          </div>
                          <div>
                            <div className="text-xs text-gray-600 mb-0.5">Limit Price</div>
                            <div className="text-gray-900">
                              {formatCurrency(statusOrderLimit, 5, 5)}
                            </div>
                          </div>
                          <div>
                            <div className="text-xs text-gray-600 mb-0.5">Original Size</div>
                            <div className="text-gray-900">
                              {formatFixed(statusOrderOrigSize, 4, 4)}
                            </div>
                          </div>
                          <div>
                            <div className="text-xs text-gray-600 mb-0.5">Filled Size</div>
                            <div className="text-gray-900">
                              {formatFixed(statusOrderFilledSize, 4, 4)}
                            </div>
                          </div>
                          <div>
                            <div className="text-xs text-gray-600 mb-0.5">Coin</div>
                            <div className="text-gray-900">{statusOrderCoin}</div>
                          </div>
                        </>
                      )}
                    </div>
                  ) : (
                    <p className="text-xs text-gray-500">No status data available</p>
                  )}
                </Card>
              </div>
            </TabsContent>

            <TabsContent value="raw" className="space-y-3">
              <Card className="p-3">
                <div className="flex items-center gap-2 mb-3">
                  <Code className="h-4 w-4 text-blue-600" />
                  <h3 className="text-gray-900">3Commas Bot Event</h3>
                </div>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {JSON.stringify(detailedOrder.bot_event_payload, null, 2)}
                  </pre>
                </ScrollArea>
              </Card>

              {submissionExists && (
                <Card className="p-3">
                  <div className="flex items-center gap-2 mb-3">
                    <ArrowRight className="h-4 w-4 text-purple-600" />
                    <h3 className="text-gray-900">Hyperliquid Submission</h3>
                  </div>
                  <ScrollArea className="w-full">
                    <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                      {JSON.stringify(submissionPayload, null, 2)}
                    </pre>
                  </ScrollArea>
                </Card>
              )}

              {statusPayload && (
                <Card className="p-3">
                  <div className="flex items-center gap-2 mb-3">
                    <Clock className="h-4 w-4 text-green-600" />
                    <h3 className="text-gray-900">Hyperliquid Status</h3>
                  </div>
                  <ScrollArea className="w-full">
                    <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                      {JSON.stringify(statusPayload, null, 2)}
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
