import { useEffect, useRef, useState } from 'react';
import type { HyperliquidBestBidOffer } from '../types/api';
import { buildOpsApiUrl } from '../config/opsApi';
import { logger } from '../utils/logger';

export function useHyperliquidPrices(coins: string[]): Map<string, HyperliquidBestBidOffer> {
  const [prices, setPrices] = useState<Map<string, HyperliquidBestBidOffer>>(new Map());
  const isMountedRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    if (coins.length === 0 || typeof window === 'undefined') {
      setPrices(new Map());
      return;
    }

    let isActive = true;
    let eventSource: EventSource | null = null;

    const params = new URLSearchParams();
    coins.forEach((coin) => {
      params.append('coin', coin);
    });

    const url = buildOpsApiUrl(`/sse/hyperliquid/prices?${params.toString()}`);

    logger.debug('[useHyperliquidPrices] Subscribing to coins:', coins);
    logger.debug('[useHyperliquidPrices] SSE URL:', url);

    try {
      eventSource = new EventSource(url, { withCredentials: true });

      eventSource.onopen = () => {
        logger.debug('[useHyperliquidPrices] SSE connection opened');
      };

      // Listen for "bbo" events (custom event type from the server)
      eventSource.addEventListener('bbo', (event: MessageEvent<string>) => {
        logger.debug('[useHyperliquidPrices] Received BBO event:', {
          data: event.data,
          type: event.type,
          isActive,
          isMounted: isMountedRef.current,
        });

        if (!isActive || !isMountedRef.current) {
          logger.debug('[useHyperliquidPrices] Ignoring BBO event - component unmounted');
          return;
        }

        try {
          const bbo: HyperliquidBestBidOffer = JSON.parse(event.data);
          logger.debug('[useHyperliquidPrices] Parsed BBO update:', bbo);

          setPrices((current) => {
            const updated = new Map(current);
            updated.set(bbo.coin, bbo);
            logger.debug('[useHyperliquidPrices] Updated prices map, size:', updated.size);
            return updated;
          });
        } catch (error) {
          logger.error('[useHyperliquidPrices] Failed to parse BBO event:', {
            error,
            rawData: event.data,
          });
        }
      });

      eventSource.onerror = () => {
        const readyState = eventSource?.readyState ?? 2;
        const readyStateText = ['CONNECTING', 'OPEN', 'CLOSED'][readyState];
        logger.error('[useHyperliquidPrices] SSE error:', {
          readyState,
          readyStateText,
          url,
          coins,
        });
        eventSource?.close();
      };
    } catch (error) {
      logger.error('[useHyperliquidPrices] Failed to establish SSE connection for prices:', error);
    }

    return () => {
      isActive = false;
      eventSource?.close();
    };
  }, [coins]);

  return prices;
}
