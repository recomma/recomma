import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from './ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';
import { Card } from './ui/card';
import { Badge } from './ui/badge';
import { ScrollArea } from './ui/scroll-area';
import { Separator } from './ui/separator';
import { Activity, TrendingUp, TrendingDown, ExternalLink, Bot } from 'lucide-react';
import type { DealRecord } from '../types/api';

interface DealDetailDialogProps {
  deal: DealRecord | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DealDetailDialog({ deal, open, onOpenChange }: DealDetailDialogProps) {
  if (!deal) return null;

  const dealData = deal.payload;
  const formatDate = (date: string) => {
    return new Date(date).toLocaleString();
  };

  const formatJson = (obj: unknown) => {
    return JSON.stringify(obj, null, 2);
  };

  const dealUrl = `https://app.3commas.io/deals/${deal.deal_id}`;
  const botUrl = `https://app.3commas.io/bots/${deal.bot_id}`;
  
  const profit = parseFloat(dealData.actual_profit || dealData.final_profit || '0');
  const status = dealData.status;

  const getStatusBadge = (status: string) => {
    switch (status) {
      case 'completed':
        return <Badge className="bg-green-100 text-green-800 border-green-200">Completed</Badge>;
      case 'failed':
        return <Badge className="bg-red-100 text-red-800 border-red-200">Failed</Badge>;
      case 'bought':
        return <Badge className="bg-blue-100 text-blue-800 border-blue-200">Bought</Badge>;
      case 'cancelled':
        return <Badge className="bg-gray-100 text-gray-800 border-gray-200">Cancelled</Badge>;
      default:
        return <Badge variant="outline">{status}</Badge>;
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-6xl max-h-[90vh]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            <Activity className="h-5 w-5 text-purple-600" />
            <span>Deal #{deal.deal_id}</span>
            {getStatusBadge(status)}
          </DialogTitle>
          <DialogDescription>
            View deal details, performance metrics, and trading information
          </DialogDescription>
          <div className="flex items-center gap-2 mt-2">
            <a
              href={dealUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1 text-xs text-blue-600 hover:underline"
            >
              <span>View in 3Commas</span>
              <ExternalLink className="h-3 w-3" />
            </a>
            <span className="text-xs text-gray-400">•</span>
            <a
              href={botUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1 text-xs text-blue-600 hover:underline"
            >
              <Bot className="h-3 w-3" />
              <span>Bot {deal.bot_id}</span>
            </a>
          </div>
        </DialogHeader>

        <Tabs defaultValue="overview" className="w-full">
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="raw">Raw Data</TabsTrigger>
          </TabsList>

          <ScrollArea className="h-[600px] mt-4">
            <TabsContent value="overview" className="space-y-4">
              {/* Deal Summary */}
              <Card className={`p-4 bg-gradient-to-r border ${
                profit > 0 
                  ? 'from-green-50 to-green-100 border-green-200' 
                  : profit < 0 
                  ? 'from-red-50 to-red-100 border-red-200' 
                  : 'from-purple-50 to-purple-100 border-purple-200'
              }`}>
                <h3 className="text-gray-900 mb-3">Deal Summary</h3>
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Pair</div>
                    <div className="text-gray-900">{dealData.pair || 'N/A'}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Status</div>
                    <div>{getStatusBadge(status)}</div>
                  </div>
                  <div>
                    <div className="text-xs text-gray-600 mb-1">Profit/Loss</div>
                    <div className="flex items-center gap-1">
                      {profit > 0 ? (
                        <>
                          <TrendingUp className="h-4 w-4 text-green-600" />
                          <span className="text-green-600">${profit.toFixed(2)}</span>
                        </>
                      ) : profit < 0 ? (
                        <>
                          <TrendingDown className="h-4 w-4 text-red-600" />
                          <span className="text-red-600">${profit.toFixed(2)}</span>
                        </>
                      ) : (
                        <span className="text-gray-600">$0.00</span>
                      )}
                    </div>
                  </div>
                  {dealData.profit_currency && (
                    <div>
                      <div className="text-xs text-gray-600 mb-1">Currency</div>
                      <div className="text-gray-900">{dealData.profit_currency}</div>
                    </div>
                  )}
                </div>
              </Card>

              {/* Trading Details */}
              <Card className="p-4">
                <h3 className="text-gray-900 mb-3">Trading Details</h3>
                <div className="space-y-2">
                  {dealData.bought_amount && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Bought Amount:</span>
                      <span className="text-gray-900">{dealData.bought_amount}</span>
                    </div>
                  )}
                  {dealData.bought_average_price && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Avg Buy Price:</span>
                      <span className="text-gray-900">${dealData.bought_average_price}</span>
                    </div>
                  )}
                  {dealData.sold_amount && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Sold Amount:</span>
                      <span className="text-gray-900">{dealData.sold_amount}</span>
                    </div>
                  )}
                  {dealData.sold_average_price && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Avg Sell Price:</span>
                      <span className="text-gray-900">${dealData.sold_average_price}</span>
                    </div>
                  )}
                  {dealData.base_order_volume && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Base Order Volume:</span>
                      <span className="text-gray-900">{dealData.base_order_volume}</span>
                    </div>
                  )}
                  {dealData.safety_order_volume && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Safety Order Volume:</span>
                      <span className="text-gray-900">{dealData.safety_order_volume}</span>
                    </div>
                  )}
                  {dealData.completed_safety_orders_count !== undefined && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Safety Orders Filled:</span>
                      <span className="text-gray-900">
                        {dealData.completed_safety_orders_count} / {dealData.max_safety_orders || 'N/A'}
                      </span>
                    </div>
                  )}
                </div>
              </Card>

              {/* Performance */}
              {(dealData.actual_profit_percentage || dealData.usd_final_profit) && (
                <Card className="p-4">
                  <div className="flex items-center gap-2 mb-3">
                    <TrendingUp className="h-4 w-4 text-green-600" />
                    <h3 className="text-gray-900">Performance Metrics</h3>
                  </div>
                  <div className="space-y-2">
                    {dealData.actual_profit_percentage && (
                      <div className="flex justify-between text-xs">
                        <span className="text-gray-600">Profit %:</span>
                        <span className={parseFloat(dealData.actual_profit_percentage) >= 0 ? 'text-green-600' : 'text-red-600'}>
                          {dealData.actual_profit_percentage}%
                        </span>
                      </div>
                    )}
                    {dealData.usd_final_profit && (
                      <div className="flex justify-between text-xs">
                        <span className="text-gray-600">USD Profit:</span>
                        <span className={parseFloat(dealData.usd_final_profit) >= 0 ? 'text-green-600' : 'text-red-600'}>
                          ${parseFloat(dealData.usd_final_profit).toFixed(2)}
                        </span>
                      </div>
                    )}
                  </div>
                </Card>
              )}

              {/* Timeline */}
              <Card className="p-4">
                <h3 className="text-gray-900 mb-3">Timeline</h3>
                <div className="space-y-2">
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Deal ID:</span>
                    <span className="text-gray-900">{deal.deal_id}</span>
                  </div>
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Bot ID:</span>
                    <span className="text-gray-900">{deal.bot_id}</span>
                  </div>
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Created:</span>
                    <span className="text-gray-900">{formatDate(deal.created_at)}</span>
                  </div>
                  <div className="flex justify-between text-xs">
                    <span className="text-gray-600">Updated:</span>
                    <span className="text-gray-900">{formatDate(deal.updated_at)}</span>
                  </div>
                  {dealData.closed_at && (
                    <div className="flex justify-between text-xs">
                      <span className="text-gray-600">Closed:</span>
                      <span className="text-gray-900">{formatDate(dealData.closed_at)}</span>
                    </div>
                  )}
                </div>
              </Card>
            </TabsContent>

            <TabsContent value="raw" className="space-y-4">
              <Card className="p-4">
                <div className="flex items-center gap-2 mb-3">
                  <Activity className="h-4 w-4 text-purple-600" />
                  <h3 className="text-gray-900">Deal Payload</h3>
                </div>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {formatJson(dealData)}
                  </pre>
                </ScrollArea>
              </Card>

              <Card className="p-4">
                <h3 className="text-gray-900 mb-3">Full Record</h3>
                <ScrollArea className="w-full">
                  <pre className="bg-gray-50 p-3 rounded text-xs overflow-x-auto whitespace-pre">
                    {formatJson(deal)}
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
