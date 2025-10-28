import type { CellRendererParams } from '@1771technologies/lytenyte-core/types';
import { Eye, XCircle, TrendingUp, TrendingDown } from 'lucide-react';
import { Badge } from '../../ui/badge';
import { Button } from '../../ui/button';
import { Tooltip, TooltipTrigger, TooltipContent } from '../../ui/tooltip';
import type { OrderRecord } from '../../../types/api';
import type { OrderRow, TableRow } from '../types';
import { formatPrice, formatQuantity } from '../utils/orderFormatters';
import { getOrderTypeBadge } from './statusBadges';

/**
 * Renders metadata cell with code formatting
 */
export function metadataCellRenderer(params: CellRendererParams<TableRow>) {
  const row = params.row.data;
  if (!row || row.rowType === 'deal-header') {
    return null;
  }
  return (
    <code className="text-xs px-1.5 py-0.5 rounded font-mono">
      {String(row.metadata)}
    </code>
  );
}

/**
 * Renders order type cell with badge
 */
export function orderTypeCellRenderer(params: CellRendererParams<TableRow>) {
  const row = params.row.data;
  if (!row || row.rowType === 'deal-header') {
    return null;
  }
  const value = row.orderType;
  return getOrderTypeBadge(typeof value === 'string' ? value : '-');
}

/**
 * Renders side cell (BUY/SELL) with colored badge
 */
export function sideCellRenderer(params: CellRendererParams<TableRow>) {
  const row = params.row.data;
  if (!row || row.rowType === 'deal-header') {
    return null;
  }

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

  return <span className="text-xs text-gray-600">{String(row.side)}</span>;
}

/**
 * Renders price cell with optional BBO market price comparison
 */
export function createPriceCellRenderer(bboPrices: Map<string, { bid: { price: number }; ask: { price: number } }>) {
  return function priceCellRenderer(params: CellRendererParams<TableRow>) {
    const row = params.row.data;
    if (!row || row.rowType === 'deal-header') {
      return null;
    }

    if (typeof row.price !== 'number') {
      return <span className="text-xs text-gray-400">—</span>;
    }
    const orderPrice = row.price;
    const isOpen = String(row.status).toLowerCase() === 'open';
    const bbo = bboPrices.get(String(row.coin));

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
  };
}

/**
 * Renders quantity cell
 */
export function quantityCellRenderer(params: CellRendererParams<TableRow>) {
  const row = params.row.data;
  if (!row || row.rowType === 'deal-header') {
    return null;
  }
  if (typeof row.quantity !== 'number') {
    return <span className="text-xs text-gray-400">—</span>;
  }
  return <span className="text-xs text-gray-900">{formatQuantity(row.quantity)}</span>;
}

/**
 * Renders observed timestamp cell
 */
export function observedAtCellRenderer(params: CellRendererParams<TableRow>) {
  const row = params.row.data;
  if (!row || row.rowType === 'deal-header') {
    return null;
  }
  return <span className="text-xs text-gray-600">{String(row.observedAt)}</span>;
}

/**
 * Renders status cell with colored badge
 */
export function createStatusCellRenderer(statusToneClasses: Record<string, string>) {
  return function statusCellRenderer(params: CellRendererParams<TableRow>) {
    const row = params.row.data;
    if (!row || row.rowType === 'deal-header') {
      return null;
    }
    const orderRow = row as OrderRow;
    const tone = statusToneClasses[orderRow.statusTone];
    return <Badge className={tone}>{String(orderRow.status)}</Badge>;
  };
}

/**
 * Renders history count cell
 */
export function historyCellRenderer(params: CellRendererParams<TableRow>) {
  const row = params.row.data;
  if (!row || row.rowType === 'deal-header') {
    return null;
  }
  const count = typeof row.historyCount === 'number' ? row.historyCount : 0;
  if (count === 0) {
    return <span className="text-xs text-gray-500">—</span>;
  }
  return (
    <span className="text-xs text-gray-900">
      {count} update{count === 1 ? '' : 's'}
    </span>
  );
}

/**
 * Renders actions cell with view/cancel buttons
 */
export function createActionsCellRenderer(
  onViewDetails: (order: OrderRecord) => void,
  onCancelOrder: (order: OrderRecord) => void,
) {
  return function actionsCellRenderer(params: CellRendererParams<TableRow>) {
    const row = params.row.data;
    if (!row || row.rowType === 'deal-header') {
      return null;
    }

    const orderRow = row as OrderRow;
    const isOpen = String(orderRow.status).toLowerCase() === 'open';

    return (
      <div className="flex items-center gap-1">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="sm"
              className="h-8 w-8 p-0"
              onClick={() => onViewDetails(orderRow.latest)}
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
                onClick={() => onCancelOrder(orderRow.latest)}
              >
                <XCircle className="h-4 w-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent sideOffset={5}>Cancel Order</TooltipContent>
          </Tooltip>
        )}
      </div>
    );
  };
}
