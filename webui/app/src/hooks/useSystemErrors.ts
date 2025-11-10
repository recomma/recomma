import { useEffect } from 'react';
import { toast } from 'sonner';
import { buildOpsApiUrl } from '../config/opsApi';

/**
 * Hook to listen for system events from the server and display them as toasts
 */
export function useSystemErrors() {
  useEffect(() => {
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
            duration: 10000, // Show for 10 seconds
          });
        } catch (err) {
          console.error('Failed to parse system error event:', err);
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

      eventSource.addEventListener('system_error', handleError as EventListener);
      eventSource.addEventListener('system_warn', handleWarning as EventListener);
      eventSource.addEventListener('system_info', handleInfo as EventListener);

      return () => {
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
  }, []);
}
