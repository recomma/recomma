import { useEffect } from 'react';
import { toast } from 'sonner';
import { buildOpsApiUrl } from '../config/opsApi';

/**
 * Hook to listen for system errors from the server and display them as toasts
 */
export function useSystemErrors() {
  useEffect(() => {
    const url = buildOpsApiUrl('/stream/orders');
    let eventSource: EventSource | null = null;

    try {
      eventSource = new EventSource(url, { withCredentials: true });

      const handleSystemError = (event: MessageEvent<string>) => {
        try {
          const data = JSON.parse(event.data);
          const errorMessage = data.error_message || data.ErrorMessage || 'An unknown system error occurred';

          toast.error('System Error', {
            description: errorMessage,
            duration: 10000, // Show for 10 seconds
          });
        } catch (err) {
          console.error('Failed to parse system error event:', err);
        }
      };

      eventSource.addEventListener('system_error', handleSystemError as EventListener);

      return () => {
        if (eventSource) {
          eventSource.removeEventListener('system_error', handleSystemError as EventListener);
          eventSource.close();
        }
      };
    } catch (err) {
      console.error('Failed to connect to system error stream:', err);
      return () => {
        if (eventSource) {
          eventSource.close();
        }
      };
    }
  }, []);
}
