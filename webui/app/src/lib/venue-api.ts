/**
 * API client for venue management operations
 * All endpoints use the Ops API base URL with credentials
 */

import { buildOpsApiUrl } from '../config/opsApi';
import type {
  VenueRecord,
  VenueUpsertRequest,
  VenueAssignmentRecord,
  VenueAssignmentUpsertRequest,
  BotVenueAssignmentRecord,
  ListVenuesResponse,
  ListVenueAssignmentsResponse,
  ListBotVenuesResponse,
  VaultEncryptedPayload,
  VaultSecretsBundle,
} from '../types/api';

/**
 * Fetches all configured venues
 * GET /api/venues
 */
export async function fetchVenues(): Promise<VenueRecord[]> {
  const response = await fetch(buildOpsApiUrl('/api/venues'), {
    method: 'GET',
    credentials: 'include',
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to load venues');
  }

  const data: ListVenuesResponse = await response.json();
  return data.items;
}

/**
 * Creates or updates a venue
 * PUT /api/venues/{venue_id}
 */
export async function upsertVenue(
  venueId: string,
  venue: VenueUpsertRequest
): Promise<VenueRecord> {
  const response = await fetch(buildOpsApiUrl(`/api/venues/${venueId}`), {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    credentials: 'include',
    body: JSON.stringify(venue),
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to save venue');
  }

  return await response.json();
}

/**
 * Deletes a venue
 * DELETE /api/venues/{venue_id}
 */
export async function deleteVenue(venueId: string): Promise<void> {
  const response = await fetch(buildOpsApiUrl(`/api/venues/${venueId}`), {
    method: 'DELETE',
    credentials: 'include',
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to delete venue');
  }
}

/**
 * Fetches bot assignments for a venue
 * GET /api/venues/{venue_id}/assignments
 */
export async function fetchVenueAssignments(
  venueId: string
): Promise<VenueAssignmentRecord[]> {
  const response = await fetch(buildOpsApiUrl(`/api/venues/${venueId}/assignments`), {
    method: 'GET',
    credentials: 'include',
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to load venue assignments');
  }

  const data: ListVenueAssignmentsResponse = await response.json();
  return data.items;
}

/**
 * Assigns a bot to a venue
 * PUT /api/venues/{venue_id}/assignments/{bot_id}
 */
export async function assignBotToVenue(
  venueId: string,
  botId: number,
  isPrimary: boolean
): Promise<VenueAssignmentRecord> {
  const body: VenueAssignmentUpsertRequest = {
    is_primary: isPrimary,
  };

  const response = await fetch(buildOpsApiUrl(`/api/venues/${venueId}/assignments/${botId}`), {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    credentials: 'include',
    body: JSON.stringify(body),
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to assign bot to venue');
  }

  return await response.json();
}

/**
 * Removes a bot assignment from a venue
 * DELETE /api/venues/{venue_id}/assignments/{bot_id}
 */
export async function unassignBotFromVenue(venueId: string, botId: number): Promise<void> {
  const response = await fetch(buildOpsApiUrl(`/api/venues/${venueId}/assignments/${botId}`), {
    method: 'DELETE',
    credentials: 'include',
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to remove bot assignment');
  }
}

/**
 * Fetches venue assignments for a bot
 * GET /api/bots/{bot_id}/venues
 */
export async function fetchBotVenues(botId: number): Promise<BotVenueAssignmentRecord[]> {
  const response = await fetch(buildOpsApiUrl(`/api/bots/${botId}/venues`), {
    method: 'GET',
    credentials: 'include',
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to load bot venues');
  }

  const data: ListBotVenuesResponse = await response.json();
  return data.items;
}

/**
 * Fetches the encrypted vault payload
 * GET /vault/payload
 */
export async function fetchVaultPayload(): Promise<VaultEncryptedPayload> {
  const response = await fetch(buildOpsApiUrl('/vault/payload'), {
    method: 'GET',
    credentials: 'include',
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to load vault payload');
  }

  return await response.json();
}

/**
 * Updates the vault payload with new venue credentials
 * PUT /vault/payload
 */
export async function updateVaultPayload(
  encryptedPayload: VaultEncryptedPayload,
  decryptedPayload: VaultSecretsBundle
): Promise<void> {
  const response = await fetch(buildOpsApiUrl('/vault/payload'), {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    credentials: 'include',
    body: JSON.stringify({
      encrypted_payload: encryptedPayload,
      decrypted_payload: decryptedPayload,
    }),
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || 'Failed to update vault payload');
  }
}
