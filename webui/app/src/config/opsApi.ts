interface RecommaRuntimeConfig {
  OPS_API_ORIGIN?: string;
  DEBUG_LOGS?: boolean;
}

declare global {
  interface Window {
    __RECOMMA_CONFIG__?: RecommaRuntimeConfig;
  }
}

const FALLBACK_OPS_API_ORIGIN = 'http://localhost:8080';

const getRuntimeInjectedOrigin = (): string | undefined => {
  if (typeof window === 'undefined') {
    return undefined;
  }
  return window.__RECOMMA_CONFIG__?.OPS_API_ORIGIN;
};

const getEnvironmentOrigin = (): string | undefined => {
  const origin = import.meta.env.VITE_OPS_API_ORIGIN;
  if (typeof origin === 'string' && origin.trim().length > 0) {
    return origin;
  }
  return undefined;
};

const getWindowOrigin = (): string | undefined => {
  if (typeof window === 'undefined') {
    return undefined;
  }
  if (typeof window.location?.origin === 'string' && window.location.origin.length > 0) {
    return window.location.origin;
  }
  return undefined;
};

export const getOpsApiBaseUrl = (): string => {
  return (
    getRuntimeInjectedOrigin() ??
    getEnvironmentOrigin() ??
    getWindowOrigin() ??
    FALLBACK_OPS_API_ORIGIN
  );
};

export const buildOpsApiUrl = (path: string): string => {
  const base = getOpsApiBaseUrl().replace(/\/$/, '');
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return `${base}${normalizedPath}`;
};

