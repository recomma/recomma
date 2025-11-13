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
      eventSource = new EventSource(url, { withCredentials: true });

      const handleError = (event: MessageEvent<string>) => {
        try {
          const data = JSON.parse(event.data);
          const errorMessage = data.message || 'An unknown system error occurred';

          toast.error('System Error', {
            description: errorMessage,
            duration: 10000,
          });
        } catch (err) {
          console.error('[SystemError] Failed to parse event:', err);
        }
      };

      const handleWarning = (event: MessageEvent<string>) => {
        try {
          const data = JSON.parse(event.data);
          const message = data.message || 'System warning';

          toast.warning('System Warning', {
            description: message,
            duration: 5000,
          });
        } catch (err) {
          console.error('[SystemWarning] Failed to parse event:', err);
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
          console.error('[SystemInfo] Failed to parse event:', err);
        }
      };

      eventSource.onerror = (error) => {
        console.error('[SystemErrors] SSE connection error:', error);
      };

      eventSource.addEventListener('system_error', handleError as EventListener);
      eventSource.addEventListener('system_warn', handleWarning as EventListener);
      eventSource.addEventListener('system_info', handleInfo as EventListener);
    } catch (err) {
      console.error('[SystemErrors] Failed to create EventSource:', err);
    }

    return () => {
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
    };
  }, [enabled]);
}
