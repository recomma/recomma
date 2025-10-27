import { Badge } from '../../ui/badge';

/**
 * Returns a styled badge component for an order type
 */
export const getOrderTypeBadge = (orderType: string) => {
  if (orderType === '-') {
    return <span className="text-xs text-gray-400">-</span>;
  }

  const normalized = orderType.toLowerCase();

  if (normalized === 'base') {
    return (
      <Badge className="bg-blue-100 text-blue-800 border-blue-200 text-xs">Base</Badge>
    );
  }

  if (normalized === 'take profit') {
    return (
      <Badge className="bg-green-100 text-green-800 border-green-200 text-xs">
        Take Profit
      </Badge>
    );
  }

  if (normalized === 'safety') {
    return (
      <Badge className="bg-orange-100 text-orange-800 border-orange-200 text-xs">
        Safety
      </Badge>
    );
  }

  if (normalized === 'manual safety') {
    return (
      <Badge className="bg-amber-100 text-amber-800 border-amber-200 text-xs">
        Manual Safety
      </Badge>
    );
  }

  if (normalized === 'stop loss') {
    return (
      <Badge className="bg-red-100 text-red-800 border-red-200 text-xs">Stop Loss</Badge>
    );
  }

  return <Badge variant="outline" className="text-xs">{orderType}</Badge>;
};
