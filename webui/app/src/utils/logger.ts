/**
 * Runtime-configurable logger that respects the DEBUG_LOGS flag in window.__RECOMMA_CONFIG__
 *
 * Usage:
 *   import { logger } from '../utils/logger';
 *   logger.debug('Debug message');  // Only shown if DEBUG_LOGS is true
 *   logger.error('Error message');  // Always shown
 *
 * Configuration (set in Go binary's HTML template):
 *   <script>
 *     window.__RECOMMA_CONFIG__ = {
 *       DEBUG_LOGS: true  // Set to false in production
 *     };
 *   </script>
 */

interface RecommaRuntimeConfig {
  OPS_API_ORIGIN?: string;
  DEBUG_LOGS?: boolean;
}

declare global {
  interface Window {
    __RECOMMA_CONFIG__?: RecommaRuntimeConfig;
  }
}

const isDebugEnabled = (): boolean => {
  if (typeof window === 'undefined') {
    return false;
  }
  const enabled = window.__RECOMMA_CONFIG__?.DEBUG_LOGS ?? false;

  // One-time initialization log (always shown to verify logger is loaded)
  if (!isDebugEnabled.hasLoggedInit) {
    isDebugEnabled.hasLoggedInit = true;
    console.log('[Logger] Initialized. DEBUG_LOGS =', enabled, 'Config:', window.__RECOMMA_CONFIG__);
  }

  return enabled;
};
// Static property to track initialization
(isDebugEnabled as any).hasLoggedInit = false;

export const logger = {
  /**
   * Debug logs - only shown when DEBUG_LOGS is enabled
   */
  debug: (...args: unknown[]): void => {
    if (isDebugEnabled()) {
      console.log(...args);
    }
  },

  /**
   * Info logs - only shown when DEBUG_LOGS is enabled
   */
  info: (...args: unknown[]): void => {
    if (isDebugEnabled()) {
      console.info(...args);
    }
  },

  /**
   * Warning logs - always shown (important for monitoring)
   */
  warn: (...args: unknown[]): void => {
    console.warn(...args);
  },

  /**
   * Error logs - always shown (critical for debugging production issues)
   */
  error: (...args: unknown[]): void => {
    console.error(...args);
  },
};
