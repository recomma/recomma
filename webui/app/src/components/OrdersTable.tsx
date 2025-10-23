import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type {
  CancelOrderByMetadataResponse,
  HyperliquidAction,
  HyperliquidCancelAction,
  HyperliquidCreateAction,
  HyperliquidCreateOrder,
  HyperliquidModifyAction,
  ListOrdersResponse,
  OrderFilterState,
  OrderIdentifiers,
  OrderRecord,
  ThreeCommasBotEvent,
} from '../types/api';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { toast } from 'sonner';
import { Eye, SlidersHorizontal, XCircle, TrendingUp, TrendingDown, Activity } from 'lucide-react';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from './ui/alert-dialog';
import { useHyperliquidPrices } from '../hooks/useHyperliquidPrices';
import { buildOpsApiUrl } from '../config/opsApi';
import { logger } from '../utils/logger';
import {
  RevoGrid,
  Template,
  type ColumnDataSchemaModel,
  type ColumnRegular,
  type ColumnTemplateProp,
} from '@revolist/react-datagrid';
import { OrderDetailDialog } from './OrderDetailDialog';
import { Tooltip, TooltipTrigger, TooltipContent } from './ui/tooltip';

interface OrdersTableProps {
  filters: OrderFilterState;
}

type StatusTone = 'success' | 'danger' | 'warning' | 'info' | 'neutral';

const statusToneClasses: Record<StatusTone, string> = {
  success: 'bg-green-100 text-green-800 border-green-200 text-xs',
  danger: 'bg-red-100 text-red-800 border-red-200 text-xs',
  warning: 'bg-yellow-100 text-yellow-800 border-yellow-200 text-xs',
  info: 'bg-blue-100 text-blue-800 border-blue-200 text-xs',
  neutral: 'bg-gray-100 text-gray-800 border-gray-200 text-xs',
};

type SideVariant = 'buy' | 'sell' | 'neutral';

type OrderColumnKey =
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

const COLUMN_ORDER: OrderColumnKey[] = [
  'metadata',
  'botId',
  'dealId',
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

const COLUMN_LABELS: Record<OrderColumnKey, string> = {
  metadata: 'Metadata Hex',
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

const REQUIRED_COLUMNS = new Set<OrderColumnKey>(['metadata', 'actions']);
const DEFAULT_VISIBLE_COLUMNS: OrderColumnKey[] = [...COLUMN_ORDER];

type OrderRow = {
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

const isDataModelProps = (
  props: ColumnDataSchemaModel | ColumnTemplateProp,
): props is ColumnDataSchemaModel => 'model' in props;

interface OrderGroup {
  key: string;
  latest: OrderRecord;
  history: OrderRecord[];
}

interface FetchOrdersOptions {
  showSpinner?: boolean;
  signal?: AbortSignal;
}

const DEFAULT_FETCH_LIMIT = 100;

// Shared cell properties for consistent centering across all columns
const centerCellProps = () => ({
  style: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
  },
});

export function OrdersTable({ filters }: OrdersTableProps) {
  const [orders, setOrders] = useState<OrderRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedOrder, setSelectedOrder] = useState<OrderRecord | null>(null);
  const [detailDialogOpen, setDetailDialogOpen] = useState(false);
  const [visibleColumns, setVisibleColumns] =
    useState<OrderColumnKey[]>(DEFAULT_VISIBLE_COLUMNS);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [orderToCancel, setOrderToCancel] = useState<OrderRecord | null>(null);
  const [isCanceling, setIsCanceling] = useState(false);
  const [columnsDropdownOpen, setColumnsDropdownOpen] = useState(false);

  const isMountedRef = useRef(false);
  const columnsButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const fetchOrders = useCallback(
    async (options: FetchOrdersOptions = {}) => {
      const { showSpinner = true, signal } = options;

      if (showSpinner && isMountedRef.current) {
        setLoading(true);
      }

      const params = createOrderQueryParams(filters);
      const query = params.toString();
      const url = buildOpsApiUrl(query ? `/api/orders?${query}` : '/api/orders');

      try {
        const response = await fetch(url, {
          credentials: 'include',
          signal,
        });

        if (!response.ok) {
          throw new Error('API not available');
        }

        const data: ListOrdersResponse = await response.json();

        if (isMountedRef.current) {
          setOrders(data.items ?? []);
        }
      } catch (error) {
        if (
          (error instanceof DOMException || error instanceof Error) &&
          error.name === 'AbortError'
        ) {
          return;
        }

        if (isMountedRef.current) {
          setOrders(generateMockOrders());
        }
      } finally {
        if (showSpinner && isMountedRef.current) {
          setLoading(false);
        }
      }
    },
    [filters],
  );

  const refreshOrdersForMetadata = useCallback(
    async (metadata: string) => {
      const params = createOrderQueryParams(filters, { includeLimit: false });
      params.set('metadata', metadata);
      params.append('limit', String(DEFAULT_FETCH_LIMIT));

      const url = buildOpsApiUrl(`/api/orders?${params.toString()}`);

      try {
        const response = await fetch(url, {
          credentials: 'include',
        });

        if (!response.ok) {
          throw new Error('API not available');
        }

        const data: ListOrdersResponse = await response.json();

        if (!isMountedRef.current) {
          return;
        }

        const updatedItems = data.items ?? [];
        const metadataKeys =
          updatedItems.length > 0
            ? new Set(updatedItems.map((order) => getMetadataHex(order)))
            : new Set([metadata]);

        setOrders((previous) => {
          const remaining = previous.filter(
            (order) => !metadataKeys.has(getMetadataHex(order)),
          );

          if (updatedItems.length === 0) {
            return remaining;
          }

          return [...remaining, ...updatedItems];
        });
      } catch (error) {
        if (
          (error instanceof DOMException || error instanceof Error) &&
          error.name === 'AbortError'
        ) {
          return;
        }

        if (isMountedRef.current) {
          void fetchOrders({ showSpinner: false });
        }
      }
    },
    [fetchOrders, filters],
  );

  useEffect(() => {
    const abortController = new AbortController();
    let isActive = true;

    void fetchOrders({ showSpinner: true, signal: abortController.signal });

    if (typeof window === 'undefined') {
      return () => {
        isActive = false;
        abortController.abort();
      };
    }

    const params = createOrderQueryParams(filters, { includeLimit: false });
    const query = params.toString();
    const url = buildOpsApiUrl(query ? `/sse/orders?${query}` : '/sse/orders');

    let eventSource: EventSource | null = null;

    try {
      eventSource = new EventSource(url, { withCredentials: true });

      eventSource.onmessage = (event: MessageEvent<string>) => {
        if (!isActive || !isMountedRef.current) {
          return;
        }

        const metadata = extractMetadataFromEvent(event.data);
        if (metadata) {
          toast.info(`Order ${metadata} updated`);
        } else {
          toast.info('Order updated in real-time');
        }

        if (metadata) {
          void refreshOrdersForMetadata(metadata);
        } else {
          void fetchOrders({ showSpinner: false });
        }
      };

      eventSource.onerror = () => {
        eventSource?.close();
      };
    } catch {
      // SSE not available; continue without live updates.
    }

    return () => {
      isActive = false;
      abortController.abort();
      eventSource?.close();
    };
  }, [fetchOrders, filters, refreshOrdersForMetadata]);

  const groupedOrders = useMemo(() => groupOrders(orders), [orders]);

  const rows: OrderRow[] = useMemo(() => {
    return groupedOrders.map((group) => {
      const order = group.latest;
      const identifiers = getIdentifiers(order);
      const metadataHex = getMetadataHex(order);
      const side = extractSide(order);
      const priceValue = getNumericPrice(order);
      const quantityValue = getNumericQuantity(order);
      const { label: statusLabel, tone: statusTone } = getStatusInfo(order);
      const coin = extractCoin(order);
      const isBuy = extractIsBuy(order);

      return {
        id: group.key,
        metadata: metadataHex,
        botId: identifiers.bot_id?.toString() ?? '—',
        dealId: identifiers.deal_id?.toString() ?? '—',
        orderType: extractOrderType(order),
        orderPosition: extractOrderPosition(order),
        side,
        sideVariant: getSideVariant(side),
        price: priceValue ?? null,
        quantity: quantityValue ?? null,
        observedAt: formatDate(order.observed_at ?? identifiers.created_at),
        observedAtTs: getOrderTimestamp(order),
        status: statusLabel,
        statusTone,
        historyCount: Math.max(0, group.history.length - 1),
        actions: '',
        coin,
        isBuy,
        latest: order,
        history: group.history,
      };
    });
  }, [groupedOrders]);

  const uniqueCoins = useMemo(() => {
    const coins = new Set<string>();
    rows.forEach((row) => {
      if (row.coin && row.coin !== 'N/A') {
        coins.add(row.coin);
      }
    });
    const result = Array.from(coins);
    logger.debug('[OrdersTable] Extracted unique coins:', result);
    logger.debug('[OrdersTable] Sample rows with coins:', rows.slice(0, 3).map(r => ({ coin: r.coin, metadata: r.metadata })));
    return result;
  }, [rows]);

  const bboPrices = useHyperliquidPrices(uniqueCoins);

  logger.debug('[OrdersTable] Current BBO prices:', bboPrices);

  const viewDetails = useCallback((order: OrderRecord) => {
    setSelectedOrder(order);
    setDetailDialogOpen(true);
  }, []);

  const handleCancelOrder = useCallback(async () => {
    if (!orderToCancel) return;

    setIsCanceling(true);
    const metadata = orderToCancel.metadata ?? orderToCancel.identifiers.hex;

    try {
      const response = await fetch(
        buildOpsApiUrl(`/api/orders/${metadata}/cancel`),
        {
          method: 'POST',
          credentials: 'include',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({ dry_run: false }),
        },
      );

      if (!response.ok) {
        throw new Error(`Failed to cancel order: ${response.statusText}`);
      }

      const result: CancelOrderByMetadataResponse = await response.json();

      toast.success(`Order cancel ${result.status}`, {
        description: result.message || `Metadata: ${metadata}`,
      });

      // Refresh the order data
      void refreshOrdersForMetadata(metadata);
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      toast.error('Failed to cancel order', {
        description: message,
      });
    } finally {
      setIsCanceling(false);
      setCancelDialogOpen(false);
      setOrderToCancel(null);
    }
  }, [orderToCancel, refreshOrdersForMetadata]);

  const promptCancelOrder = useCallback((order: OrderRecord) => {
    setOrderToCancel(order);
    setCancelDialogOpen(true);
  }, []);

  const metadataTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        const { value } = props;
        return (
          <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded font-mono">
            {value as string}
          </code>
        );
      }),
    [],
  );

  const orderTypeTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        const { value } = props;
        return getOrderTypeBadge(typeof value === 'string' ? value : '-');
      }),
    [],
  );

  const sideTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        const row = props.model as unknown as OrderRow;

        if (row.sideVariant === 'buy') {
          return (
            <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">
              BUY
            </Badge>
          );
        }

        if (row.sideVariant === 'sell') {
          return (
            <Badge className="bg-red-100 text-red-800 border-red-200 text-xs">
              SELL
            </Badge>
          );
        }

        return <span className="text-xs text-gray-600">{props.value as string}</span>;
      }),
    [],
  );

  const priceTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        if (typeof props.value !== 'number') {
          return <span className="text-xs text-gray-400">—</span>;
        }

        const row = props.model as unknown as OrderRow;
        const orderPrice = props.value;
        const isOpen = row.status.toLowerCase() === 'open';
        const bbo = bboPrices.get(row.coin);

        logger.debug('[priceTemplate] Rendering price cell:', {
          coin: row.coin,
          status: row.status,
          isOpen,
          hasBBO: !!bbo,
          bboPricesSize: bboPrices.size,
        });

        // Only show market price for open orders with BBO data
        if (!isOpen || !bbo) {
          return <span className="text-xs text-gray-900">${formatPrice(orderPrice)}</span>;
        }

        // Determine market price based on order side
        const marketPrice = row.isBuy ? bbo.ask.price : bbo.bid.price;
        const priceDiff = marketPrice - orderPrice;

        // Determine if movement is favorable
        // For BUY: favorable if ask < order (can buy cheaper)
        // For SELL: favorable if bid > order (can sell higher)
        const isFavorable = row.isBuy ? priceDiff < 0 : priceDiff > 0;

        return (
          <div className="flex flex-col gap-1">
            <div className="text-xs text-gray-900 font-medium">
              ${formatPrice(orderPrice)}
            </div>
            <div className={`text-xs flex items-center gap-1 font-medium ${isFavorable ? 'text-green-600' : 'text-red-600'}`}>
              <span>${formatPrice(marketPrice)}</span>
              {row.isBuy ? (
                isFavorable ? (
                  <TrendingDown className="h-3.5 w-3.5" />
                ) : (
                  <TrendingUp className="h-3.5 w-3.5" />
                )
              ) : isFavorable ? (
                <TrendingUp className="h-3.5 w-3.5" />
              ) : (
                <TrendingDown className="h-3.5 w-3.5" />
              )}
            </div>
          </div>
        );
      }),
    [bboPrices],
  );

  const quantityTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        if (typeof props.value !== 'number') {
          return <span className="text-xs text-gray-400">—</span>;
        }
        return <span className="text-xs text-gray-900">{formatQuantity(props.value)}</span>;
      }),
    [],
  );

  const observedAtTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        return <span className="text-xs text-gray-600">{props.value as string}</span>;
      }),
    [],
  );

  const statusTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        const row = props.model as unknown as OrderRow;
        const tone = statusToneClasses[row.statusTone];
        return <Badge className={tone}>{props.value as string}</Badge>;
      }),
    [],
  );

  const historyTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        const count = typeof props.value === 'number' ? props.value : 0;
        if (count === 0) {
          return <span className="text-xs text-gray-500">—</span>;
        }
        return (
          <span className="text-xs text-gray-900">
            {count} update{count === 1 ? '' : 's'}
          </span>
        );
      }),
    [],
  );

  const actionsTemplate = useMemo(
    () =>
      Template((props: ColumnDataSchemaModel | ColumnTemplateProp) => {
        if (!isDataModelProps(props)) {
          return null;
        }
        const row = props.model as unknown as OrderRow;
        const isOpen = row.status.toLowerCase() === 'open';

        return (
          <div className="flex items-center gap-1">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-8 w-8 p-0"
                  onClick={() => viewDetails(row.latest)}
                >
                  <Eye className="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent sideOffset={5}>View Details</TooltipContent>
            </Tooltip>
            {isOpen && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="destructive"
                    size="sm"
                    className="h-8 w-8 p-0"
                    onClick={() => promptCancelOrder(row.latest)}
                  >
                    <XCircle className="h-4 w-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent sideOffset={5}>Cancel Order</TooltipContent>
              </Tooltip>
            )}
          </div>
        );
      }),
    [viewDetails, promptCancelOrder],
  );

  const columnDefinitions = useMemo<Record<OrderColumnKey, ColumnRegular>>(
    () => ({
      metadata: {
        prop: 'metadata',
        name: COLUMN_LABELS.metadata,
        size: 220,
        sortable: true,
        cellTemplate: metadataTemplate,
        cellProperties: centerCellProps,
      },
      botId: {
        prop: 'botId',
        name: COLUMN_LABELS.botId,
        size: 110,
        sortable: true,
        cellProperties: centerCellProps,
      },
      dealId: {
        prop: 'dealId',
        name: COLUMN_LABELS.dealId,
        size: 110,
        sortable: true,
        cellProperties: centerCellProps,
      },
      orderType: {
        prop: 'orderType',
        name: COLUMN_LABELS.orderType,
        size: 110,
        sortable: true,
        cellTemplate: orderTypeTemplate,
        cellProperties: centerCellProps,
      },
      orderPosition: {
        prop: 'orderPosition',
        name: COLUMN_LABELS.orderPosition,
        size: 80,
        sortable: true,
        cellProperties: centerCellProps,
      },
      side: {
        prop: 'side',
        name: COLUMN_LABELS.side,
        size: 100,
        sortable: true,
        cellTemplate: sideTemplate,
        cellProperties: centerCellProps,
      },
      price: {
        prop: 'price',
        name: COLUMN_LABELS.price,
        size: 150,
        sortable: true,
        cellTemplate: priceTemplate,
        cellProperties: centerCellProps,
        cellCompare: (_prop, a, b) =>
          (a.price ?? Number.NEGATIVE_INFINITY) -
          (b.price ?? Number.NEGATIVE_INFINITY),
      },
      quantity: {
        prop: 'quantity',
        name: COLUMN_LABELS.quantity,
        size: 140,
        sortable: true,
        cellTemplate: quantityTemplate,
        cellProperties: centerCellProps,
        cellCompare: (_prop, a, b) =>
          (a.quantity ?? Number.NEGATIVE_INFINITY) -
          (b.quantity ?? Number.NEGATIVE_INFINITY),
      },
      observedAt: {
        prop: 'observedAt',
        name: COLUMN_LABELS.observedAt,
        size: 200,
        sortable: true,
        order: 'desc',
        cellTemplate: observedAtTemplate,
        cellProperties: centerCellProps,
        cellCompare: (_prop, a, b) =>
          (a.observedAtTs ?? Number.NEGATIVE_INFINITY) -
          (b.observedAtTs ?? Number.NEGATIVE_INFINITY),
      },
      status: {
        prop: 'status',
        name: COLUMN_LABELS.status,
        size: 150,
        sortable: true,
        cellTemplate: statusTemplate,
        cellProperties: centerCellProps,
      },
      historyCount: {
        prop: 'historyCount',
        name: COLUMN_LABELS.historyCount,
        size: 130,
        sortable: true,
        cellTemplate: historyTemplate,
        cellProperties: centerCellProps,
        cellCompare: (_prop, a, b) =>
          (a.historyCount ?? 0) - (b.historyCount ?? 0),
      },
      actions: {
        prop: 'actions',
        name: COLUMN_LABELS.actions,
        size: 100,
        sortable: false,
        cellTemplate: actionsTemplate,
        cellProperties: centerCellProps,
      },
    }),
    [
      actionsTemplate,
      historyTemplate,
      metadataTemplate,
      observedAtTemplate,
      orderTypeTemplate,
      priceTemplate,
      quantityTemplate,
      sideTemplate,
      statusTemplate,
    ],
  );

  const visibleColumnSet = useMemo(
    () => new Set<OrderColumnKey>(visibleColumns),
    [visibleColumns],
  );

  const columns = useMemo(
    () =>
      COLUMN_ORDER.filter((key) => visibleColumnSet.has(key)).map(
        (key) => columnDefinitions[key],
      ),
    [columnDefinitions, visibleColumnSet],
  );

  const handleColumnVisibilityChange = useCallback(
    (column: OrderColumnKey, value: boolean | 'indeterminate') => {
      if (value === 'indeterminate' || REQUIRED_COLUMNS.has(column)) {
        return;
      }

      setVisibleColumns((current) => {
        const next = new Set(current);
        if (value) {
          next.add(column);
        } else {
          next.delete(column);
        }
        REQUIRED_COLUMNS.forEach((required) => next.add(required));
        return COLUMN_ORDER.filter((key) => next.has(key));
      });
    },
    [],
  );

  if (loading) {
    return (
      <div className="flex h-full flex-col">
        <div className="flex-1 flex items-center justify-center p-8 bg-white">
          <div className="flex items-center justify-center">
            <Activity className="h-6 w-6 animate-pulse text-gray-400 mr-2" />
            <span className="text-gray-600">Loading orders...</span>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      <div className="flex-1 flex flex-col gap-0 bg-white min-h-0">
        <div className="px-4 py-2 border-b flex items-center justify-between shrink-0 relative z-10 bg-white">
          <h2 className="text-gray-900">Orders ({rows.length})</h2>
          <div className="relative">
            <Button
              ref={columnsButtonRef}
              variant="outline"
              size="sm"
              className="h-8 px-2 text-xs"
              onClick={() => setColumnsDropdownOpen(!columnsDropdownOpen)}
            >
              <SlidersHorizontal className="h-3.5 w-3.5 mr-1" />
              Columns
            </Button>
            {columnsDropdownOpen && (
              <>
                <div
                  className="fixed inset-0 z-40"
                  onClick={() => setColumnsDropdownOpen(false)}
                />
                <div className="absolute right-0 top-full mt-1 w-48 bg-white border border-gray-200 rounded-md shadow-lg z-50 py-1">
                  <div className="px-3 py-2 text-sm font-semibold border-b">
                    Visible Columns
                  </div>
                  {COLUMN_ORDER.map((key) => (
                    <label
                      key={key}
                      className={`flex items-center gap-2 px-3 py-2 text-sm hover:bg-gray-100 cursor-pointer ${
                        REQUIRED_COLUMNS.has(key) ? 'opacity-50 cursor-not-allowed' : ''
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={visibleColumnSet.has(key)}
                        disabled={REQUIRED_COLUMNS.has(key)}
                        onChange={(e) => handleColumnVisibilityChange(key, e.target.checked)}
                        className="rounded border-gray-300"
                      />
                      <span>{COLUMN_LABELS[key]}</span>
                    </label>
                  ))}
                </div>
              </>
            )}
          </div>
        </div>

        {rows.length === 0 ? (
          <div className="flex flex-1 items-center justify-center text-gray-500 min-h-0">
            No orders found
          </div>
        ) : (
          <div className="flex-1 min-h-0 revogrid-wrapper">
            <RevoGrid
              theme="default"
              columns={columns}
              source={rows}
              readonly={true}
              canMoveColumns={true}
              resize={true}
              range={true}
              autoSizeColumn={false}
              rowSize={60}
              style={{ height: '100%', width: '100%' }}
            />
          </div>
        )}
      </div>

      {selectedOrder && (
        <OrderDetailDialog
          order={selectedOrder}
          open={detailDialogOpen}
          onOpenChange={setDetailDialogOpen}
        />
      )}

      <AlertDialog open={cancelDialogOpen} onOpenChange={setCancelDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Cancel Order</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to cancel this order? This action cannot be undone.
              {orderToCancel && (
                <div className="mt-3 p-2 bg-gray-50 rounded text-xs">
                  <div className="font-mono text-gray-700">
                    {orderToCancel.metadata ?? orderToCancel.identifiers.hex}
                  </div>
                </div>
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isCanceling}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleCancelOrder}
              disabled={isCanceling}
              className="bg-red-600 hover:bg-red-700"
            >
              {isCanceling ? 'Canceling...' : 'Confirm Cancel'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

const createOrderQueryParams = (
  filters: OrderFilterState,
  options: { includeLimit?: boolean; limit?: number } = {},
): URLSearchParams => {
  const params = new URLSearchParams();

  if (filters.metadata?.trim()) {
    params.append('metadata', filters.metadata.trim());
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

const toIsoString = (value?: string): string | undefined => {
  if (!value) {
    return undefined;
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return undefined;
  }
  return date.toISOString();
};

const extractMetadataFromEvent = (data: string): string | null => {
  if (!data) {
    return null;
  }

  try {
    const parsed = JSON.parse(data) as Partial<
      OrderRecord & {
        metadata?: string;
        identifiers?: { hex?: string };
      }
    >;

    return parsed.metadata ?? parsed.identifiers?.hex ?? null;
  } catch {
    return null;
  }
};

const isCreateOrModifyAction = (
  action: HyperliquidAction | undefined,
): action is HyperliquidCreateAction | HyperliquidModifyAction =>
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

const getMetadataHex = (order: OrderRecord): string =>
  order.metadata ?? order.identifiers.hex;

const getIdentifiers = (order: OrderRecord): OrderIdentifiers => order.identifiers;

const getThreeCommasEvent = (order: OrderRecord): ThreeCommasBotEvent =>
  order.three_commas.event;

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

const determineStatusTone = (normalizedStatus?: string): StatusTone => {
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

const getStatusInfo = (order: OrderRecord): { label: string; tone: StatusTone } => {
  const status = determineStatus(order);
  const normalized = status?.toLowerCase();
  return {
    label: formatStatusLabel(status),
    tone: determineStatusTone(normalized),
  };
};

const buildOrderIdentity = (order: OrderRecord): string => {
  const identifiers = getIdentifiers(order);
  return [
    getMetadataHex(order),
    identifiers.bot_event_id?.toString() ?? 'unknown-event',
    identifiers.bot_id?.toString() ?? 'unknown-bot',
    identifiers.deal_id?.toString() ?? 'unknown-deal',
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

const extractOrderPosition = (order: OrderRecord): string => {
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

const extractSide = (order: OrderRecord): string => {
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

const extractOrderType = (order: OrderRecord): string => {
  const eventOrderType = getThreeCommasEvent(order)?.order_type;
  if (typeof eventOrderType === 'string' && eventOrderType.trim()) {
    return eventOrderType.replace(/_/g, ' ');
  }
  return '-';
};

const extractCoin = (order: OrderRecord): string => {
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

const extractIsBuy = (order: OrderRecord): boolean | null => {
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

const getOrderTypeBadge = (orderType: string) => {
  if (orderType === '-') {
    return <span className="text-xs text-gray-400">-</span>;
  }

  const normalized = orderType.toLowerCase();

  if (normalized === 'base') {
    return (
      <Badge className="bg-blue-100 text-blue-800 border-blue-200 text-xs">Base</Badge>
    );
  }

  if (normalized === 'take profit') {
    return (
      <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">
        Take Profit
      </Badge>
    );
  }

  if (normalized === 'safety') {
    return (
      <Badge className="bg-orange-100 text-orange-800 border-orange-200 text-xs">
        Safety
      </Badge>
    );
  }

  if (normalized === 'manual safety') {
    return (
      <Badge className="bg-amber-100 text-amber-800 border-amber-200 text-xs">
        Manual Safety
      </Badge>
    );
  }

  if (normalized === 'stop loss') {
    return (
      <Badge className="bg-red-100 text-red-800 border-red-200 text-xs">Stop Loss</Badge>
    );
  }

  return <Badge variant="outline" className="text-xs">{orderType}</Badge>;
};

const getNumericPrice = (order: OrderRecord): number | undefined => {
  const statusOrder = order.hyperliquid?.latest_status?.order;
  const submissionOrder = getSubmissionOrder(order.hyperliquid?.latest_submission);
  const event = getThreeCommasEvent(order);
  return pickNumericValue(statusOrder?.limit_px, submissionOrder?.price, event?.price);
};

const getNumericQuantity = (order: OrderRecord): number | undefined => {
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

const formatPrice = (value: number): string =>
  value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  });

const formatQuantity = (value: number): string =>
  value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 8,
  });

const getSideVariant = (side: string): SideVariant => {
  if (side.toUpperCase() === 'BUY') {
    return 'buy';
  }
  if (side.toUpperCase() === 'SELL') {
    return 'sell';
  }
  return 'neutral';
};

function generateMockOrders(): OrderRecord[] {
  const now = new Date().toISOString();

  const sampleOrders: OrderRecord[] = [
    {
      metadata: 'mock-order-1',
      identifiers: {
        hex: 'mock-order-1',
        bot_id: 16541235,
        deal_id: 2380849671,
        bot_event_id: 3940649977,
        created_at: now,
      },
      observed_at: now,
      three_commas: {
        event: {
          created_at: now,
          action: 'Placing',
          coin: 'BTC',
          type: 'BUY',
          status: 'active',
          price: 65234.12,
          size: 0.15,
          order_type: 'take_profit',
          order_size: 1,
          order_position: 1,
          quote_volume: 9785.118,
          quote_currency: 'USDT',
          is_market: false,
          profit: 0,
          profit_currency: 'USDT',
          profit_percentage: 0,
          profit_usd: 0,
          text: 'Mock order generated for offline usage.',
        },
      },
      hyperliquid: {
        latest_submission: {
          kind: 'create',
          order: {
            coin: 'BTC',
            is_buy: true,
            price: 65234.12,
            size: 0.15,
            reduce_only: false,
            order_type: { limit: { tif: 'Gtc' } },
            client_order_id: 'mock-order-1',
          },
        },
        latest_status: {
          status: 'open',
          status_timestamp: now,
          order: {
            coin: 'BTC',
            side: 'B',
            limit_px: '65234.12',
            size: '0.15',
            orig_size: '0.15',
            timestamp: now,
            client_order_id: 'mock-order-1',
          },
        },
      },
      log_entries: [],
    } as unknown as OrderRecord,
    {
      metadata: 'mock-order-2',
      identifiers: {
        hex: 'mock-order-2',
        bot_id: 17551234,
        deal_id: 2380849672,
        bot_event_id: 3940649980,
        created_at: now,
      },
      observed_at: now,
      three_commas: {
        event: {
          created_at: now,
          action: 'Closing',
          coin: 'ETH',
          type: 'SELL',
          status: 'completed',
          price: 3580.45,
          size: 2.5,
          order_type: 'safety',
          order_size: 2,
          order_position: 2,
          quote_volume: 8951.125,
          quote_currency: 'USDT',
          is_market: false,
          profit: 124.55,
          profit_currency: 'USDT',
          profit_percentage: 3.6,
          profit_usd: 124.55,
          text: 'Mock closing order generated for offline usage.',
        },
      },
      hyperliquid: {
        latest_submission: {
          kind: 'create',
          order: {
            coin: 'ETH',
            is_buy: false,
            price: 3580.45,
            size: 2.5,
            reduce_only: true,
            order_type: { limit: { tif: 'Gtc' } },
            client_order_id: 'mock-order-2',
          },
        },
        latest_status: {
          status: 'filled',
          status_timestamp: now,
          order: {
            coin: 'ETH',
            side: 'A',
            limit_px: '3580.45',
            size: '0',
            orig_size: '2.5',
            timestamp: now,
            client_order_id: 'mock-order-2',
          },
        },
      },
      log_entries: [],
    } as unknown as OrderRecord,
  ];

  return sampleOrders;
}
