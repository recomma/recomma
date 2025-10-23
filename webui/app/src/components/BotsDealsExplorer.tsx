import { useState, useEffect, useRef, type MouseEvent } from 'react';
import { Card } from './ui/card';
import { Button } from './ui/button';
import { Badge } from './ui/badge';
import { ScrollArea } from './ui/scroll-area';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from './ui/collapsible';
import {
  Bot,
  TrendingUp,
  TrendingDown,
  Activity,
  ChevronRight,
  ChevronDown,
  Info,
  ExternalLink,
} from 'lucide-react';
import { BotDetailDialog } from './BotDetailDialog';
import { DealDetailDialog } from './DealDetailDialog';
import type {
  BotRecord,
  DealRecord,
  ListBotsResponse,
  ListDealsResponse,
} from '../types/api';
import { asRecord, coerceNumber, coerceString } from '../types/api';
import { buildOpsApiUrl } from '../config/opsApi';

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

const describeDeal = (deal: DealRecord) => {
  const payload = asRecord(deal.payload as unknown);
  const pair = coerceString(payload.pair, 'N/A');
  const status = coerceString(payload.status, '');
  const profit = pickNumber(Number.NaN, payload.actual_profit, payload.final_profit);
  return { pair, status, profit };
};

interface BotsDealsExplorerProps {
  onBotSelect: (botId: number | undefined) => void;
  onDealSelect: (dealId: number | undefined) => void;
  selectedBotId?: number;
  selectedDealId?: number;
}

export function BotsDealsExplorer({ onBotSelect, onDealSelect, selectedBotId, selectedDealId }: BotsDealsExplorerProps) {
  const [bots, setBots] = useState<BotRecord[]>([]);
  const [deals, setDeals] = useState<DealRecord[]>([]);
  const [isBotsOpen, setIsBotsOpen] = useState(false);
  const [isDealsOpen, setIsDealsOpen] = useState(false);
  const [botDetailDialog, setBotDetailDialog] = useState<{ open: boolean; bot: BotRecord | null }>({ open: false, bot: null });
  const [dealDetailDialog, setDealDetailDialog] = useState<{ open: boolean; deal: DealRecord | null }>({ open: false, deal: null });
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

  useEffect(() => {
    if (selectedBotId) {
      fetchDeals(selectedBotId);
    } else {
      fetchDeals();
    }
  }, [selectedBotId]);

  const fetchBots = async () => {
    try {
      const response = await fetch(buildOpsApiUrl('/api/bots?limit=100'));
      if (!response.ok) throw new Error('API not available');
      const data: ListBotsResponse = await response.json();
      setBots(data.items ?? []);
    } catch {
      setBots([]);
    } finally {
      // no-op
    }
  };

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
    } finally {
      // no-op
    }
  };

  // Set up SSE to refresh bots/deals when order events arrive
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

        // Refresh bots and deals when order updates arrive
        // This keeps the data fresh without dedicated SSE endpoints
        void fetchBots();
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

  const selectedBot = bots.find(b => b.bot_id === selectedBotId);
  const selectedDeal = deals.find(d => d.deal_id === selectedDealId);
  const selectedBotSummary = selectedBot ? describeBot(selectedBot) : null;
  const filteredDeals = selectedBotId ? deals.filter(d => d.bot_id === selectedBotId) : deals;

  const handleBotDetails = (event: MouseEvent<HTMLButtonElement>, bot: BotRecord) => {
    event.stopPropagation();
    setBotDetailDialog({ open: true, bot });
  };

  const handleDealDetails = (event: MouseEvent<HTMLButtonElement>, deal: DealRecord) => {
    event.stopPropagation();
    setDealDetailDialog({ open: true, deal });
  };

  return (
    <>
      <BotDetailDialog
        bot={botDetailDialog.bot}
        open={botDetailDialog.open}
        onOpenChange={(open) => setBotDetailDialog({ open, bot: null })}
      />
      <DealDetailDialog
        deal={dealDetailDialog.deal}
        open={dealDetailDialog.open}
        onOpenChange={(open) => setDealDetailDialog({ open, deal: null })}
      />
      <Card className="gap-0">
      <div className="divide-y">
        {/* Bots Section */}
        <Collapsible open={isBotsOpen} onOpenChange={setIsBotsOpen}>
          <div className="px-3 py-2.5">
            <CollapsibleTrigger asChild>
              <div className="flex items-center justify-between cursor-pointer">
                <div className="flex items-center gap-2">
                  <Bot className="h-4 w-4 text-blue-600" />
                  <span className="text-sm font-medium">Bots</span>
                  <Badge variant="secondary" className="text-xs">{bots.length}</Badge>
                  {selectedBotId && (
                    <>
                      <ChevronRight className="h-3 w-3 text-gray-400" />
                      <span className="text-xs text-gray-600 truncate max-w-[200px]">
                        {selectedBotSummary?.name ?? `Bot ${selectedBotId}`}
                      </span>
                    </>
                  )}
                </div>
                <div className="flex items-center gap-1.5">
                  {selectedBotId && selectedBot && (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={(event: MouseEvent<HTMLButtonElement>) => handleBotDetails(event, selectedBot)}
                        className="h-7 px-2 text-xs"
                      >
                        <Info className="h-3 w-3 mr-1" />
                        Details
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={(event: MouseEvent<HTMLButtonElement>) => {
                          event.stopPropagation();
                          window.open(`https://app.3commas.io/bots/${selectedBotId}`, '_blank');
                        }}
                        className="h-7 px-2 text-xs"
                      >
                        <ExternalLink className="h-3 w-3 mr-1" />
                        3Commas
                      </Button>
                    </>
                  )}
                  {selectedBotId && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={(event: MouseEvent<HTMLButtonElement>) => {
                        event.stopPropagation();
                        onBotSelect(undefined);
                        onDealSelect(undefined);
                      }}
                      className="h-7 px-2 text-xs"
                    >
                      Clear
                    </Button>
                  )}
                  <ChevronDown className={`h-4 w-4 text-gray-400 transition-transform ${isBotsOpen ? 'rotate-180' : ''}`} />
                </div>
              </div>
            </CollapsibleTrigger>
          </div>

          <CollapsibleContent>
            <div className="px-3 pb-3">
              <ScrollArea className="h-[120px] rounded-md border">
                <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-2 p-2">
                  {bots.map((bot) => {
                    const isSelected = selectedBotId === bot.bot_id;
                    const summary = describeBot(bot);
                    const profit = summary.finishedProfit;
                    const profitIsFinite = Number.isFinite(profit);
                    const isEnabled =
                      summary.isEnabled !== undefined ? summary.isEnabled : false;

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
          </CollapsibleContent>
        </Collapsible>

        {/* Deals Section */}
        <Collapsible open={isDealsOpen} onOpenChange={setIsDealsOpen}>
          <div className="px-3 py-2.5">
            <CollapsibleTrigger asChild>
              <div className="flex items-center justify-between cursor-pointer">
                <div className="flex items-center gap-2">
                  <Activity className="h-4 w-4 text-purple-600" />
                  <span className="text-sm font-medium">Deals</span>
                  <Badge variant="secondary" className="text-xs">{filteredDeals.length}</Badge>
                  {selectedDealId && (
                    <>
                      <ChevronRight className="h-3 w-3 text-gray-400" />
                      <span className="text-xs text-gray-600">
                        Deal #{selectedDealId}
                      </span>
                    </>
                  )}
                </div>
                <div className="flex items-center gap-1.5">
                  {selectedDealId && selectedDeal && (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={(event: MouseEvent<HTMLButtonElement>) => handleDealDetails(event, selectedDeal)}
                        className="h-7 px-2 text-xs"
                      >
                        <Info className="h-3 w-3 mr-1" />
                        Details
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={(event: MouseEvent<HTMLButtonElement>) => {
                          event.stopPropagation();
                          window.open(`https://app.3commas.io/deals/${selectedDealId}`, '_blank');
                        }}
                        className="h-7 px-2 text-xs"
                      >
                        <ExternalLink className="h-3 w-3 mr-1" />
                        3Commas
                      </Button>
                    </>
                  )}
                  {selectedDealId && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={(event: MouseEvent<HTMLButtonElement>) => {
                        event.stopPropagation();
                        onDealSelect(undefined);
                      }}
                      className="h-7 px-2 text-xs"
                    >
                      Clear
                    </Button>
                  )}
                  <ChevronDown className={`h-4 w-4 text-gray-400 transition-transform ${isDealsOpen ? 'rotate-180' : ''}`} />
                </div>
              </div>
            </CollapsibleTrigger>
          </div>

          <CollapsibleContent>
            <div className="px-3 pb-3">
              <ScrollArea className="h-[120px] rounded-md border">
                <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-2 p-2">
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
          </CollapsibleContent>
        </Collapsible>
      </div>
    </Card>
    </>
  );
}
