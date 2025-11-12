import type { VenueRecord } from '../types/api';

/**
 * Predefined API endpoints for Hyperliquid
 */
export const API_ENDPOINTS = {
  MAINNET: 'https://api.hyperliquid.xyz',
  TESTNET: 'https://api.hyperliquid-testnet.xyz',
  CUSTOM: 'custom',
} as const;

/**
 * Generates a unique venue ID from display name
 * @param displayName - Human-readable display name
 * @returns Venue ID in format "hyperliquid:{sanitized-name}"
 */
export function generateVenueId(displayName: string): string {
  const sanitized = displayName
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');

  const timestamp = Date.now();
  return `hyperliquid:${sanitized || `custom-${timestamp}`}`;
}

/**
 * Normalizes wallet address to standard format
 * @param address - Wallet address (with or without 0x prefix)
 * @returns Normalized address with 0x prefix
 */
export function normalizeWalletAddress(address: string): string {
  const trimmed = address.trim();
  return trimmed.startsWith('0x') ? trimmed.toLowerCase() : `0x${trimmed.toLowerCase()}`;
}

/**
 * Normalizes private key to standard format
 * @param privateKey - Private key (with or without 0x prefix)
 * @returns Normalized private key with 0x prefix
 */
export function normalizePrivateKey(privateKey: string): string {
  const trimmed = privateKey.trim();
  return trimmed.startsWith('0x') ? trimmed.toLowerCase() : `0x${trimmed.toLowerCase()}`;
}

/**
 * Validates display name
 * @param name - Display name to validate
 * @param existingNames - List of existing display names
 * @param currentName - Current name when editing (to allow keeping the same name)
 * @returns Error message or null if valid
 */
export function validateDisplayName(
  name: string,
  existingNames: string[] = [],
  currentName?: string
): string | null {
  const trimmed = name.trim();

  if (!trimmed) {
    return 'Display name is required';
  }

  if (trimmed.length < 1 || trimmed.length > 50) {
    return 'Display name must be 1-50 characters';
  }

  if (!/^[a-zA-Z0-9\s\-_]+$/.test(trimmed)) {
    return 'Display name can only contain letters, numbers, spaces, hyphens, and underscores';
  }

  // Check for duplicates (case-insensitive), but allow keeping the same name when editing
  if (currentName !== trimmed && existingNames.some(n => n.toLowerCase() === trimmed.toLowerCase())) {
    return 'Display name already in use';
  }

  return null;
}

/**
 * Validates wallet address format
 * @param address - Wallet address to validate
 * @returns Error message or null if valid
 */
export function validateWalletAddress(address: string): string | null {
  const trimmed = address.trim();

  if (!trimmed) {
    return 'Wallet address is required';
  }

  // Remove 0x prefix for validation
  const cleaned = trimmed.replace(/^0x/i, '');

  if (!/^[0-9a-fA-F]{40}$/.test(cleaned)) {
    return 'Invalid wallet address format. Must be 40 hex characters.';
  }

  return null;
}

/**
 * Validates private key format
 * @param privateKey - Private key to validate
 * @returns Error message or null if valid
 */
export function validatePrivateKey(privateKey: string): string | null {
  const trimmed = privateKey.trim();

  if (!trimmed) {
    return 'Private key is required';
  }

  // Remove 0x prefix for validation
  const cleaned = trimmed.replace(/^0x/i, '');

  if (!/^[0-9a-fA-F]{64}$/.test(cleaned)) {
    return 'Invalid private key format. Must be 64 hex characters.';
  }

  return null;
}

/**
 * Validates API URL
 * @param url - API URL to validate
 * @param isCustom - Whether this is a custom URL
 * @returns Error message or null if valid
 */
export function validateApiUrl(url: string, isCustom: boolean): string | null {
  if (!isCustom) {
    // Predefined URLs don't need validation
    return null;
  }

  const trimmed = url.trim();

  if (!trimmed) {
    return 'Custom API URL is required';
  }

  if (!trimmed.startsWith('https://')) {
    return 'Custom API URL must use HTTPS';
  }

  try {
    new URL(trimmed);
    return null;
  } catch {
    return 'Invalid URL format';
  }
}

/**
 * Checks if wallet address is a duplicate
 * @param address - Wallet address to check
 * @param existingVenues - List of existing venues
 * @param currentVenueId - Current venue ID when editing (to allow keeping the same wallet)
 * @returns True if duplicate, false otherwise
 */
export function isDuplicateWallet(
  address: string,
  existingVenues: VenueRecord[],
  currentVenueId?: string
): boolean {
  const normalized = normalizeWalletAddress(address);
  return existingVenues.some(
    v => v.venue_id !== currentVenueId && normalizeWalletAddress(v.wallet) === normalized
  );
}

/**
 * Truncates wallet address for display
 * @param address - Full wallet address
 * @param prefixLength - Number of characters to show at start (default 6, including 0x)
 * @param suffixLength - Number of characters to show at end (default 4)
 * @returns Truncated address like "0x1234...5678"
 */
export function truncateWalletAddress(
  address: string,
  prefixLength: number = 6,
  suffixLength: number = 4
): string {
  if (address.length <= prefixLength + suffixLength) {
    return address;
  }
  return `${address.slice(0, prefixLength)}...${address.slice(-suffixLength)}`;
}
