import { Fragment, useEffect, useMemo, useState } from 'react';
import type { ListOrdersResponse, OrderFilterState, OrderRecord } from '../types/api';
import { Card } from './ui/card';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';
import { OrderDetailDialog } from './OrderDetailDialog';
import { toast } from 'sonner';
import { RefreshCw, Eye, ChevronDown, ChevronUp } from 'lucide-react';
import { buildOpsApiUrl } from '../config/opsApi';

interface OrdersTableProps {
  filters: OrderFilterState;
}

interface OrderGroup {
  key: string;
  latest: OrderRecord;
  history: OrderRecord[];
}

export function OrdersTable({ filters }: OrdersTableProps) {
  const [orders, setOrders] = useState<OrderRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedOrder, setSelectedOrder] = useState<OrderRecord | null>(null);
  const [detailDialogOpen, setDetailDialogOpen] = useState(false);
  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({});

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

  const pickNumericValue = (...values: unknown[]): number | undefined => {
    for (const value of values) {
      const numeric = coerceToNumber(value);
      if (numeric !== undefined) {
        return numeric;
      }
    }

    return undefined;
  };

  const formatStatusLabel = (value?: string) => {
    if (!value) return 'Pending';

    return value
      .toString()
      .replace(/_/g, ' ')
      .toLowerCase()
      .replace(/\b\w/g, (char) => char.toUpperCase());
  };

  const determineStatus = (order: OrderRecord): string | undefined => {
    const rawStatus =
      order.latest_status?.status ??
      order.bot_event_payload?.Status ??
      order.bot_event_payload?.status;

    if (typeof rawStatus === 'string' && rawStatus.trim().length > 0) {
      return rawStatus;
    }

    const action = order.bot_event_payload?.Action;
    if (typeof action === 'string' && action.toLowerCase().includes('cancel')) {
      return 'cancelled';
    }

    return undefined;
  };

  const buildOrderIdentity = (order: OrderRecord): string => {
    return [
      order.metadata_hex ?? 'unknown-metadata',
      order.bot_event_id ?? 'unknown-event',
      order.bot_id ?? 'unknown-bot',
      order.deal_id ?? 'unknown-deal'
    ].join(':');
  };

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

  const getOrderTimestamp = (order: OrderRecord): number => {
    const candidates: unknown[] = [
      order.latest_status?.status_timestamp,
      order.latest_status?.statusTimestamp,
      order.latest_status?.observed_at,
      order.latest_status?.observedAt,
      order.latest_status?.updated_at,
      order.latest_status?.updateTime,
      order.latest_status?.time,
      order.latest_status?.timestamp,
      order.latest_submission?.observed_at,
      order.latest_submission?.observedAt,
      order.observed_at,
      order.created_at
    ];

    for (const candidate of candidates) {
      const parsed = parseTimestamp(candidate);
      if (parsed !== null) {
        return parsed;
      }
    }

    return Number.MIN_SAFE_INTEGER;
  };

  const groupOrders = (items: OrderRecord[]): OrderGroup[] => {
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

      const history = sorted.map(entry => entry.order);
      const latest = history[0] ?? entries[0].order;

      return {
        key,
        latest,
        history: history.length > 0 ? history : [entries[0].order]
      };
    });

    return groups.sort((a, b) => {
      const aTime = getOrderTimestamp(a.latest);
      const bTime = getOrderTimestamp(b.latest);
      return bTime - aTime;
    });
  };

  const groupedOrders = useMemo(() => groupOrders(orders), [orders]);

  useEffect(() => {
    setExpandedRows({});
  }, [orders]);

  const fetchOrders = async () => {
    setLoading(true);
    try {
      const params = new URLSearchParams();
      if (filters.metadata) {
        params.append('metadata', filters.metadata);
        params.append('metadata_hex', filters.metadata);
      }
      if (filters.bot_id) params.append('bot_id', filters.bot_id);
      if (filters.deal_id) params.append('deal_id', filters.deal_id);
      if (filters.bot_event_id) params.append('bot_event_id', filters.bot_event_id);
      if (filters.observed_from) params.append('observed_from', new Date(filters.observed_from).toISOString());
      if (filters.observed_to) params.append('observed_to', new Date(filters.observed_to).toISOString());
      params.append('limit', '100');

      const response = await fetch(buildOpsApiUrl(`/api/orders?${params}`));
      
      if (!response.ok) {
        throw new Error('API not available');
      }
      
      const data: ListOrdersResponse = await response.json();
      setOrders(data.items ?? []);
    } catch {
      // Silently use mock data when API is unavailable
      setOrders(generateMockOrders());
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchOrders();

    // Try to set up SSE for real-time updates
    let eventSource: EventSource | null = null;
    
    try {
      const params = new URLSearchParams();
      if (filters.metadata) {
        params.append('metadata', filters.metadata);
        params.append('metadata_hex', filters.metadata);
      }
      if (filters.bot_id) params.append('bot_id', filters.bot_id);
      if (filters.deal_id) params.append('deal_id', filters.deal_id);

      eventSource = new EventSource(buildOpsApiUrl(`/sse/orders?${params}`));
      
      eventSource.onmessage = () => {
        toast.info('Order updated in real-time');
        fetchOrders(); // Refresh the list
      };

      eventSource.onerror = () => {
        // Silently close on error - API not available
        if (eventSource) {
          eventSource.close();
        }
      };
    } catch {
      // SSE not available, continue without real-time updates
    }

    return () => {
      if (eventSource) {
        eventSource.close();
      }
    };
  }, [filters]);

  const getStatusBadge = (order: OrderRecord) => {
    const status = determineStatus(order);
    const normalized = status?.toLowerCase();
    const label = formatStatusLabel(status);

    if (!normalized) {
      return <Badge variant="outline">Pending</Badge>;
    }
    
    if (['filled', 'completed', 'executed'].includes(normalized)) {
      const successLabel = normalized === 'filled' ? 'Filled' : label;
      return <Badge className="bg-green-100 text-green-800 border-green-200">{successLabel}</Badge>;
    }

    if (['failed', 'error', 'cancelled', 'canceled', 'rejected'].includes(normalized)) {
      return <Badge className="bg-red-100 text-red-800 border-red-200">{label}</Badge>;
    }

    if (['open', 'active'].includes(normalized)) {
      return <Badge className="bg-blue-100 text-blue-800 border-blue-200">{label}</Badge>;
    }

    if (normalized === 'pending') {
      return <Badge className="bg-yellow-100 text-yellow-800 border-yellow-200">Pending</Badge>;
    }

    return <Badge variant="outline">{label}</Badge>;
  };

  const formatDate = (date?: string) => {
    if (!date) {
      return '—';
    }

    const parsed = new Date(date);
    if (Number.isNaN(parsed.getTime())) {
      return '—';
    }

    return parsed.toLocaleString();
  };

  const viewDetails = (order: OrderRecord) => {
    setSelectedOrder(order);
    setDetailDialogOpen(true);
  };

  const toggleRow = (key: string) => {
    setExpandedRows(prev => ({
      ...prev,
      [key]: !prev[key]
    }));
  };

  const extractPrice = (order: OrderRecord): string => {
    const statusOrder = order.latest_status?.order as Record<string, unknown> | undefined;
    const value = pickNumericValue(
      order.latest_status?.average_price,
      statusOrder?.averagePrice,
      statusOrder?.limitPx,
      order.latest_submission?.price,
      order.latest_submission?.Price,
      order.bot_event_payload?.Price,
      order.bot_event_payload?.price
    );

    if (value === undefined) {
      return '-';
    }

    return value.toLocaleString('en-US', {
      minimumFractionDigits: 2,
      maximumFractionDigits: 6
    });
  };

  const extractQuantity = (order: OrderRecord): string => {
    const statusOrder = order.latest_status?.order as Record<string, unknown> | undefined;
    const value = pickNumericValue(
      order.latest_status?.filled_size,
      statusOrder?.origSz,
      statusOrder?.sz,
      order.latest_submission?.size,
      order.latest_submission?.Size,
      order.latest_submission?.OrderSize,
      order.bot_event_payload?.Size,
      order.bot_event_payload?.size,
      order.bot_event_payload?.OrderSize,
      order.bot_event_payload?.amount
    );

    if (value === undefined) {
      return '-';
    }

    return value.toLocaleString('en-US', {
      minimumFractionDigits: 2,
      maximumFractionDigits: 8
    });
  };

  const extractSide = (order: OrderRecord): string => {
    const submissionSide = order.latest_submission?.side ?? order.latest_submission?.Side;
    if (typeof submissionSide === 'string') {
      return submissionSide.toUpperCase();
    }

    if (typeof order.latest_submission?.IsBuy === 'boolean') {
      return order.latest_submission.IsBuy ? 'BUY' : 'SELL';
    }

    const statusOrder = order.latest_status?.order as Record<string, unknown> | undefined;
    const statusSide = statusOrder?.side;
    if (typeof statusSide === 'string') {
      const normalized = statusSide.toUpperCase();
      if (normalized === 'B') return 'BUY';
      if (normalized === 'S') return 'SELL';
      return normalized;
    }

    const payloadType = order.bot_event_payload?.Type ?? order.bot_event_payload?.type;
    if (typeof payloadType === 'string') {
      return payloadType.toUpperCase();
    }

    const payloadSide = order.bot_event_payload?.Side;
    if (typeof payloadSide === 'string') {
      return payloadSide.toUpperCase();
    }

    const action = order.bot_event_payload?.Action;
    if (typeof action === 'string') {
      const normalizedAction = action.toLowerCase();
      if (normalizedAction.includes('sell')) return 'SELL';
      if (normalizedAction.includes('buy')) return 'BUY';
    }

    return '-';
  };

  const extractOrderType = (order: OrderRecord): string => {
    const orderType = order.bot_event_payload?.OrderType ?? order.bot_event_payload?.orderType;
    if (typeof orderType === 'string' && orderType.trim()) {
      return orderType;
    }
    return '-';
  };

  const extractOrderPosition = (order: OrderRecord): string => {
    const position = order.bot_event_payload?.OrderPosition ?? order.bot_event_payload?.orderPosition;
    const size = order.bot_event_payload?.OrderSize ?? order.bot_event_payload?.orderSize;
    
    if (typeof position === 'number' && typeof size === 'number') {
      return `${position}/${size}`;
    }
    if (typeof position === 'number') {
      return position.toString();
    }
    return '-';
  };

  const getOrderTypeBadge = (orderType: string) => {
    if (orderType === '-') {
      return <span className="text-xs text-gray-400">-</span>;
    }

    const normalized = orderType.toLowerCase();
    
    if (normalized === 'base') {
      return <Badge className="bg-blue-100 text-blue-800 border-blue-200 text-xs">Base</Badge>;
    }
    
    if (normalized === 'take profit') {
      return <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">Take Profit</Badge>;
    }
    
    if (normalized === 'safety') {
      return <Badge className="bg-orange-100 text-orange-800 border-orange-200 text-xs">Safety</Badge>;
    }
    
    if (normalized === 'manual safety') {
      return <Badge className="bg-amber-100 text-amber-800 border-amber-200 text-xs">Manual Safety</Badge>;
    }
    
    if (normalized === 'stop loss') {
      return <Badge className="bg-red-100 text-red-800 border-red-200 text-xs">Stop Loss</Badge>;
    }
    
    return <Badge variant="outline" className="text-xs">{orderType}</Badge>;
  };

  if (loading) {
    return (
      <Card className="p-8">
        <div className="flex items-center justify-center">
          <RefreshCw className="h-6 w-6 animate-spin text-gray-400 mr-2" />
          <span className="text-gray-600">Loading orders...</span>
        </div>
      </Card>
    );
  }

  return (
    <>
      <Card>
        <div className="p-4 border-b flex items-center justify-between">
          <h2 className="text-gray-900">Orders ({groupedOrders.length})</h2>
          <Button variant="outline" size="sm" onClick={fetchOrders}>
            <RefreshCw className="h-4 w-4 mr-2" />
            Refresh
          </Button>
        </div>

        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="text-xs">Metadata</TableHead>
                <TableHead className="text-xs">Bot</TableHead>
                <TableHead className="text-xs">Deal</TableHead>
                <TableHead className="text-xs">Type</TableHead>
                <TableHead className="text-xs">Pos</TableHead>
                <TableHead className="text-xs">Side</TableHead>
                <TableHead className="text-xs">Price</TableHead>
                <TableHead className="text-xs">Qty</TableHead>
                <TableHead className="text-xs">Time</TableHead>
                <TableHead className="text-xs">Status</TableHead>
                <TableHead className="text-xs text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groupedOrders.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={11} className="text-center text-gray-500 py-8">
                    No orders found
                  </TableCell>
                </TableRow>
              ) : (
                groupedOrders.map((group, index) => {
                  const order = group.latest;
                  const previousStates = group.history.slice(1);
                  const isExpanded = !!expandedRows[group.key];
                  const rowKey = [
                    group.key,
                    order.observed_at ?? order.created_at ?? `idx-${index}`
                  ].join(':');
                  const side = extractSide(order);
                  const isBuy = side.toLowerCase() === 'buy';
                  const isSell = side.toLowerCase() === 'sell';
                  
                  return (
                    <Fragment key={group.key}>
                      <TableRow key={`${rowKey}-latest`}>
                        <TableCell>
                          <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded font-mono">
                            {order.metadata_hex}
                          </code>
                        </TableCell>
                        <TableCell className="text-xs text-gray-900">{order.bot_id}</TableCell>
                        <TableCell className="text-xs text-gray-900">{order.deal_id}</TableCell>
                        <TableCell>{getOrderTypeBadge(extractOrderType(order))}</TableCell>
                        <TableCell className="text-xs text-gray-900">{extractOrderPosition(order)}</TableCell>
                        <TableCell>
                          {isBuy && (
                            <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">
                              {side}
                            </Badge>
                          )}
                          {isSell && (
                            <Badge className="bg-red-100 text-red-800 border-red-200 text-xs">
                              {side}
                            </Badge>
                          )}
                          {!isBuy && !isSell && (
                            <span className="text-xs text-gray-600">{side}</span>
                          )}
                        </TableCell>
                        <TableCell className="text-xs text-gray-900">
                          ${extractPrice(order)}
                        </TableCell>
                        <TableCell className="text-xs text-gray-900">
                          {extractQuantity(order)}
                        </TableCell>
                        <TableCell className="text-xs text-gray-600">
                          {formatDate(order.observed_at ?? order.created_at)}
                        </TableCell>
                        <TableCell>{getStatusBadge(order)}</TableCell>
                        <TableCell>
                          <div className="flex items-center gap-1">
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => viewDetails(order)}
                              className="h-8 px-2 text-xs"
                            >
                              <Eye className="h-3.5 w-3.5 mr-1" />
                              Details
                            </Button>
                            {previousStates.length > 0 && (
                              <Button
                                variant="outline"
                                size="sm"
                                onClick={() => toggleRow(group.key)}
                                className="h-8 px-2 text-xs"
                              >
                                {isExpanded ? (
                                  <>
                                    <ChevronUp className="h-3.5 w-3.5 mr-1" />
                                    Hide
                                  </>
                                ) : (
                                  <>
                                    <ChevronDown className="h-3.5 w-3.5 mr-1" />
                                    History ({previousStates.length})
                                  </>
                                )}
                              </Button>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                      {isExpanded && previousStates.length > 0 && (
                        <>
                          <TableRow className="bg-gray-100">
                            <TableCell colSpan={11} className="py-1.5 px-4">
                              <span className="text-xs text-gray-600">
                                {previousStates.length} earlier update{previousStates.length === 1 ? '' : 's'}
                              </span>
                            </TableCell>
                          </TableRow>
                          {previousStates.map((previousOrder, historyIndex) => {
                            const prevSide = extractSide(previousOrder);
                            const prevIsBuy = prevSide.toLowerCase() === 'buy';
                            const prevIsSell = prevSide.toLowerCase() === 'sell';
                            
                            return (
                              <TableRow key={`${group.key}-history-${historyIndex}`} className="bg-gray-50 hover:bg-gray-100">
                                <TableCell>
                                  <code className="text-xs bg-gray-200 px-1.5 py-0.5 rounded font-mono">
                                    {previousOrder.metadata_hex}
                                  </code>
                                </TableCell>
                                <TableCell className="text-xs text-gray-900">{previousOrder.bot_id}</TableCell>
                                <TableCell className="text-xs text-gray-900">{previousOrder.deal_id}</TableCell>
                                <TableCell>{getOrderTypeBadge(extractOrderType(previousOrder))}</TableCell>
                                <TableCell className="text-xs text-gray-900">{extractOrderPosition(previousOrder)}</TableCell>
                                <TableCell>
                                  {prevIsBuy && (
                                    <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">
                                      {prevSide}
                                    </Badge>
                                  )}
                                  {prevIsSell && (
                                    <Badge className="bg-red-100 text-red-800 border-red-200 text-xs">
                                      {prevSide}
                                    </Badge>
                                  )}
                                  {!prevIsBuy && !prevIsSell && (
                                    <span className="text-xs text-gray-600">{prevSide}</span>
                                  )}
                                </TableCell>
                                <TableCell className="text-xs text-gray-900">
                                  ${extractPrice(previousOrder)}
                                </TableCell>
                                <TableCell className="text-xs text-gray-900">
                                  {extractQuantity(previousOrder)}
                                </TableCell>
                                <TableCell className="text-xs text-gray-600">
                                  {formatDate(previousOrder.observed_at ?? previousOrder.created_at)}
                                </TableCell>
                                <TableCell>{getStatusBadge(previousOrder)}</TableCell>
                                <TableCell>
                                  <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={() => viewDetails(previousOrder)}
                                    className="h-8 px-2 text-xs"
                                  >
                                    <Eye className="h-3.5 w-3.5 mr-1" />
                                    Details
                                  </Button>
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </>
                      )}
                    </Fragment>
                  );
                })
              )}
            </TableBody>
          </Table>
        </div>
      </Card>

      {selectedOrder && (
        <OrderDetailDialog
          order={selectedOrder}
          open={detailDialogOpen}
          onOpenChange={setDetailDialogOpen}
        />
      )}
    </>
  );
}

// Mock data generator for demonstration
function generateMockOrders(): OrderRecord[] {
  return [
    // Order with multiple states - most recent (current state)
    {
      metadata_hex: '0x4f9800fc66338de8e207eae18bf9429a',
      bot_id: 16541235,
      deal_id: 2380849671,
      bot_event_id: 3940649977,
      created_at: '2025-10-15T20:31:03.987Z',
      observed_at: '2025-10-15T23:59:22.220Z',
      bot_event_payload: {
        Action: 'Placing',
        Coin: 'DOGE',
        CreatedAt: '2025-10-15T20:31:03.987Z',
        IsMarket: false,
        OrderPosition: 0,
        OrderSize: 0,
        OrderType: 'Take Profit',
        Price: 0.20221,
        Profit: 0,
        ProfitCurrency: '',
        ProfitPercentage: 0,
        ProfitUSD: 0,
        QuoteCurrency: 'USDT',
        QuoteVolume: 36.19559,
        Size: 179,
        Status: 'Active',
        Text: 'Placing TakeProfit trade. Price: 0.20221 USDT Size: 36.19559 USDT (179.0 DOGE)',
        Type: 'SELL'
      },
      latest_submission: {
        ClientOrderID: '0x4f9800fc66338de8e207eae18bf9429a',
        Coin: 'DOGE',
        IsBuy: false,
        OrderType: { limit: { tif: 'Gtc' } },
        Price: 0.20221,
        ReduceOnly: true,
        Size: 179
      },
      latest_status: {
        order: {
          cloid: '0x4f9800fc66338de8e207eae18bf9429a',
          coin: 'DOGE',
          limitPx: '0.20221',
          oid: 41107904937,
          origSz: '179.0',
          side: 'A',
          sz: '179.0',
          timestamp: 1760572763245
        },
        status: 'open',
        statusTimestamp: 1760572763245
      }
    },
    // Order with multiple states - cancelled state
    {
      metadata_hex: '0x4f9800fc66338de8e207eae18bf9429a',
      bot_id: 16541235,
      deal_id: 2380849671,
      bot_event_id: 3940649977,
      created_at: '2025-10-15T20:31:03.822Z',
      observed_at: '2025-10-15T23:59:22.219Z',
      bot_event_payload: {
        Action: 'Cancel',
        Coin: 'DOGE',
        CreatedAt: '2025-10-15T20:31:03.822Z',
        IsMarket: false,
        OrderPosition: 0,
        OrderSize: 0,
        OrderType: 'Take Profit',
        Price: 0.203,
        Profit: 0,
        ProfitCurrency: '',
        ProfitPercentage: 0,
        ProfitUSD: 0,
        QuoteCurrency: 'USDT',
        QuoteVolume: 20.706,
        Size: 102,
        Status: 'Cancelled',
        Text: 'Cancelling TakeProfit trade. Price: 0.203 USDT Size: 20.706 USDT (102.0 DOGE)',
        Type: 'SELL'
      },
      latest_submission: {
        ClientOrderID: '0x4f9800fc66338de8e207eae18bf9429a',
        Coin: 'DOGE',
        IsBuy: false,
        OrderType: { limit: { tif: 'Gtc' } },
        Price: 0.203,
        ReduceOnly: true,
        Size: 102
      },
      latest_status: {
        status: 'cancelled'
      }
    },
    // Order with multiple states - initial placing
    {
      metadata_hex: '0x4f9800fc66338de8e207eae18bf9429a',
      bot_id: 16541235,
      deal_id: 2380849671,
      bot_event_id: 3940649977,
      created_at: '2025-10-15T20:15:24.353Z',
      observed_at: '2025-10-15T23:59:22.218Z',
      bot_event_payload: {
        Action: 'Placing',
        Coin: 'DOGE',
        CreatedAt: '2025-10-15T20:15:24.353Z',
        IsMarket: false,
        OrderPosition: 0,
        OrderSize: 0,
        OrderType: 'Take Profit',
        Price: 0.203,
        Profit: 0,
        ProfitCurrency: '',
        ProfitPercentage: 0,
        ProfitUSD: 0,
        QuoteCurrency: 'USDT',
        QuoteVolume: 20.706,
        Size: 102,
        Status: 'Active',
        Text: 'Placing TakeProfit trade. Price: 0.203 USDT Size: 20.706 USDT (102.0 DOGE)',
        Type: 'SELL'
      },
      latest_submission: {
        ClientOrderID: '0x4f9800fc66338de8e207eae18bf9429a',
        Coin: 'DOGE',
        IsBuy: false,
        OrderType: { limit: { tif: 'Gtc' } },
        Price: 0.203,
        ReduceOnly: true,
        Size: 102
      },
      latest_status: {
        status: 'filled'
      }
    },
    // Simple filled order
    {
      metadata_hex: '0x1a2b3c4d5e6f7890abcdef1234567890',
      bot_id: 12345,
      deal_id: 67890,
      bot_event_id: 11223,
      created_at: '2025-10-15T10:30:00Z',
      observed_at: '2025-10-15T10:30:15Z',
      bot_event_payload: {
        Action: 'Placing',
        Coin: 'BTC',
        Type: 'BUY',
        Price: 65000,
        Size: 0.1,
        QuoteVolume: 6500,
        OrderType: 'Base',
        Text: 'Placing base order. Price: 65000 USDT Size: 6500 USDT (0.1 BTC)'
      },
      latest_submission: {
        Coin: 'BTC',
        IsBuy: true,
        Size: 0.1,
        Price: 65000
      },
      latest_status: {
        status: 'filled',
        order: {
          coin: 'BTC',
          origSz: '0.1',
          sz: '0.0',
          limitPx: '65000'
        }
      }
    },
    // Pending order
    {
      metadata_hex: '0x9876543210fedcba0987654321fedcba',
      bot_id: 12346,
      deal_id: 67891,
      bot_event_id: 11224,
      created_at: '2025-10-15T11:00:00Z',
      observed_at: '2025-10-15T11:00:20Z',
      bot_event_payload: {
        Action: 'Placing',
        Coin: 'ETH',
        Type: 'SELL',
        Price: 3200,
        Size: 2.5,
        QuoteVolume: 8000,
        OrderType: 'Take Profit',
        Text: 'Placing TakeProfit trade. Price: 3200 USDT Size: 8000 USDT (2.5 ETH)'
      },
      latest_submission: {
        Coin: 'ETH',
        IsBuy: false,
        Size: 2.5,
        Price: 3200
      },
      latest_status: {
        status: 'open',
        order: {
          coin: 'ETH',
          origSz: '2.5',
          sz: '2.5',
          limitPx: '3200'
        }
      }
    },
    // Error order
    {
      metadata_hex: '0xabcdef1234567890abcdef1234567890',
      bot_id: 12347,
      deal_id: 67892,
      bot_event_id: 11225,
      created_at: '2025-10-15T11:30:00Z',
      observed_at: '2025-10-15T11:30:10Z',
      bot_event_payload: {
        Action: 'Placing',
        Coin: 'SOL',
        Type: 'BUY',
        Price: 145,
        Size: 50,
        QuoteVolume: 7250,
        OrderType: 'Base',
        Text: 'Placing base order. Price: 145 USDT Size: 7250 USDT (50 SOL)'
      },
      latest_submission: {
        Coin: 'SOL',
        IsBuy: true,
        Size: 50,
        Price: 145
      },
      latest_status: {
        status: 'error',
        error_message: 'Insufficient balance'
      }
    }
  ];
}
