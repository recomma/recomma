import { useEffect, useState } from 'react';

/**
 * Hook to detect when the Sonner Toaster component is mounted and ready
 * Checks for the existence of the toast container DOM element
 */
export function useToasterReady(): boolean {
  const [isReady, setIsReady] = useState(false);

  useEffect(() => {
    // Check if Sonner's toast container exists in the DOM
    const checkToasterReady = () => {
      // Sonner creates an element with data-sonner-toaster attribute
      const toasterElement = document.querySelector('[data-sonner-toaster]');

      if (toasterElement && !isReady) {
        console.log('[ToasterReady] Toaster component is mounted and ready');
        setIsReady(true);
      }
    };

    // Initial check
    checkToasterReady();

    // If not ready yet, poll until it is
    if (!isReady) {
      const intervalId = setInterval(checkToasterReady, 100);

      // Stop polling after 5 seconds max
      const timeoutId = setTimeout(() => {
        clearInterval(intervalId);
        if (!isReady) {
          console.warn('[ToasterReady] Toaster not detected after 5 seconds, giving up');
        }
      }, 5000);

      return () => {
        clearInterval(intervalId);
        clearTimeout(timeoutId);
      };
    }
  }, [isReady]);

  return isReady;
}
