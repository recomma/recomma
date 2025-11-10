import { useEffect } from 'react';
import { toast } from 'sonner';
import { buildOpsApiUrl } from '../config/opsApi';

/**
 * Hook to listen for system events from the server and display them as toasts
 * @param enabled - Whether to connect to the system event stream
 */
export function useSystemErrors(enabled: boolean = true) {
  useEffect(() => {
    if (!enabled) {
      console.log('[SystemErrors] Not connecting: disabled');
      return;
    }

    // Force Sonner initialization and wait for it to be ready
    console.log('[SystemErrors] Initializing toast system...');
    toast.info('System monitoring active', { duration: 2000 });

    // EventSource reference in effect scope so cleanup can access it
    let eventSource: EventSource | null = null;

    // Wait for Sonner's toast container to exist in the DOM
    const waitForToaster = () => {
      return new Promise<void>((resolve) => {
        let checkInterval: NodeJS.Timeout | null = null;
        let timeoutHandle: NodeJS.Timeout | null = null;

        checkInterval = setInterval(() => {
          const toasterElement = document.querySelector('[data-sonner-toaster]');
          if (toasterElement) {
            console.log('[SystemErrors] Toast system ready');
            if (checkInterval) clearInterval(checkInterval);
            if (timeoutHandle) clearTimeout(timeoutHandle);
            resolve();
          }
        }, 50);

        // Timeout after 5 seconds
        timeoutHandle = setTimeout(() => {
          if (checkInterval) clearInterval(checkInterval);
          console.warn('[SystemErrors] Toast system not detected, connecting anyway');
          resolve();
        }, 5000);
      });
    };

    const initializeSSE = async () => {
      // Wait for toaster to be ready
      await waitForToaster();

      const url = buildOpsApiUrl('/stream/system');

      try {
        console.log('[SystemErrors] Creating EventSource for:', url);
        eventSource = new EventSource(url, { withCredentials: true });
        console.log('[SystemErrors] EventSource created, readyState:', eventSource.readyState);

        const handleError = (event: MessageEvent<string>) => {
          console.log('[SystemErrors] Received system_error event, raw:', event);
          try {
            const data = JSON.parse(event.data);
            const errorMessage = data.message || 'An unknown system error occurred';

            console.error('[SystemError] Parsed:', {
              source: data.source,
              message: errorMessage,
              timestamp: data.timestamp,
              details: data.details,
            });

            console.log('[SystemError] Calling toast.error()...');
            try {
              const result = toast.error('System Error', {
                description: errorMessage,
                duration: 10000, // Show for 10 seconds
              });
              console.log('[SystemError] toast.error() returned:', result);
            } catch (toastErr) {
              console.error('[SystemError] toast.error() threw:', toastErr);
            }
          } catch (err) {
            console.error('[SystemError] Failed to parse event:', err, 'raw data:', event.data);
          }
        };

        const handleWarning = (event: MessageEvent<string>) => {
          console.log('[SystemErrors] Received system_warn event, raw:', event);
          try {
            const data = JSON.parse(event.data);
            const message = data.message || 'System warning';

            console.warn('[SystemWarning] Parsed:', {
              source: data.source,
              message: message,
              timestamp: data.timestamp,
              details: data.details,
            });

            console.log('[SystemWarning] Calling toast.warning()...');
            try {
              const result = toast.warning('System Warning', {
                description: message,
                duration: 5000,
              });
              console.log('[SystemWarning] toast.warning() returned:', result);
            } catch (toastErr) {
              console.error('[SystemWarning] toast.warning() threw:', toastErr);
            }
          } catch (err) {
            console.error('[SystemWarning] Failed to parse event:', err);
          }
        };

        const handleInfo = (event: MessageEvent<string>) => {
          console.log('[SystemErrors] Received system_info event, raw:', event);
          try {
            const data = JSON.parse(event.data);
            const message = data.message || 'System info';

            toast.info(message, {
              duration: 3000,
            });
          } catch (err) {
            console.error('[SystemInfo] Failed to parse event:', err);
          }
        };

        // Generic message handler to see ALL messages
        eventSource.onmessage = (event) => {
          console.log('[SystemErrors] Received generic message:', {
            data: event.data,
            type: event.type,
            lastEventId: event.lastEventId,
          });
        };

        // Connection lifecycle handlers
        eventSource.onopen = (event) => {
          console.log('[SystemErrors] SSE connection opened:', {
            readyState: eventSource?.readyState,
            url: eventSource?.url,
            event: event
          });
        };

        eventSource.onerror = (error) => {
          console.error('[SystemErrors] SSE connection error:', {
            readyState: eventSource?.readyState,
            url: eventSource?.url,
            error: error,
          });
        };

        console.log('[SystemErrors] Adding event listeners...');
        eventSource.addEventListener('system_error', handleError as EventListener);
        console.log('[SystemErrors] Added system_error listener');
        eventSource.addEventListener('system_warn', handleWarning as EventListener);
        console.log('[SystemErrors] Added system_warn listener');
        eventSource.addEventListener('system_info', handleInfo as EventListener);
        console.log('[SystemErrors] Added system_info listener');
        console.log('[SystemErrors] All listeners registered');
      } catch (err) {
        console.error('[SystemErrors] Failed to create/configure EventSource:', err);
      }
    };

    // Start initialization (don't await - let it run in background)
    initializeSSE();

    // Cleanup function has access to eventSource in closure
    return () => {
      console.log('[SystemErrors] Cleaning up system event stream');
      if (eventSource) {
        console.log('[SystemErrors] Closing EventSource, readyState:', eventSource.readyState);
        eventSource.close();
        eventSource = null;
      }
    };
  }, [enabled]);
}
