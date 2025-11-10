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
      return;
    }

    const url = buildOpsApiUrl('/stream/system');
    let eventSource: EventSource | null = null;

    try {
      console.log('[SystemErrors] Connecting to system event stream:', url);
      eventSource = new EventSource(url, { withCredentials: true });

      const handleError = (event: MessageEvent<string>) => {
        try {
          const data = JSON.parse(event.data);
          const errorMessage = data.message || 'An unknown system error occurred';

          console.error('[SystemError]', {
            source: data.source,
            message: errorMessage,
            timestamp: data.timestamp,
            details: data.details,
          });

          console.log('[SystemError] Calling toast.error...');
          try {
            const toastResult = toast.error('System Error', {
              description: errorMessage,
              duration: 10000, // Show for 10 seconds
            });
            console.log('[SystemError] toast.error returned:', toastResult);
          } catch (toastErr) {
            console.error('[SystemError] toast.error threw error:', toastErr);
          }
        } catch (err) {
          console.error('Failed to parse system error event:', err);
        }
      };

      const handleWarning = (event: MessageEvent<string>) => {
        try {
          const data = JSON.parse(event.data);
          const message = data.message || 'System warning';

          console.warn('[SystemWarning]', {
            source: data.source,
            message: message,
            timestamp: data.timestamp,
            details: data.details,
          });

          toast.warning('System Warning', {
            description: message,
            duration: 5000,
          });
        } catch (err) {
          console.error('Failed to parse system warning event:', err);
        }
      };

      const handleInfo = (event: MessageEvent<string>) => {
        try {
          const data = JSON.parse(event.data);
          const message = data.message || 'System info';

          toast.info(message, {
            duration: 3000,
          });
        } catch (err) {
          console.error('Failed to parse system info event:', err);
        }
      };

      // Connection lifecycle handlers
      eventSource.onopen = () => {
        console.log('[SystemErrors] SSE connection opened');
      };

      eventSource.onerror = (error) => {
        console.error('[SystemErrors] SSE connection error:', error);
      };

      eventSource.addEventListener('system_error', handleError as EventListener);
      eventSource.addEventListener('system_warn', handleWarning as EventListener);
      eventSource.addEventListener('system_info', handleInfo as EventListener);

      return () => {
        console.log('[SystemErrors] Disconnecting from system event stream');

        if (eventSource) {
          eventSource.removeEventListener('system_error', handleError as EventListener);
          eventSource.removeEventListener('system_warn', handleWarning as EventListener);
          eventSource.removeEventListener('system_info', handleInfo as EventListener);
          eventSource.close();
        }
      };
    } catch (err) {
      console.error('Failed to connect to system event stream:', err);
      return () => {
        if (eventSource) {
          eventSource.close();
        }
      };
    }
  }, [enabled]);
}
