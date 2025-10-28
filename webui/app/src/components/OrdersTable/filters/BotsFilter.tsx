import { useState, useEffect, useRef, type MouseEvent } from 'react';
import { Button } from '../../ui/button';
import { Badge } from '../../ui/badge';
import { ScrollArea } from '../../ui/scroll-area';
import {
  Bot,
  Info,
  ExternalLink,
  X,
} from 'lucide-react';
import { BotDetailDialog } from '../../BotDetailDialog';
import type {
  BotRecord,
  ListBotsResponse,
} from '../../../types/api';
import { asRecord, coerceNumber, coerceString } from '../../../types/api';
import { buildOpsApiUrl } from '../../../config/opsApi';
import { attachOrderStreamHandlers } from '../../../utils/orderStream';

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

const describeBot = (bot: BotRecord) => {
  const payload = asRecord(bot.payload as unknown);

  const name = coerceString(payload.name, `Bot ${bot.bot_id}`);
  const isEnabled = typeof payload.is_enabled === 'boolean' ? payload.is_enabled : undefined;
  const activeDealsCount = pickNumber(0, payload.active_deals_count);
  const finishedDealsCount = pickNumber(0, payload.finished_deals_count);
  const finishedProfit = pickNumber(Number.NaN, payload.finished_deals_profit_usd);

  return {
    name,
    isEnabled,
    activeDealsCount,
    finishedDealsCount,
    finishedProfit,
  };
};

interface BotsFilterProps {
  selectedBotId?: number;
  onBotSelect: (botId: number | undefined) => void;
  onDealSelect: (dealId: number | undefined) => void;
}

export function BotsFilter({ selectedBotId, onBotSelect, onDealSelect }: BotsFilterProps) {
  const [bots, setBots] = useState<BotRecord[]>([]);
  const [botDetailDialog, setBotDetailDialog] = useState<{ open: boolean; bot: BotRecord | null }>({
    open: false,
    bot: null,
  });
  const isMountedRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    fetchBots();
  }, []);

  const fetchBots = async () => {
    try {
      const response = await fetch(buildOpsApiUrl('/api/bots?limit=100'));
      if (!response.ok) throw new Error('API not available');
      const data: ListBotsResponse = await response.json();
      setBots(data.items ?? []);
    } catch {
      setBots([]);
    }
  };

  // Set up SSE to refresh bots when order events arrive
  useEffect(() => {
    if (typeof window === 'undefined') {
      return;
    }

    let isActive = true;
    const url = buildOpsApiUrl('/sse/orders');
    let eventSource: EventSource | null = null;

    let detachHandlers: (() => void) | undefined;

    try {
      eventSource = new EventSource(url, { withCredentials: true });

      const handleOrderEvent = (_event: MessageEvent<string>) => {
        if (!isActive || !isMountedRef.current) {
          return;
        }
        void fetchBots();
      };

      detachHandlers = attachOrderStreamHandlers(eventSource, handleOrderEvent);
      eventSource.onerror = () => {
        eventSource?.close();
      };
    } catch {
      // SSE not available; continue without live updates.
    }

    return () => {
      isActive = false;
      detachHandlers?.();
      eventSource?.close();
    };
  }, []);

  const handleBotClick = (botId: number) => {
    if (selectedBotId === botId) {
      // Deselect
      onBotSelect(undefined);
      onDealSelect(undefined);
    } else {
      onBotSelect(botId);
      onDealSelect(undefined); // Clear deal selection when bot changes
    }
  };

  const selectedBot = bots.find(b => b.bot_id === selectedBotId);
  const selectedBotSummary = selectedBot ? describeBot(selectedBot) : null;

  const handleBotDetails = (event: MouseEvent<HTMLButtonElement>, bot: BotRecord) => {
    event.stopPropagation();
    setBotDetailDialog({ open: true, bot });
  };

  return (
    <>
      <BotDetailDialog
        bot={botDetailDialog.bot}
        open={botDetailDialog.open}
        onOpenChange={(open) => setBotDetailDialog({ open, bot: null })}
      />
      <div>
        <div className="p-3 border-b">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2">
              <Bot className="h-4 w-4 text-blue-600" />
              <span className="text-sm font-semibold">Select Bot</span>
              <Badge variant="secondary" className="text-xs">{bots.length}</Badge>
            </div>
            {selectedBotId && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  onBotSelect(undefined);
                  onDealSelect(undefined);
                }}
                className="h-7 px-2 text-xs"
              >
                <X className="h-3 w-3 mr-1" />
                Clear
              </Button>
            )}
          </div>
          {selectedBotId && selectedBotSummary && (
            <div className="flex items-center gap-2 text-xs text-gray-600 mt-2">
              <span className="font-medium">Selected:</span>
              <span className="truncate max-w-[300px]">{selectedBotSummary.name}</span>
              {selectedBot && (
                <div className="flex items-center gap-1 ml-auto">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={(event: MouseEvent<HTMLButtonElement>) => handleBotDetails(event, selectedBot)}
                    className="h-6 px-2 text-xs"
                  >
                    <Info className="h-3 w-3 mr-1" />
                    Details
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      window.open(`https://app.3commas.io/bots/${selectedBotId}`, '_blank');
                    }}
                    className="h-6 px-2 text-xs"
                  >
                    <ExternalLink className="h-3 w-3 mr-1" />
                    3Commas
                  </Button>
                </div>
              )}
            </div>
          )}
        </div>

        <div className="p-3">
          <ScrollArea className="h-[300px]">
            <div className="grid grid-cols-3 gap-2 pr-4">
              {bots.map((bot) => {
                const isSelected = selectedBotId === bot.bot_id;
                const summary = describeBot(bot);
                const profit = summary.finishedProfit;
                const profitIsFinite = Number.isFinite(profit);
                const isEnabled = summary.isEnabled !== undefined ? summary.isEnabled : false;

                return (
                  <div
                    key={bot.bot_id}
                    onClick={() => handleBotClick(bot.bot_id)}
                    className={`p-2 border rounded cursor-pointer transition-colors ${
                      isSelected
                        ? 'border-blue-500 bg-blue-50'
                        : 'border-gray-200 hover:border-gray-300 hover:bg-gray-50'
                    }`}
                  >
                    <div className="flex items-start justify-between mb-1">
                      <p className="text-xs text-gray-900 truncate flex-1">
                        {summary.name}
                      </p>
                      <div
                        className={`w-2 h-2 rounded-full flex-shrink-0 ml-1 mt-0.5 ${
                          isEnabled ? 'bg-green-500' : 'bg-gray-300'
                        }`}
                      />
                    </div>
                    <div className="flex items-center justify-between text-xs text-gray-500">
                      <span>{summary.activeDealsCount} active</span>
                      {profitIsFinite && (
                        <span
                          className={
                            profit > 0
                              ? 'text-green-600'
                              : profit < 0
                                ? 'text-red-600'
                                : 'text-gray-600'
                          }
                        >
                          {formatSignedNumber(profit, 2)}
                        </span>
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
