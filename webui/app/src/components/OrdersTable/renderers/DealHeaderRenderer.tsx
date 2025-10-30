import type { RowFullWidthRendererParams } from '@1771technologies/lytenyte-core/types';
import { ChevronDown, ChevronRight, Activity, ExternalLink, Bot as BotIcon, AlertCircle, Eye } from 'lucide-react';
import { Badge } from '../../ui/badge';
import { Button } from '../../ui/button';
import type { HyperliquidBestBidOffer } from '../../../types/api';
import { formatPrice } from '../utils/orderFormatters';
import type { DealRecord } from '../../../types/api';
import type { DealGroupRow, TableRow } from '../types';

interface DealHeaderRendererProps {
  params: RowFullWidthRendererParams<TableRow>;
  expandedDeals: Map<string, boolean>;
  allExpanded: boolean;
  onToggleDeal: (dealId: string) => void;
  onViewDealDetails: (deal: DealRecord) => void;
  onViewBotDetails: (botId: string) => void;
  onCancelAllOrders: (metadataHashes: Set<string>, dealId: string) => void;
  bbo?: HyperliquidBestBidOffer;
}

/**
 * Renders a deal header as a full-width row
 */
export function DealHeaderRenderer({
  params,
  expandedDeals,
  allExpanded,
  onToggleDeal,
  onViewDealDetails,
  onViewBotDetails,
  onCancelAllOrders,
  bbo,
}: DealHeaderRendererProps) {
  const row = params.row.data;
  if (!row || row.rowType !== 'deal-header') {
    return null;
  }

  const dealRow = row as DealGroupRow;
  const deal = dealRow.deal;
  const dealPayload = deal?.payload;
  const isExpanded = expandedDeals.get(dealRow.dealId) ?? allExpanded;

  const dealUrl = `https://app.3commas.io/deals/${dealRow.dealId}`;
  const botUrl = `https://app.3commas.io/bots/${dealRow.botId}`;

  const pair = dealPayload?.pair || 'N/A';

  const askPrice =
    bbo && typeof bbo.ask?.price === 'number' && Number.isFinite(bbo.ask.price)
      ? bbo.ask.price
      : null;
  const bidPrice =
    bbo && typeof bbo.bid?.price === 'number' && Number.isFinite(bbo.bid.price)
      ? bbo.bid.price
      : null;

  const openExternal = (url: string) => {
    if (typeof window !== 'undefined') {
      window.open(url, '_blank', 'noopener,noreferrer');
    }
  };

  return (
    <div
      className="flex w-full items-center gap-2 py-1 px-2 cursor-pointer"
      onClick={() => onToggleDeal(dealRow.dealId)}
    >
      {isExpanded ? (
        <ChevronDown className="h-4 w-4 flex-shrink-0" />
      ) : (
        <ChevronRight className="h-4 w-4 flex-shrink-0" />
      )}

      <div className="flex w-full flex-wrap items-center gap-3 text-xs">
        <div className="flex flex-wrap items-center gap-1.5">
          <Activity className="h-3.5 w-3.5 text-purple-600" />
          <span className="font-semibold">Deal #{dealRow.dealId}</span>
          {deal && (
            <Badge className="text-xs bg-gray-100 text-gray-700 border-gray-200 px-1 py-0">
              {pair}
            </Badge>
          )}
          <Badge variant="outline" className="text-xs px-1 py-0">
            {dealRow.orderCount} orders
          </Badge>
        </div>
        {(askPrice !== null || bidPrice !== null) && (
          <div className="flex items-center gap-2 rounded-md bg-gray-50 px-2 py-1 text-[11px] text-gray-600">
            {askPrice !== null && (
              <span className="flex items-center gap-1 font-medium text-green-700">
                BUY
                <span className="font-semibold text-gray-900">${formatPrice(askPrice)}</span>
              </span>
            )}
            {bidPrice !== null && (
              <span className="flex items-center gap-1 font-medium text-red-700">
                SELL
                <span className="font-semibold text-gray-900">${formatPrice(bidPrice)}</span>
              </span>
            )}
          </div>
        )}
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          {deal && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-7 px-2 text-xs font-medium text-purple-600 hover:text-purple-700"
              onClick={(e) => {
                e.stopPropagation();
                onViewDealDetails(deal);
              }}
            >
              <Eye className="h-3.5 w-3.5" />
              Deal Details
            </Button>
          )}
          {dealRow.botId && dealRow.botId !== '—' && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-7 px-2 text-xs font-medium text-indigo-600 hover:text-indigo-700"
              onClick={(e) => {
                e.stopPropagation();
                onViewBotDetails(dealRow.botId);
              }}
            >
              <BotIcon className="h-3.5 w-3.5" />
              Bot Details
            </Button>
          )}
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-xs font-medium text-red-600 hover:text-red-700"
            onClick={(e) => {
              e.stopPropagation();
              onCancelAllOrders(dealRow.metadataHashes, dealRow.dealId);
            }}
          >
            <AlertCircle className="h-3.5 w-3.5" />
            Cancel All Orders
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-xs font-medium text-gray-600 hover:text-gray-800"
            onClick={(e) => {
              e.stopPropagation();
              openExternal(botUrl);
            }}
          >
            <BotIcon className="h-3.5 w-3.5" />
            View Bot {dealRow.botId} at 3Commas
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-xs font-medium text-blue-600 hover:text-blue-700"
            onClick={(e) => {
              e.stopPropagation();
              openExternal(dealUrl);
            }}
          >
            <ExternalLink className="h-3.5 w-3.5" />
            View Deal at 3Commas
          </Button>
        </div>
      </div>
    </div>
  );
}
