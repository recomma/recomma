import type { RowFullWidthRendererParams } from '@1771technologies/lytenyte-core/types';
import { ChevronDown, ChevronRight, TrendingUp, TrendingDown, Activity, ExternalLink, Bot as BotIcon, AlertCircle, Eye } from 'lucide-react';
import { Badge } from '../../ui/badge';
import type { DealRecord } from '../../../types/api';
import type { DealGroupRow, TableRow } from '../types';

interface DealHeaderRendererProps {
  params: RowFullWidthRendererParams<TableRow>;
  expandedDeals: Map<string, boolean>;
  allExpanded: boolean;
  onToggleDeal: (dealId: string) => void;
  onViewDealDetails: (deal: DealRecord) => void;
  onCancelAllOrders: (metadataHashes: Set<string>, dealId: string) => void;
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
  onCancelAllOrders,
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

  const profit = deal ? parseFloat(String(dealPayload?.actual_profit || dealPayload?.final_profit || '0')) : 0;
  const status = dealPayload?.status || 'unknown';
  const pair = dealPayload?.pair || 'N/A';

  return (
    <div
      className="flex items-center gap-2 py-1 px-2 bg-gradient-to-r from-purple-50 to-blue-50 border-l-4 border-purple-400 cursor-pointer hover:from-purple-100 hover:to-blue-100"
      onClick={() => onToggleDeal(dealRow.dealId)}
      style={{ minHeight: '60px' }}
    >
      {isExpanded ? (
        <ChevronDown className="h-4 w-4 flex-shrink-0" />
      ) : (
        <ChevronRight className="h-4 w-4 flex-shrink-0" />
      )}

      <div className="flex items-center gap-1.5 flex-wrap text-xs">
        <Activity className="h-3.5 w-3.5 text-purple-600" />
        <span className="font-semibold">Deal #{dealRow.dealId}</span>
        {deal && (
          <>
            <Badge className="text-xs bg-gray-100 text-gray-700 border-gray-200 px-1 py-0">
              {pair}
            </Badge>
            <Badge className={`text-xs px-1 py-0 ${
              status === 'completed' ? 'bg-green-100 text-green-800' :
              status === 'failed' ? 'bg-red-100 text-red-800' :
              status === 'bought' ? 'bg-blue-100 text-blue-800' :
              'bg-gray-100 text-gray-600'
            }`}>
              {status}
            </Badge>
            {profit !== 0 && (
              <span className={`font-medium ${profit > 0 ? 'text-green-600' : 'text-red-600'}`}>
                {profit > 0 ? <TrendingUp className="h-3 w-3 inline" /> : <TrendingDown className="h-3 w-3 inline" />}
                ${Math.abs(profit).toFixed(2)}
              </span>
            )}
          </>
        )}
        <Badge variant="outline" className="text-xs px-1 py-0">
          {dealRow.orderCount} orders
        </Badge>
        <span className="text-gray-400">•</span>
        <a
          href={botUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="text-gray-600 hover:underline flex items-center gap-0.5"
          onClick={(e) => e.stopPropagation()}
        >
          <BotIcon className="h-3 w-3" />
          Bot {dealRow.botId}
        </a>
        <span className="text-gray-400">•</span>
        <a
          href={dealUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="text-blue-600 hover:underline flex items-center gap-0.5"
          onClick={(e) => e.stopPropagation()}
        >
          <ExternalLink className="h-3 w-3" />
          3Commas
        </a>
        {deal && (
          <>
            <span className="text-gray-400">•</span>
            <button
              className="text-purple-600 hover:underline"
              onClick={(e) => {
                e.stopPropagation();
                onViewDealDetails(deal);
              }}
            >
              <Eye className="h-3 w-3 inline mr-0.5" />
              Details
            </button>
          </>
        )}
        <span className="text-gray-400">•</span>
        <button
          className="text-red-600 hover:underline"
          onClick={(e) => {
            e.stopPropagation();
            onCancelAllOrders(dealRow.metadataHashes, dealRow.dealId);
          }}
        >
          <AlertCircle className="h-3 w-3 inline mr-0.5" />
          Cancel All
        </button>
      </div>
    </div>
  );
}
