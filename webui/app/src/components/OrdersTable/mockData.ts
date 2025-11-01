import type { OrderRecord } from '../../types/api';

/**
 * Generates mock order data for offline/demo usage
 */
export function generateMockOrders(): OrderRecord[] {
  const now = new Date().toISOString();

  const sampleOrders: OrderRecord[] = [
    {
      order_id: 'mock-order-1',
      identifiers: {
        hex: 'mock-order-1',
        bot_id: 16541235,
        deal_id: 2380849671,
        bot_event_id: 3940649977,
        created_at: now,
      },
      observed_at: now,
      three_commas: {
        event: {
          created_at: now,
          action: 'Placing',
          coin: 'BTC',
          type: 'BUY',
          status: 'active',
          price: 65234.12,
          size: 0.15,
          order_type: 'take_profit',
          order_size: 1,
          order_position: 1,
          quote_volume: 9785.118,
          quote_currency: 'USDT',
          is_market: false,
          profit: 0,
          profit_currency: 'USDT',
          profit_percentage: 0,
          profit_usd: 0,
          text: 'Mock order generated for offline usage.',
        },
      },
      hyperliquid: {
        latest_submission: {
          kind: 'create',
          order: {
            coin: 'BTC',
            is_buy: true,
            price: 65234.12,
            size: 0.15,
            reduce_only: false,
            order_type: { limit: { tif: 'Gtc' } },
            cloid: 'mock-order-1',
          },
        },
        latest_status: {
          status: 'open',
          status_timestamp: now,
          order: {
            coin: 'BTC',
            side: 'B',
            limit_px: '65234.12',
            size: '0.15',
            orig_size: '0.15',
            timestamp: now,
            cloid: 'mock-order-1',
          },
        },
      },
      log_entries: [],
    } as unknown as OrderRecord,
    {
      order_id: 'mock-order-2',
      identifiers: {
        hex: 'mock-order-2',
        bot_id: 17551234,
        deal_id: 2380849672,
        bot_event_id: 3940649980,
        created_at: now,
      },
      observed_at: now,
      three_commas: {
        event: {
          created_at: now,
          action: 'Closing',
          coin: 'ETH',
          type: 'SELL',
          status: 'completed',
          price: 3580.45,
          size: 2.5,
          order_type: 'safety',
          order_size: 2,
          order_position: 2,
          quote_volume: 8951.125,
          quote_currency: 'USDT',
          is_market: false,
          profit: 124.55,
          profit_currency: 'USDT',
          profit_percentage: 3.6,
          profit_usd: 124.55,
          text: 'Mock closing order generated for offline usage.',
        },
      },
      hyperliquid: {
        latest_submission: {
          kind: 'create',
          order: {
            coin: 'ETH',
            is_buy: false,
            price: 3580.45,
            size: 2.5,
            reduce_only: true,
            order_type: { limit: { tif: 'Gtc' } },
            cloid: 'mock-order-2',
          },
        },
        latest_status: {
          status: 'filled',
          status_timestamp: now,
          order: {
            coin: 'ETH',
            side: 'A',
            limit_px: '3580.45',
            size: '0',
            orig_size: '2.5',
            timestamp: now,
            cloid: 'mock-order-2',
          },
        },
      },
      log_entries: [],
    } as unknown as OrderRecord,
  ];

  return sampleOrders;
}
