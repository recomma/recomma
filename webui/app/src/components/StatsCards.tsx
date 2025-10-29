import { useCallback, useEffect, useRef, useState } from 'react';
import { Card } from './ui/card';
import { ArrowUpRight, Activity, CheckCircle2, XCircle } from 'lucide-react';
import type { ListOrdersResponse, OrderRecord } from '../types/api';
import { buildOpsApiUrl } from '../config/opsApi';
import { attachOrderStreamHandlers } from '../utils/orderStream';

interface Stats {
  total_orders: number;
  successful: number;
  failed: number;
  pending: number;
}

export function StatsCards() {
  const [stats, setStats] = useState<Stats>({
    total_orders: 0,
    successful: 0,
    failed: 0,
    pending: 0
  });
  const isMountedRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const classifyOrderStatus = useCallback((order: OrderRecord): 'successful' | 'failed' | 'pending' => {
    const statusCandidates = [
      order?.hyperliquid?.latest_status?.status,
      order?.three_commas?.event?.status
    ];

    let normalized =
      statusCandidates
        .map((value) => (typeof value === 'string' ? value.toLowerCase() : ''))
        .find((value) => value.length > 0) || '';

    if (!normalized && order?.hyperliquid?.latest_submission?.kind === 'cancel') {
      normalized = 'cancelled';
    }

    if (!normalized) {
      const action = order?.three_commas?.event?.action;
      if (typeof action === 'string' && action.toLowerCase().includes('cancel')) {
        normalized = 'cancelled';
      }
    }

    if (['filled', 'completed', 'executed', 'success', 'successful'].includes(normalized)) {
      return 'successful';
    }

    if (['failed', 'error', 'cancelled', 'canceled', 'rejected'].includes(normalized)) {
      return 'failed';
    }

    return 'pending';
  }, []);

  const calculateStats = useCallback((orders: OrderRecord[]) => {
    const tally = orders.reduce(
      (acc: { successful: number; failed: number; pending: number }, order: OrderRecord) => {
        const bucket = classifyOrderStatus(order);
        acc[bucket] += 1;
        return acc;
      },
      { successful: 0, failed: 0, pending: 0 }
    );

    return {
      total_orders: orders.length,
      successful: tally.successful,
      failed: tally.failed,
      pending: tally.pending
    };
  }, [classifyOrderStatus]);

  const fetchStats = useCallback(async () => {
    try {
      const response = await fetch(buildOpsApiUrl('/api/orders?limit=500'));
      if (!response.ok) throw new Error('API not available');
      const data: ListOrdersResponse = await response.json();
      const orders = data.items ?? [];

      if (isMountedRef.current) {
        setStats(calculateStats(orders));
      }
    } catch {
      if (isMountedRef.current) {
        // Use mock data stats when API is unavailable
        setStats({
          total_orders: 3,
          successful: 1,
          failed: 1,
          pending: 1
        });
      }
    }
  }, [calculateStats]);

  useEffect(() => {
    let isActive = true;

    // Initial fetch
    void fetchStats();

    if (typeof window === 'undefined') {
      return () => {
        isActive = false;
      };
    }

    // Set up SSE for real-time updates
    const url = buildOpsApiUrl('/sse/orders');
    let eventSource: EventSource | null = null;

    let detachHandlers: (() => void) | undefined;

    try {
      eventSource = new EventSource(url, { withCredentials: true });

      const handleOrderEvent = (_event: MessageEvent<string>) => {
        void _event;
        if (!isActive || !isMountedRef.current) {
          return;
        }
        // Refetch stats when order updates arrive
        void fetchStats();
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
  }, [fetchStats]);

  const cards = [
    {
      title: 'Total Orders',
      value: stats.total_orders,
      icon: Activity,
      color: 'text-blue-600',
      bgColor: 'bg-blue-50'
    },
    {
      title: 'Successful',
      value: stats.successful,
      icon: CheckCircle2,
      color: 'text-green-600',
      bgColor: 'bg-green-50'
    },
    {
      title: 'Failed',
      value: stats.failed,
      icon: XCircle,
      color: 'text-red-600',
      bgColor: 'bg-red-50'
    },
    {
      title: 'Pending',
      value: stats.pending,
      icon: ArrowUpRight,
      color: 'text-yellow-600',
      bgColor: 'bg-yellow-50'
    }
  ];

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
      {cards.map((card) => {
        const Icon = card.icon;
        return (
          <Card key={card.title} className="p-3">
            <div className="flex items-start justify-between">
              <div>
                <p className="text-xs text-gray-600">{card.title}</p>
                <p className="text-gray-900 mt-1">{card.value}</p>
              </div>
              <div className={`p-2 rounded-lg ${card.bgColor}`}>
                <Icon className={`h-4 w-4 ${card.color}`} />
              </div>
            </div>
          </Card>
        );
      })}
    </div>
  );
}
