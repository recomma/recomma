import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from './ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';
import { Card } from './ui/card';
import { Badge } from './ui/badge';
import { ScrollArea } from './ui/scroll-area';
import { Separator } from './ui/separator';
import { Bot, TrendingUp, Activity, ExternalLink } from 'lucide-react';
import type { BotRecord } from '../types/api';

interface BotDetailDialogProps {
  bot: BotRecord | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function BotDetailDialog({ bot, open, onOpenChange }: BotDetailDialogProps) {
  if (!bot) return null;

  const botData = bot.payload;
  const toNumber = (value: unknown): number => {
    if (typeof value === 'number') {
      return Number.isFinite(value) ? value : 0;
    }
    if (typeof value === 'string') {
      const parsed = Number(value);
      return Number.isFinite(parsed) ? parsed : 0;
    }
    return 0;
  };
  const finishedDealsCount = toNumber(botData.finished_deals_count);
  const activeDealsCount = toNumber(botData.active_deals_count);
  const finishedDealsProfit = toNumber(botData.finished_deals_profit_usd);
  const hasDeals = finishedDealsCount > 0 || activeDealsCount > 0;
  const hasProfit = Number.isFinite(finishedDealsProfit);
  const formatDate = (date: string) => {
    return new Date(date).toLocaleString();
  };

  const formatJson = (obj: unknown) => {
    return JSON.stringify(obj, null, 2);
  };

  const botUrl = `https://app.3commas.io/bots/${bot.bot_id}`;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-6xl max-h-[90vh]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            <Bot className="h-5 w-5 text-blue-600" />
            <span>{botData.name || `Bot ${bot.bot_id}`}</span>
            {botData.is_enabled ? (
              <Badge className="bg-green-100 text-green-800 border-green-200">Active</Badge>
            ) : (
              <Badge variant="outline">Inactive</Badge>
            )}
          </DialogTitle>
          <DialogDescription>
            View bot configuration, performance metrics, and settings
          </DialogDescription>
          <div className="flex items-center gap-2 mt-2">
            <a
              href={botUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1 text-xs text-blue-600 hover:underline"
            >
              <span>View in 3Commas</span>
              <ExternalLink className="h-3 w-3" />
            </a>
            <span className="text-xs text-gray-400">•</span>
            <code className="text-xs bg-gray-100 px-2 py-1 rounded">Bot ID: {bot.bot_id}</code>
          </div>
        </DialogHeader>

        <Tabs defaultValue="overview" className="w-full">
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="raw">Raw Data</TabsTrigger>
          </TabsList>

          <ScrollArea className="h-[600px] mt-4">
            <TabsContent value="overview" className="space-y-4">
              {/* Main Info */}
              <Card className="p-4 bg-gradient-to-r from-blue-50 to-blue-100 border-blue-200">
                <div className="flex items-start justify-between mb-4">
                  <h3 className="text-gray-900">Bot Configuration</h3>
                  {botData.is_enabled ? (
                    <Badge className="bg-green-100 text-green-800 border-green-200">Active</Badge>
                  ) : (
                    <Badge variant="outline">Inactive</Badge>
                  )}
                </div>
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Account</div>
                    <div className="text-gray-900">{botData.account_name || 'N/A'}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Strategy</div>
                    <div className="text-gray-900">{botData.strategy || 'N/A'}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Max Active Deals</div>
                    <div className="text-gray-900">{botData.max_active_deals || 'N/A'}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Base Order Volume</div>
                    <div className="text-gray-900">{botData.base_order_volume || 'N/A'} {botData.base_order_volume_type || ''}</div>
                  </div>
                </div>

                <Separator className="my-3" />

                <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Active Deals</div>
                    <div className="text-gray-900">
                      {botData.active_deals_count || 0} / {botData.max_active_deals || 0}
                    </div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Finished Deals</div>
                    <div className="text-gray-900">{botData.finished_deals_count || 0}</div>
                  </div>
                  {botData.finished_deals_profit_usd && (
                    <div>
                      <div className="text-xs text-gray-600 mb-1">Total Profit</div>
                      <div className={parseFloat(botData.finished_deals_profit_usd) >= 0 ? 'text-green-600' : 'text-red-600'}>
                        ${parseFloat(botData.finished_deals_profit_usd).toFixed(2)}
                      </div>
                    </div>
                  )}
                </div>
              </Card>

              {/* Trading Pairs */}
              {botData.pairs && botData.pairs.length > 0 && (
                <Card className="p-4">
                  <div className="flex items-center gap-2 mb-3">
                    <Activity className="h-4 w-4 text-purple-600" />
                    <h3 className="text-gray-900">Trading Pairs</h3>
                    <Badge variant="secondary" className="text-xs">{botData.pairs.length}</Badge>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {botData.pairs.map((pair: string, index: number) => (
                      <Badge key={index} variant="outline" className="text-xs">
                        {pair}
                      </Badge>
                    ))}
                  </div>
                </Card>
              )}

              {/* Performance Summary */}
              {hasDeals && (
                <Card className="p-4">
                  <div className="flex items-center gap-2 mb-3">
                    <TrendingUp className="h-4 w-4 text-green-600" />
                    <h3 className="text-gray-900">Performance Summary</h3>
                  </div>
                  <div className="space-y-3">
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Total Deals:</span>
                      <span className="text-gray-900">
                        {finishedDealsCount + activeDealsCount}
                      </span>
                    </div>
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Completed:</span>
                      <span className="text-gray-900">{finishedDealsCount}</span>
                    </div>
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">In Progress:</span>
                      <span className="text-gray-900">{activeDealsCount}</span>
                    </div>
                    {hasProfit && (
                      <>
                        <Separator />
                        <div className="flex justify-between text-xs">
                          <span className="text-gray-600">Total P&L:</span>
                          <span className={finishedDealsProfit >= 0 ? 'text-green-600' : 'text-red-600'}>
                            ${finishedDealsProfit.toFixed(2)}
                          </span>
                        </div>
                        {finishedDealsCount > 0 && (
                          <div className="flex justify-between text-xs">
                            <span className="text-gray-600">Avg Profit per Deal:</span>
                            <span className={finishedDealsProfit >= 0 ? 'text-green-600' : 'text-red-600'}>
                              ${(finishedDealsProfit / finishedDealsCount).toFixed(2)}
                            </span>
                          </div>
                        )}
                      </>
                    )}
                  </div>
                </Card>
              )}

              {/* Metadata */}
              <Card className="p-4">
                <h3 className="text-gray-900 mb-3">Metadata</h3>
                <div className="space-y-2">
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Bot ID:</span>
                    <span className="text-gray-900">{bot.bot_id}</span>
                  </div>
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Last Synced:</span>
                    <span className="text-gray-900">{formatDate(bot.last_synced_at)}</span>
                  </div>
                </div>
              </Card>
            </TabsContent>

            <TabsContent value="raw" className="space-y-4">
              <Card className="p-4">
                <div className="flex items-center gap-2 mb-3">
                  <Bot className="h-4 w-4 text-blue-600" />
                  <h3 className="text-gray-900">Bot Payload</h3>
                </div>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {formatJson(botData)}
                  </pre>
                </ScrollArea>
              </Card>

              <Card className="p-4">
                <h3 className="text-gray-900 mb-3">Full Record</h3>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {formatJson(bot)}
                  </pre>
                </ScrollArea>
              </Card>
            </TabsContent>
          </ScrollArea>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
