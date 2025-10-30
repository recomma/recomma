import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type { DealRecord, ListDealsResponse, ListOrdersResponse, OrderFilterState, OrderRecord } from '../../../types/api';
import { buildOpsApiUrl } from '../../../config/opsApi';
import type { FetchOrdersOptions } from '../types';
import { DEFAULT_FETCH_LIMIT } from '../constants';
import { createOrderQueryParams, extractMetadataFromEvent } from '../utils/queryParams';
import { getMetadataHex } from '../utils/orderFieldExtractors';
import { generateMockOrders } from '../mockData';
import { attachOrderStreamHandlers } from '../../../utils/orderStream';

/**
 * Custom hook to manage orders and deals data fetching with real-time updates
 */
export function useOrdersData(filters: OrderFilterState) {
  const [orders, setOrders] = useState<OrderRecord[]>([]);
  const [deals, setDeals] = useState<DealRecord[]>([]);
  const [loading, setLoading] = useState(true);

  const isMountedRef = useRef(false);
  const initialFetchPendingRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const fetchOrders = useCallback(
    async (options: FetchOrdersOptions = {}) => {
      const { showSpinner = true, signal } = options;

      if (showSpinner && isMountedRef.current) {
        setLoading(true);
      }

      const params = createOrderQueryParams(filters);
      const query = params.toString();
      const url = buildOpsApiUrl(query ? `/api/orders?${query}` : '/api/orders');

      try {
        const response = await fetch(url, {
          credentials: 'include',
          signal,
        });

        if (!response.ok) {
          throw new Error('API not available');
        }

        const data: ListOrdersResponse = await response.json();

        if (isMountedRef.current) {
          setOrders(data.items ?? []);
        }
      } catch (error) {
        if (
          (error instanceof DOMException || error instanceof Error) &&
          error.name === 'AbortError'
        ) {
          return;
        }

        if (isMountedRef.current) {
          setOrders(generateMockOrders());
        }
      } finally {
        if (showSpinner && isMountedRef.current) {
          setLoading(false);
        }
      }
    },
    [filters],
  );

  const fetchDeals = useCallback(async () => {
    try {
      const response = await fetch(buildOpsApiUrl('/api/deals?limit=1000'), {
        credentials: 'include',
      });

      if (!response.ok) {
        throw new Error('API not available');
      }

      const data: ListDealsResponse = await response.json();

      if (isMountedRef.current) {
        setDeals(data.items ?? []);
      }
    } catch {
      if (isMountedRef.current) {
        setDeals([]);
      }
    }
  }, []);

  const refreshOrdersForMetadata = useCallback(
    async (metadata: string) => {
      const params = createOrderQueryParams(filters, { includeLimit: false });
      params.set('metadata', metadata);
      params.append('limit', String(DEFAULT_FETCH_LIMIT));

      const url = buildOpsApiUrl(`/api/orders?${params.toString()}`);

      try {
        const response = await fetch(url, {
          credentials: 'include',
        });

        if (!response.ok) {
          throw new Error('API not available');
        }

        const data: ListOrdersResponse = await response.json();

        if (!isMountedRef.current) {
          return;
        }

        const updatedItems = data.items ?? [];
        const metadataKeys =
          updatedItems.length > 0
            ? new Set(updatedItems.map((order) => getMetadataHex(order)))
            : new Set([metadata]);

        setOrders((previous) => {
          const remaining = previous.filter(
            (order) => !metadataKeys.has(getMetadataHex(order)),
          );

          if (updatedItems.length === 0) {
            return remaining;
          }

          return [...remaining, ...updatedItems];
        });

        // Refresh deals when orders change
        void fetchDeals();
      } catch (error) {
        if (
          (error instanceof DOMException || error instanceof Error) &&
          error.name === 'AbortError'
        ) {
          return;
        }

        if (isMountedRef.current) {
          void fetchOrders({ showSpinner: false });
        }
      }
    },
    [fetchOrders, fetchDeals, filters],
  );

  // Setup data fetching and SSE subscription
  useEffect(() => {
    const abortController = new AbortController();
    let isActive = true;

    initialFetchPendingRef.current = true;

    void fetchOrders({ showSpinner: true, signal: abortController.signal }).finally(() => {
      initialFetchPendingRef.current = false;
    });
    void fetchDeals();

    if (typeof window === 'undefined') {
      return () => {
        isActive = false;
        abortController.abort();
      };
    }

    const params = createOrderQueryParams(filters, { includeLimit: false });
    const query = params.toString();
    const url = buildOpsApiUrl(query ? `/sse/orders?${query}` : '/sse/orders');

    let eventSource: EventSource | null = null;

    let detachHandlers: (() => void) | undefined;

    try {
      eventSource = new EventSource(url, { withCredentials: true });

      const handleOrderEvent = (event: MessageEvent<string>) => {
        if (!isActive || !isMountedRef.current) {
          return;
        }

        if (initialFetchPendingRef.current && !abortController.signal.aborted) {
          initialFetchPendingRef.current = false;
          abortController.abort();
        }

        const metadata = extractMetadataFromEvent(event.data);
        if (metadata) {
          toast.info(`Order ${metadata} updated`);
        } else {
          toast.info('Order updated in real-time');
        }

        if (metadata) {
          void refreshOrdersForMetadata(metadata);
        } else {
          void fetchOrders({ showSpinner: false });
          void fetchDeals();
        }
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
      initialFetchPendingRef.current = false;
      abortController.abort();
      detachHandlers?.();
      eventSource?.close();
    };
  }, [fetchOrders, fetchDeals, filters, refreshOrdersForMetadata]);

  return {
    orders,
    deals,
    loading,
    refreshOrdersForMetadata,
  };
}
