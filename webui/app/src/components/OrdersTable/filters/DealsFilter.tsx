import { useState, useEffect, useRef, type MouseEvent } from 'react';
import { Button } from '../../ui/button';
import { Badge } from '../../ui/badge';
import { ScrollArea } from '../../ui/scroll-area';
import {
  Activity,
  TrendingUp,
  TrendingDown,
  Info,
  ExternalLink,
  X,
} from 'lucide-react';
import { DealDetailDialog } from '../../DealDetailDialog';
import type {
  DealRecord,
  ListDealsResponse,
} from '../../../types/api';
import { asRecord, coerceNumber, coerceString } from '../../../types/api';
import { buildOpsApiUrl } from '../../../config/opsApi';

const pickNumber = (fallback: number, ...values: unknown[]): number => {
  for (const value of values) {
    const numeric = coerceNumber(value, Number.NaN);
    if (!Number.isNaN(numeric)) {
      return numeric;
    }
  }
  return fallback;
};

const formatSignedNumber = (value: number, fractionDigits = 2): string => {
  return value.toLocaleString('en-US', {
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  });
};

const describeDeal = (deal: DealRecord) => {
  const payload = asRecord(deal.payload as unknown);
  const pair = coerceString(payload.pair, 'N/A');
  const status = coerceString(payload.status, '');
  const profit = pickNumber(Number.NaN, payload.actual_profit, payload.final_profit);
  return { pair, status, profit };
};

interface DealsFilterProps {
  selectedBotId?: number;
  selectedDealId?: number;
  onBotSelect: (botId: number | undefined) => void;
  onDealSelect: (dealId: number | undefined) => void;
}

export function DealsFilter({ selectedBotId, selectedDealId, onBotSelect, onDealSelect }: DealsFilterProps) {
  const [deals, setDeals] = useState<DealRecord[]>([]);
  const [dealDetailDialog, setDealDetailDialog] = useState<{ open: boolean; deal: DealRecord | null }>({
    open: false,
    deal: null,
  });
  const isMountedRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    if (selectedBotId) {
      fetchDeals(selectedBotId);
    } else {
      fetchDeals();
    }
  }, [selectedBotId]);

  const fetchDeals = async (botId?: number) => {
    try {
      const params = new URLSearchParams();
      if (botId) params.append('bot_id', botId.toString());
      params.append('limit', '100');

      const response = await fetch(buildOpsApiUrl(`/api/deals?${params}`));
      if (!response.ok) throw new Error('API not available');
      const data: ListDealsResponse = await response.json();
      setDeals(data.items ?? []);
    } catch {
      setDeals([]);
    }
  };

  // Set up SSE to refresh deals when order events arrive
  useEffect(() => {
    if (typeof window === 'undefined') {
      return;
    }

    let isActive = true;
    const url = buildOpsApiUrl('/sse/orders');
    let eventSource: EventSource | null = null;

    try {
      eventSource = new EventSource(url, { withCredentials: true });

      eventSource.onmessage = () => {
        if (!isActive || !isMountedRef.current) {
          return;
        }

        if (selectedBotId) {
          void fetchDeals(selectedBotId);
        } else {
          void fetchDeals();
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
      eventSource?.close();
    };
  }, [selectedBotId]);

  const handleDealClick = (dealId: number, botId: number) => {
    if (selectedDealId === dealId) {
      // Deselect
      onDealSelect(undefined);
    } else {
      // Make sure bot is selected too
      if (selectedBotId !== botId) {
        onBotSelect(botId);
      }
      onDealSelect(dealId);
    }
  };

  const selectedDeal = deals.find(d => d.deal_id === selectedDealId);
  const filteredDeals = selectedBotId ? deals.filter(d => d.bot_id === selectedBotId) : deals;

  const handleDealDetails = (event: MouseEvent<HTMLButtonElement>, deal: DealRecord) => {
    event.stopPropagation();
    setDealDetailDialog({ open: true, deal });
  };

  return (
    <>
      <DealDetailDialog
        deal={dealDetailDialog.deal}
        open={dealDetailDialog.open}
        onOpenChange={(open) => setDealDetailDialog({ open, deal: null })}
      />
      <div>
        <div className="p-3 border-b">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2">
              <Activity className="h-4 w-4 text-purple-600" />
              <span className="text-sm font-semibold">Select Deal</span>
              <Badge variant="secondary" className="text-xs">{filteredDeals.length}</Badge>
            </div>
            {selectedDealId && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onDealSelect(undefined)}
                className="h-7 px-2 text-xs"
              >
                <X className="h-3 w-3 mr-1" />
                Clear
              </Button>
            )}
          </div>
          {selectedDealId && selectedDeal && (
            <div className="flex items-center gap-2 text-xs text-gray-600 mt-2">
              <span className="font-medium">Selected:</span>
              <span>Deal #{selectedDealId}</span>
              <div className="flex items-center gap-1 ml-auto">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={(event: MouseEvent<HTMLButtonElement>) => handleDealDetails(event, selectedDeal)}
                  className="h-6 px-2 text-xs"
                >
                  <Info className="h-3 w-3 mr-1" />
                  Details
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    window.open(`https://app.3commas.io/deals/${selectedDealId}`, '_blank');
                  }}
                  className="h-6 px-2 text-xs"
                >
                  <ExternalLink className="h-3 w-3 mr-1" />
                  3Commas
                </Button>
              </div>
            </div>
          )}
          {selectedBotId && (
            <div className="text-xs text-gray-500 mt-2">
              Showing deals for selected bot
            </div>
          )}
        </div>

        <div className="p-3">
          <ScrollArea className="h-[300px]">
            <div className="grid grid-cols-4 gap-2 pr-4">
              {filteredDeals.map((deal) => {
                const isSelected = selectedDealId === deal.deal_id;
                const summary = describeDeal(deal);
                const profit = summary.profit;
                const profitPositive = profit > 0;
                const profitNegative = profit < 0;

                return (
                  <div
                    key={deal.deal_id}
                    onClick={() => handleDealClick(deal.deal_id, deal.bot_id)}
                    className={`p-2 border rounded cursor-pointer transition-colors ${
                      isSelected
                        ? 'border-purple-500 bg-purple-50'
                        : 'border-gray-200 hover:border-gray-300 hover:bg-gray-50'
                    }`}
                  >
                    <div className="flex items-start justify-between mb-1">
                      <p className="text-xs text-gray-900">#{deal.deal_id}</p>
                      {summary.status === 'completed' && (
                        <div className="w-2 h-2 rounded-full bg-green-500 flex-shrink-0" />
                      )}
                      {summary.status === 'failed' && (
                        <div className="w-2 h-2 rounded-full bg-red-500 flex-shrink-0" />
                      )}
                      {summary.status === 'bought' && (
                        <div className="w-2 h-2 rounded-full bg-blue-500 flex-shrink-0" />
                      )}
                    </div>
                    <p className="text-xs text-gray-500 truncate mb-1">{summary.pair}</p>
                    <div className="flex items-center gap-1 text-xs">
                      {profitPositive && (
                        <>
                          <TrendingUp className="h-3 w-3 text-green-600" />
                          <span className="text-green-600">
                            {formatSignedNumber(profit, 2)}
                          </span>
                        </>
                      )}
                      {profitNegative && (
                        <>
                          <TrendingDown className="h-3 w-3 text-red-600" />
                          <span className="text-red-600">
                            {formatSignedNumber(profit, 2)}
                          </span>
                        </>
                      )}
                      {!profitPositive && !profitNegative && (
                        <span className="text-gray-600">0.00</span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </ScrollArea>
        </div>
      </div>
    </>
  );
}
