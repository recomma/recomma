import { useEffect, useState } from 'react';
import { Card } from './ui/card';
import { ArrowUpRight, Activity, CheckCircle2, XCircle } from 'lucide-react';
import type { ListOrdersResponse, OrderRecord } from '../types/api';
import { buildOpsApiUrl } from '../config/opsApi';

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

  const classifyOrderStatus = (order: OrderRecord): 'successful' | 'failed' | 'pending' => {
    const rawStatusCandidates = [
      order?.latest_status?.status,
      order?.bot_event_payload?.Status,
      order?.bot_event_payload?.status
    ];

    let normalized =
      rawStatusCandidates
        .map((value) => (typeof value === 'string' ? value.toLowerCase() : ''))
        .find((value) => value.length > 0) || '';

    if (!normalized) {
      const action = order?.bot_event_payload?.Action;
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
  };

  useEffect(() => {
    // Fetch stats from API
    fetch(buildOpsApiUrl('/api/orders?limit=500'))
      .then(async response => {
        if (!response.ok) throw new Error('API not available');
        const data: ListOrdersResponse = await response.json();
        const orders = data.items ?? [];
        const tally = orders.reduce(
          (acc: { successful: number; failed: number; pending: number }, order: OrderRecord) => {
            const bucket = classifyOrderStatus(order);
            acc[bucket] += 1;
            return acc;
          },
          { successful: 0, failed: 0, pending: 0 }
        );

        setStats({
          total_orders: orders.length,
          successful: tally.successful,
          failed: tally.failed,
          pending: tally.pending
        });
      })
      .catch(() => {
        // Use mock data stats when API is unavailable
        setStats({
          total_orders: 3,
          successful: 1,
          failed: 1,
          pending: 1
        });
      });
  }, []);

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
