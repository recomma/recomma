import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  BotRecord,
  CancelOrderByOrderIdResponse,
  DealRecord,
  ListBotsResponse,
  OrderFilterState,
  OrderRecord,
} from '../../types/api';
import { Button } from '../ui/button';
import { Card } from '../ui/card';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { toast } from 'sonner';
import { SlidersHorizontal, ChevronDown, ChevronRight, Activity } from 'lucide-react';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '../ui/alert-dialog';
import { useHyperliquidPrices } from '../../hooks/useHyperliquidPrices';
import { buildOpsApiUrl } from '../../config/opsApi';
import {
  Grid,
  useClientRowDataSource,
} from '@1771technologies/lytenyte-core';
import type {
  Column,
  RowFullWidthPredicate,
  RowFullWidthPredicateParams,
} from '@1771technologies/lytenyte-core/types';
import { OrderDetailDialog } from '../OrderDetailDialog';
import { DealDetailDialog } from '../DealDetailDialog';
import { BotDetailDialog } from '../BotDetailDialog';

// Local imports from extracted modules
import type { OrderColumnKey, OrderRow, TableRow, DealGroupRow } from './types';
import { statusToneClasses } from './types';
import { COLUMN_ORDER, COLUMN_LABELS, REQUIRED_COLUMNS, DEFAULT_VISIBLE_COLUMNS } from './constants';
import { useOrdersData } from './hooks/useOrdersData';
import { groupOrders } from './utils/orderDataTransformers';
import {
  getOrderIdHex as getOrderIdHex,
  getIdentifiers,
  extractSide,
  extractOrderType,
  extractOrderPosition,
  extractCoin,
  extractIsBuy,
  getNumericPrice,
  getNumericQuantity,
  getSideVariant,
} from './utils/orderFieldExtractors';
import { getOrderTimestamp } from './utils/orderDataTransformers';
import { formatDate } from './utils/orderFormatters';
import { getStatusInfo } from './utils/orderStatus';
import {
  orderIdCellRenderer,
  orderTypeCellRenderer,
  sideCellRenderer,
  createPriceCellRenderer,
  quantityCellRenderer,
  observedAtCellRenderer,
  createStatusCellRenderer,
  historyCellRenderer,
  createActionsCellRenderer,
} from './renderers/cellRenderers';
import { DealHeaderRenderer } from './renderers/DealHeaderRenderer';
import { FilterControls } from './FilterControls';

export interface OrdersTableProps {
  filters: OrderFilterState;
  selectedBotId?: number;
  selectedDealId?: number;
  onBotSelect: (botId: number | undefined) => void;
  onDealSelect: (dealId: number | undefined) => void;
  onFiltersChange: (filters: OrderFilterState) => void;
}

export function OrdersTable({ filters, selectedBotId, selectedDealId, onBotSelect, onDealSelect, onFiltersChange }: OrdersTableProps) {
  const { orders, deals, loading, refreshOrdersForMetadata } = useOrdersData(filters);

  const [selectedOrder, setSelectedOrder] = useState<OrderRecord | null>(null);
  const [selectedDeal, setSelectedDeal] = useState<DealRecord | null>(null);
  const [detailDialogOpen, setDetailDialogOpen] = useState(false);
  const [dealDetailDialogOpen, setDealDetailDialogOpen] = useState(false);
  const [selectedBot, setSelectedBot] = useState<BotRecord | null>(null);
  const [botDetailDialogOpen, setBotDetailDialogOpen] = useState(false);
  const [bulkCancelDialogOpen, setBulkCancelDialogOpen] = useState(false);
  const [bulkCancelContext, setBulkCancelContext] = useState<{ dealId: string; orderIds: string[] } | null>(null);
  const [isBulkCanceling, setIsBulkCanceling] = useState(false);
  const [visibleColumns, setVisibleColumns] =
    useState<OrderColumnKey[]>(DEFAULT_VISIBLE_COLUMNS);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [orderToCancel, setOrderToCancel] = useState<OrderRecord | null>(null);
  const [isCanceling, setIsCanceling] = useState(false);
  const [columnsPopoverOpen, setColumnsPopoverOpen] = useState(false);
  const [expandedDeals, setExpandedDeals] = useState<Map<string, boolean>>(new Map());
  const [allExpanded, setAllExpanded] = useState(true);

  const groupedOrders = useMemo(() => groupOrders(orders), [orders]);

  const dealsMap = useMemo(() => {
    const map = new Map<string, DealRecord>();
    deals.forEach((deal) => {
      map.set(deal.deal_id.toString(), deal);
    });
    return map;
  }, [deals]);

  const orderRows: OrderRow[] = useMemo(() => {
    return groupedOrders.map((group) => {
      const order = group.latest;
      const identifiers = getIdentifiers(order);
      const oidHex = getOrderIdHex(order);
      const side = extractSide(order);
      const priceValue = getNumericPrice(order);
      const quantityValue = getNumericQuantity(order);
      const { label: statusLabel, tone: statusTone } = getStatusInfo(order);
      const coin = extractCoin(order);
      const isBuy = extractIsBuy(order);

      return {
        rowType: 'order' as const,
        id: group.key,
        orderId: oidHex,
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

  const rows: TableRow[] = useMemo(() => {
    const dealGroups = new Map<string, OrderRow[]>();
    orderRows.forEach((row) => {
      const dealId = row.dealId;
      if (!dealGroups.has(dealId)) {
        dealGroups.set(dealId, []);
      }
      dealGroups.get(dealId)!.push(row);
    });

    const result: TableRow[] = [];

    const sortedDeals = Array.from(dealGroups.entries()).sort((a, b) => {
      const aLatest = Math.max(...a[1].map((row) => row.observedAtTs));
      const bLatest = Math.max(...b[1].map((row) => row.observedAtTs));
      return bLatest - aLatest;
    });

    sortedDeals.forEach(([dealId, orders]) => {
      const deal = dealsMap.get(dealId) ?? null;
      const firstOrder = orders[0];
      const orderIds = new Set(orders.map((o) => o.orderId));

      const dealRow: DealGroupRow = {
        rowType: 'deal-header' as const,
        id: `deal-${dealId}`,
        dealId,
        botId: firstOrder?.botId ?? '—',
        deal,
        orderCount: orders.length,
        orderIds,
        orderId: '',
        orderType: '',
        orderPosition: '',
        side: '',
        sideVariant: 'neutral' as const,
        price: null,
        quantity: null,
        observedAt: '',
        observedAtTs: 0,
        status: '',
        statusTone: 'neutral' as const,
        historyCount: 0,
        actions: '',
        coin: firstOrder?.coin ?? '',
        isBuy: null,
      };

      result.push(dealRow);

      const isExpanded = expandedDeals.get(dealId) ?? allExpanded;
      if (isExpanded) {
        result.push(...orders);
      }
    });

    return result;
  }, [orderRows, dealsMap, expandedDeals, allExpanded]);

  const uniqueCoins = useMemo(() => {
    const coins = new Set<string>();
    rows.forEach((row) => {
      if (row.rowType === 'order' && row.coin && row.coin !== 'N/A') {
        coins.add(row.coin);
      }
    });
    return Array.from(coins);
  }, [rows]);

  const bboPrices = useHyperliquidPrices(uniqueCoins);

  const viewDetails = useCallback((order: OrderRecord) => {
    setSelectedOrder(order);
    setDetailDialogOpen(true);
  }, []);

  const viewDealDetails = useCallback((deal: DealRecord) => {
    setSelectedDeal(deal);
    setDealDetailDialogOpen(true);
  }, []);

  const viewBotDetails = useCallback(async (botId: string) => {
    const numericId = Number(botId);
    if (!Number.isFinite(numericId)) {
      toast.error('Bot details unavailable', {
        description: 'Bot identifier is missing for this deal.',
      });
      return;
    }

    try {
      const response = await fetch(
        buildOpsApiUrl(`/api/bots?bot_id=${numericId}`),
        {
          credentials: 'include',
        },
      );

      if (!response.ok) {
        throw new Error(`Failed to load bot ${numericId}`);
      }

      const data: ListBotsResponse = await response.json();
      const bot = data.items?.[0] ?? null;

      if (!bot) {
        toast.error('Bot details unavailable', {
          description: `No bot found for #${numericId}.`,
        });
        return;
      }

      setSelectedBot(bot);
      setBotDetailDialogOpen(true);
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      toast.error('Failed to load bot details', {
        description: message,
      });
    }
  }, []);

  const toggleDeal = useCallback((dealId: string) => {
    setExpandedDeals((prev) => {
      const next = new Map(prev);
      const current = next.get(dealId) ?? allExpanded;
      next.set(dealId, !current);
      return next;
    });
  }, [allExpanded]);

  const toggleAllDeals = useCallback(() => {
    setAllExpanded((prev) => !prev);
    setExpandedDeals(new Map());
  }, []);

  const handleCancelOrder = useCallback(async () => {
    if (!orderToCancel) return;

    setIsCanceling(true);
    const orderId = orderToCancel.order_id ?? orderToCancel.identifiers.hex;

    try {
      const response = await fetch(
        buildOpsApiUrl(`/api/orders/${orderId}/cancel`),
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

      const result: CancelOrderByOrderIdResponse = await response.json();

      toast.success(`Order cancel ${result.status}`, {
        description: result.message || `Metadata: ${orderId}`,
      });

      void refreshOrdersForMetadata(orderId);
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

  const promptCancelAllOrders = useCallback((orderIds: Set<string>, dealId: string) => {
    const hashArray = Array.from(orderIds);
    if (hashArray.length === 0) {
      toast.info('No orders to cancel for this deal.', {
        description: `Deal #${dealId}`,
      });
      return;
    }

    setBulkCancelContext({
      dealId,
      orderIds: hashArray,
    });
    setBulkCancelDialogOpen(true);
  }, []);

  const executeCancelAllOrders = useCallback(async (orderIds: Set<string>, dealId: string) => {
    const hashArray = Array.from(orderIds);
    const totalCount = hashArray.length;

    toast.info(`Canceling ${totalCount} order${totalCount === 1 ? '' : 's'}...`, {
      description: `Deal #${dealId}`,
    });

    let successCount = 0;
    let failCount = 0;

    for (const orderId of hashArray) {
      try {
        const response = await fetch(
          buildOpsApiUrl(`/api/orders/${orderId}/cancel`),
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

        successCount++;
      } catch {
        failCount++;
      }
    }

    if (successCount > 0) {
      toast.success(`Canceled ${successCount} order${successCount === 1 ? '' : 's'}`, {
        description: failCount > 0 ? `${failCount} failed` : undefined,
      });

      for (const orderId of hashArray) {
        void refreshOrdersForMetadata(orderId);
      }
    }

    if (failCount > 0 && successCount === 0) {
      toast.error('Failed to cancel orders', {
        description: `All ${failCount} cancellation attempts failed`,
      });
    }
  }, [refreshOrdersForMetadata]);

  const handleConfirmBulkCancel = useCallback(async () => {
    if (!bulkCancelContext) {
      return;
    }

    setIsBulkCanceling(true);
    try {
      await executeCancelAllOrders(new Set(bulkCancelContext.orderIds), bulkCancelContext.dealId);
      setBulkCancelDialogOpen(false);
      setBulkCancelContext(null);
    } finally {
      setIsBulkCanceling(false);
    }
  }, [bulkCancelContext, executeCancelAllOrders]);

  // Row full-width predicate
  const rowFullWidthPredicate: RowFullWidthPredicate<TableRow> = useCallback((params: RowFullWidthPredicateParams<TableRow>) => {
    return params.row.data?.rowType === 'deal-header';
  }, []);

  // Create cell renderers with dependencies
  const priceCellRenderer = useMemo(() => createPriceCellRenderer(bboPrices), [bboPrices]);
  const statusCellRenderer = useMemo(() => createStatusCellRenderer(statusToneClasses), []);
  const actionsCellRenderer = useMemo(
    () => createActionsCellRenderer(viewDetails, promptCancelOrder),
    [viewDetails, promptCancelOrder],
  );

  // Deal header renderer
  const dealHeaderRenderer = useCallback(
    (params: Parameters<typeof DealHeaderRenderer>[0]['params']) => {
      const row = params.row.data;
      const dealRow =
        row && typeof row === 'object' && 'rowType' in row && (row as TableRow).rowType === 'deal-header'
          ? (row as DealGroupRow)
          : undefined;
      const coin = dealRow?.coin;
      const bbo = typeof coin === 'string' ? bboPrices.get(coin) : undefined;

      return (
        <DealHeaderRenderer
          params={params}
          expandedDeals={expandedDeals}
          allExpanded={allExpanded}
          onToggleDeal={toggleDeal}
          onViewDealDetails={viewDealDetails}
          onCancelAllOrders={promptCancelAllOrders}
          onViewBotDetails={viewBotDetails}
          bbo={bbo}
        />
      );
    },
    [expandedDeals, allExpanded, toggleDeal, viewDealDetails, viewBotDetails, promptCancelAllOrders, bboPrices],
  );

  const columnDefinitions = useMemo<Record<OrderColumnKey, Column<TableRow>>>(
    () => ({
      orderId: {
        id: 'orderId',
        name: COLUMN_LABELS.orderId,
        width: 200,
        widthMin: 150,
        cellRenderer: orderIdCellRenderer,
      },
      botId: {
        id: 'botId',
        name: COLUMN_LABELS.botId,
        width: 90,
        widthMin: 70,
      },
      dealId: {
        id: 'dealId',
        name: COLUMN_LABELS.dealId,
        width: 90,
        widthMin: 70,
      },
      orderType: {
        id: 'orderType',
        name: COLUMN_LABELS.orderType,
        width: 120,
        widthMin: 100,
        cellRenderer: orderTypeCellRenderer,
      },
      orderPosition: {
        id: 'orderPosition',
        name: COLUMN_LABELS.orderPosition,
        width: 90,
        widthMin: 80,
      },
      side: {
        id: 'side',
        name: COLUMN_LABELS.side,
        width: 80,
        widthMin: 60,
        cellRenderer: sideCellRenderer,
      },
      price: {
        id: 'price',
        name: COLUMN_LABELS.price,
        width: 120,
        widthMin: 100,
        cellRenderer: priceCellRenderer,
      },
      quantity: {
        id: 'quantity',
        name: COLUMN_LABELS.quantity,
        width: 100,
        widthMin: 80,
        cellRenderer: quantityCellRenderer,
      },
      observedAt: {
        id: 'observedAt',
        name: COLUMN_LABELS.observedAt,
        width: 160,
        widthMin: 140,
        cellRenderer: observedAtCellRenderer,
      },
      status: {
        id: 'status',
        name: COLUMN_LABELS.status,
        width: 100,
        widthMin: 80,
        cellRenderer: statusCellRenderer,
      },
      historyCount: {
        id: 'historyCount',
        name: COLUMN_LABELS.historyCount,
        width: 90,
        widthMin: 70,
        cellRenderer: historyCellRenderer,
      },
      actions: {
        id: 'actions',
        name: COLUMN_LABELS.actions,
        width: 100,
        widthMin: 80,
        cellRenderer: actionsCellRenderer,
      },
    }),
    [priceCellRenderer, statusCellRenderer, actionsCellRenderer],
  );

  const columns = useMemo(
    () =>
      COLUMN_ORDER.map((key) => ({
        ...columnDefinitions[key],
        hide: !visibleColumns.includes(key),
      })),
    [columnDefinitions, visibleColumns],
  );

  const dataSource = useClientRowDataSource({
    data: rows,
    rowIdLeaf: (d, index) => `${index}-${d.id}`,
    reflectData: true,
  });

  const grid = Grid.useLyteNyte<TableRow>({
    gridId: 'orders-table',
    columns,
    columnBase: {
      uiHints: {
        movable: true,
      },
    },
    columnSizeToFit: true,
    rowDataSource: dataSource,
    rowFullWidthPredicate,
    rowFullWidthRenderer: dealHeaderRenderer,
  });

  const view = grid.view.useValue();

  // Auto-size columns after grid renders
  useEffect(() => {
    if (rows.length > 0) {
      // Delay to ensure grid has rendered rows before measuring
      const timeoutId = setTimeout(() => {
        grid.api.columnAutosize({ includeHeader: true });
      }, 100);

      return () => clearTimeout(timeoutId);
    }
  }, [rows.length, grid.api]);

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

      // Update the grid to show/hide the column
      grid.api.columnUpdate({
        [column]: { hide: !value },
      });
    },
    [grid.api],
  );

  if (loading) {
    return (
      <div className="container mx-auto flex h-full flex-col">
        <div className="flex flex-1 justify-center overflow-hidden px-4 py-6">
          <Card className="flex h-full w-full flex-col items-center justify-center gap-3 p-8">
            <div className="flex items-center justify-center text-gray-600">
              <Activity className="mr-2 h-6 w-6 animate-pulse text-gray-400" />
              <span>Loading orders...</span>
            </div>
          </Card>
        </div>
      </div>
    );
  }

  return (
    <div className="container mx-auto flex h-full flex-col">
      <div className="flex flex-1 justify-center overflow-hidden px-4 py-6">
        <Card className="flex h-full w-full flex-col gap-0 overflow-hidden">
          <div className="flex flex-wrap items-center gap-3 border-b px-6 py-4">
            <div className="flex items-center gap-3">
              <h2 className="text-gray-900">
                {orderRows.length > 0 ? (
                  <>
                    {Array.from(new Set(orderRows.map((r) => r.dealId))).length} Deal
                    {Array.from(new Set(orderRows.map((r) => r.dealId))).length === 1 ? '' : 's'} • {orderRows.length} Order
                    {orderRows.length === 1 ? '' : 's'}
                  </>
                ) : (
                  'No orders'
                )}
              </h2>
              <Button
                variant="outline"
                size="sm"
                className="h-8 px-2 text-xs"
                onClick={toggleAllDeals}
              >
                {allExpanded ? (
                  <>
                    <ChevronDown className="mr-1 h-3.5 w-3.5" />
                    Collapse All
                  </>
                ) : (
                  <>
                    <ChevronRight className="mr-1 h-3.5 w-3.5" />
                    Expand All
                  </>
                )}
              </Button>
            </div>
            <div className="ml-auto flex flex-wrap items-center justify-end gap-3 sm:flex-nowrap">
              <FilterControls
                selectedBotId={selectedBotId}
                selectedDealId={selectedDealId}
                onBotSelect={onBotSelect}
                onDealSelect={onDealSelect}
                filters={filters}
                onFiltersChange={onFiltersChange}
              />
              <Popover open={columnsPopoverOpen} onOpenChange={setColumnsPopoverOpen}>
                <PopoverTrigger asChild>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 px-2 text-xs shadow-none"
                  >
                    <SlidersHorizontal className="mr-1 h-3.5 w-3.5" />
                    Columns
                  </Button>
                </PopoverTrigger>
                <PopoverContent
                  align="end"
                  sideOffset={8}
                  className="w-48 border-gray-200 p-0"
                >
                  <div className="py-1">
                    <div className="border-b px-3 py-2 text-sm font-semibold">
                      Visible Columns
                    </div>
                    {COLUMN_ORDER.map((key) => (
                      <label
                        key={key}
                        className={`flex cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-gray-100 ${
                          REQUIRED_COLUMNS.has(key) ? 'cursor-not-allowed opacity-50' : ''
                        }`}
                      >
                        <input
                          type="checkbox"
                          checked={visibleColumns.includes(key)}
                          disabled={REQUIRED_COLUMNS.has(key)}
                          onChange={(e) => handleColumnVisibilityChange(key, e.target.checked)}
                          className="rounded border-gray-300"
                        />
                        <span>{COLUMN_LABELS[key]}</span>
                      </label>
                    ))}
                  </div>
                </PopoverContent>
              </Popover>
            </div>
          </div>

          <div className="flex flex-1 flex-col overflow-hidden">
            {rows.length === 0 ? (
              <div className="min-h-0 flex flex-1 items-center justify-center px-6 pb-6 text-gray-500">
                No orders found
              </div>
            ) : (
              <div className="flex flex-1 flex-col px-2 pb-6 sm:px-4 lg:px-6">
                <div className="lng-grid" style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
                  <div style={{ flex: 1, position: 'relative' }}>
                    <div style={{ position: 'absolute', width: '100%', height: '100%' }}>
                      <Grid.Root grid={grid}>
                        <Grid.Viewport>
                          <Grid.Header>
                            {view.header.layout.map((row, i) => (
                              <Grid.HeaderRow key={i} headerRowIndex={i}>
                                {row.map((c) => {
                                  if (c.kind === 'group') {
                                    return (
                                      <Grid.HeaderGroupCell
                                        key={c.idOccurrence}
                                        cell={c}
                                        className="flex h-full items-center justify-center px-2 text-center"
                                      />
                                    );
                                  }
                                  return (
                                    <Grid.HeaderCell
                                      key={c.column.id}
                                      cell={c}
                                      className="flex h-full items-center justify-center px-2 text-center"
                                    />
                                  );
                                })}
                              </Grid.HeaderRow>
                            ))}
                          </Grid.Header>
                          <Grid.RowsContainer>
                            <Grid.RowsCenter>
                              {view.rows.center.map((row) =>
                                row.kind === 'full-width' ? (
                                  <Grid.RowFullWidth key={row.id} row={row} />
                                ) : (
                                  <Grid.Row key={row.id} row={row}>
                                    {row.cells.map((cell) => (
                                      <Grid.Cell key={cell.id} cell={cell} />
                                    ))}
                                  </Grid.Row>
                                ),
                              )}
                            </Grid.RowsCenter>
                          </Grid.RowsContainer>
                        </Grid.Viewport>
                      </Grid.Root>
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>
        </Card>
      </div>

      {selectedOrder && (
        <OrderDetailDialog
          order={selectedOrder}
          open={detailDialogOpen}
          onOpenChange={setDetailDialogOpen}
        />
      )}

      {selectedDeal && (
        <DealDetailDialog
          deal={selectedDeal}
          open={dealDetailDialogOpen}
          onOpenChange={setDealDetailDialogOpen}
        />
      )}

      <BotDetailDialog
        bot={selectedBot}
        open={botDetailDialogOpen}
        onOpenChange={(open) => {
          setBotDetailDialogOpen(open);
          if (!open) {
            setSelectedBot(null);
          }
        }}
      />

      <AlertDialog open={cancelDialogOpen} onOpenChange={setCancelDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Cancel Order</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to cancel this order? This action cannot be undone.
              {orderToCancel && (
                <div className="mt-3 rounded bg-gray-50 p-2 text-xs">
                  <div className="font-mono text-gray-700">
                    {orderToCancel.order_id ?? orderToCancel.identifiers.hex}
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

      <AlertDialog
        open={bulkCancelDialogOpen}
        onOpenChange={(open) => {
          setBulkCancelDialogOpen(open);
          if (!open) {
            setBulkCancelContext(null);
            setIsBulkCanceling(false);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Cancel All Orders for Deal</AlertDialogTitle>
            <AlertDialogDescription>
              {`This will attempt to cancel ${bulkCancelContext?.orderIds.length ?? 0} order${(bulkCancelContext?.orderIds.length ?? 0) === 1 ? '' : 's'} for deal #${bulkCancelContext?.dealId ?? 'N/A'}.`}
              <span className="mt-3 block text-xs text-muted-foreground">
                This action cannot be undone.
              </span>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isBulkCanceling}>Keep Orders</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleConfirmBulkCancel}
              disabled={isBulkCanceling}
              className="bg-red-600 hover:bg-red-700"
            >
              {isBulkCanceling
                ? 'Canceling...'
                : `Cancel ${bulkCancelContext?.orderIds.length ?? 0} Order${(bulkCancelContext?.orderIds.length ?? 0) === 1 ? '' : 's'}`}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
