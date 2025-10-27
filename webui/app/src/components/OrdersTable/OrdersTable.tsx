import { useCallback, useMemo, useRef, useState } from 'react';
import type {
  CancelOrderByMetadataResponse,
  DealRecord,
  OrderFilterState,
  OrderRecord,
} from '../../types/api';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
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

// Local imports from extracted modules
import type { OrderColumnKey, OrderRow, TableRow, DealGroupRow } from './types';
import { statusToneClasses } from './types';
import { COLUMN_ORDER, COLUMN_LABELS, REQUIRED_COLUMNS, DEFAULT_VISIBLE_COLUMNS } from './constants';
import { useOrdersData } from './hooks/useOrdersData';
import { groupOrders } from './utils/orderDataTransformers';
import {
  getMetadataHex,
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
  metadataCellRenderer,
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

export interface OrdersTableProps {
  filters: OrderFilterState;
}

export function OrdersTable({ filters }: OrdersTableProps) {
  const { orders, deals, loading, refreshOrdersForMetadata } = useOrdersData(filters);

  const [selectedOrder, setSelectedOrder] = useState<OrderRecord | null>(null);
  const [selectedDeal, setSelectedDeal] = useState<DealRecord | null>(null);
  const [detailDialogOpen, setDetailDialogOpen] = useState(false);
  const [dealDetailDialogOpen, setDealDetailDialogOpen] = useState(false);
  const [visibleColumns, setVisibleColumns] =
    useState<OrderColumnKey[]>(DEFAULT_VISIBLE_COLUMNS);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [orderToCancel, setOrderToCancel] = useState<OrderRecord | null>(null);
  const [isCanceling, setIsCanceling] = useState(false);
  const [columnsDropdownOpen, setColumnsDropdownOpen] = useState(false);
  const [expandedDeals, setExpandedDeals] = useState<Map<string, boolean>>(new Map());
  const [allExpanded, setAllExpanded] = useState(true);

  const columnsButtonRef = useRef<HTMLButtonElement>(null);

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
      const metadataHex = getMetadataHex(order);
      const side = extractSide(order);
      const priceValue = getNumericPrice(order);
      const quantityValue = getNumericQuantity(order);
      const { label: statusLabel, tone: statusTone } = getStatusInfo(order);
      const coin = extractCoin(order);
      const isBuy = extractIsBuy(order);

      return {
        rowType: 'order' as const,
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
      const metadataHashes = new Set(orders.map((o) => o.metadata));

      const dealRow: DealGroupRow = {
        rowType: 'deal-header' as const,
        id: `deal-${dealId}`,
        dealId,
        botId: firstOrder?.botId ?? '—',
        deal,
        orderCount: orders.length,
        metadataHashes,
        metadata: '',
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
        coin: '',
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

  const handleCancelAllOrders = useCallback(async (metadataHashes: Set<string>, dealId: string) => {
    const hashArray = Array.from(metadataHashes);
    const totalCount = hashArray.length;

    toast.info(`Canceling ${totalCount} order${totalCount === 1 ? '' : 's'}...`, {
      description: `Deal #${dealId}`,
    });

    let successCount = 0;
    let failCount = 0;

    for (const metadata of hashArray) {
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

        successCount++;
      } catch {
        failCount++;
      }
    }

    if (successCount > 0) {
      toast.success(`Canceled ${successCount} order${successCount === 1 ? '' : 's'}`, {
        description: failCount > 0 ? `${failCount} failed` : undefined,
      });

      for (const metadata of hashArray) {
        void refreshOrdersForMetadata(metadata);
      }
    }

    if (failCount > 0 && successCount === 0) {
      toast.error('Failed to cancel orders', {
        description: `All ${failCount} cancellation attempts failed`,
      });
    }
  }, [refreshOrdersForMetadata]);

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
    (params: Parameters<typeof DealHeaderRenderer>[0]['params']) => (
      <DealHeaderRenderer
        params={params}
        expandedDeals={expandedDeals}
        allExpanded={allExpanded}
        onToggleDeal={toggleDeal}
        onViewDealDetails={viewDealDetails}
        onCancelAllOrders={handleCancelAllOrders}
      />
    ),
    [expandedDeals, allExpanded, toggleDeal, viewDealDetails, handleCancelAllOrders],
  );

  const columnDefinitions = useMemo<Record<OrderColumnKey, Column<TableRow>>>(
    () => ({
      metadata: {
        id: 'metadata',
        name: COLUMN_LABELS.metadata,
        width: 600,
        cellRenderer: metadataCellRenderer,
      },
      botId: {
        id: 'botId',
        name: COLUMN_LABELS.botId,
        width: 110,
      },
      dealId: {
        id: 'dealId',
        name: COLUMN_LABELS.dealId,
        width: 110,
      },
      orderType: {
        id: 'orderType',
        name: COLUMN_LABELS.orderType,
        width: 110,
        cellRenderer: orderTypeCellRenderer,
      },
      orderPosition: {
        id: 'orderPosition',
        name: COLUMN_LABELS.orderPosition,
        width: 80,
      },
      side: {
        id: 'side',
        name: COLUMN_LABELS.side,
        width: 100,
        cellRenderer: sideCellRenderer,
      },
      price: {
        id: 'price',
        name: COLUMN_LABELS.price,
        width: 150,
        cellRenderer: priceCellRenderer,
      },
      quantity: {
        id: 'quantity',
        name: COLUMN_LABELS.quantity,
        width: 140,
        cellRenderer: quantityCellRenderer,
      },
      observedAt: {
        id: 'observedAt',
        name: COLUMN_LABELS.observedAt,
        width: 200,
        cellRenderer: observedAtCellRenderer,
      },
      status: {
        id: 'status',
        name: COLUMN_LABELS.status,
        width: 150,
        cellRenderer: statusCellRenderer,
      },
      historyCount: {
        id: 'historyCount',
        name: COLUMN_LABELS.historyCount,
        width: 130,
        cellRenderer: historyCellRenderer,
      },
      actions: {
        id: 'actions',
        name: COLUMN_LABELS.actions,
        width: 100,
        cellRenderer: actionsCellRenderer,
      },
    }),
    [priceCellRenderer, statusCellRenderer, actionsCellRenderer],
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

  const dataSource = useClientRowDataSource({
    data: rows,
    rowIdLeaf: (d, index) => `${index}-${d.id}`,
    reflectData: true,
  });

  const grid = Grid.useLyteNyte<TableRow>({
    gridId: 'orders-table',
    columns,
    rowDataSource: dataSource,
    rowHeight: 60,
    rowFullWidthPredicate,
    rowFullWidthRenderer: dealHeaderRenderer,
  });

  const view = grid.view.useValue();

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
    <div className="flex flex-col h-full">
      <div className="px-4 py-2 border-b flex items-center justify-between flex-shrink-0 bg-white">
        <h2 className="text-gray-900">
          {orderRows.length > 0 ? (
            <>
              {Array.from(new Set(orderRows.map(r => r.dealId))).length} Deal{Array.from(new Set(orderRows.map(r => r.dealId))).length === 1 ? '' : 's'} • {orderRows.length} Order{orderRows.length === 1 ? '' : 's'}
            </>
          ) : (
            'No orders'
          )}
        </h2>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="h-8 px-2 text-xs"
            onClick={toggleAllDeals}
          >
            {allExpanded ? (
              <>
                <ChevronDown className="h-3.5 w-3.5 mr-1" />
                Collapse All
              </>
            ) : (
              <>
                <ChevronRight className="h-3.5 w-3.5 mr-1" />
                Expand All
              </>
            )}
          </Button>
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
      </div>

      {rows.length === 0 ? (
        <div className="flex flex-1 items-center justify-center text-gray-500 min-h-0">
          No orders found
        </div>
      ) : (
        <div className="lng-grid" style={{ flex: 1, display: "flex", flexDirection: "column" }}>
          <div style={{ flex: 1, position: "relative" }}>
            <div style={{ position: "absolute", width: "100%", height: "100%" }}>
              <Grid.Root grid={grid}>
                <Grid.Viewport>
                  <Grid.Header>
                    {view.header.layout.map((row, i) => (
                      <Grid.HeaderRow key={i} headerRowIndex={i}>
                        {row.map((c) => {
                          if (c.kind === 'group') {
                            return <Grid.HeaderGroupCell key={c.idOccurrence} cell={c} />;
                          }
                          return <Grid.HeaderCell key={c.column.id} cell={c} />;
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
      )}

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
