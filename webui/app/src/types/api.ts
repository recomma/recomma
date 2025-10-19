import type { components, operations } from '../../schema';

export type VaultStatus = components['schemas']['VaultStatus'];
export type VaultSetupRequest = components['schemas']['VaultSetupRequest'];
export type VaultEncryptedPayload = components['schemas']['VaultEncryptedPayload'];
export type VaultSecretsBundle = components['schemas']['VaultSecretsBundle'];
export type VaultUnsealRequest = components['schemas']['VaultUnsealRequest'];
export type VaultSecretsBundleExtended = VaultSecretsBundle & {
  not_secret: VaultSecretsBundle['not_secret'] & Record<string, unknown>;
  secrets: VaultSecretsBundle['secrets'] & Record<string, unknown>;
};

export type WebAuthnRegistrationBeginResponse =
  components['schemas']['WebAuthnRegistrationBeginResponse'];
export type WebAuthnRegistrationFinishRequest =
  components['schemas']['WebAuthnRegistrationFinishRequest'];
export type WebAuthnRegistrationFinishResponse =
  components['schemas']['WebAuthnRegistrationFinishResponse'];

export type WebAuthnLoginBeginRequest = components['schemas']['WebAuthnLoginBeginRequest'];
export type WebAuthnLoginBeginResponse = components['schemas']['WebAuthnLoginBeginResponse'];
export type WebAuthnLoginFinishRequest = components['schemas']['WebAuthnLoginFinishRequest'];
export type WebAuthnLoginFinishResponse = components['schemas']['WebAuthnLoginFinishResponse'];

export type ListBotsResponse =
  operations['listBots']['responses'][200]['content']['application/json'];
export type ListDealsResponse =
  operations['listDeals']['responses'][200]['content']['application/json'];
export type ListOrdersResponse =
  operations['listOrders']['responses'][200]['content']['application/json'];

export type ListBotsQuery = operations['listBots']['parameters']['query'];
export type ListDealsQuery = operations['listDeals']['parameters']['query'];
export type ListOrdersQuery = operations['listOrders']['parameters']['query'];

export type OrderRecord = components['schemas']['OrderRecord'];
export type OrderLogEntry = components['schemas']['OrderLogEntry'];
export type BotRecord = components['schemas']['BotRecord'];
export type DealRecord = components['schemas']['DealRecord'];

export type OrderFilterState = {
  metadata?: string;
  metadata_hex?: string;
  bot_id?: string;
  deal_id?: string;
  bot_event_id?: string;
  observed_from?: string;
  observed_to?: string;
  include_log?: string;
  limit?: string;
  page_token?: string;
};

export type UnknownRecord = Record<string, unknown>;

export const asRecord = (value: unknown): UnknownRecord =>
  (typeof value === 'object' && value !== null ? value : {}) as UnknownRecord;

export const coerceNumber = (value: unknown, fallback = 0): number => {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : fallback;
  }
  if (typeof value === 'string') {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : fallback;
  }
  return fallback;
};

export const coerceString = (value: unknown, fallback = ''): string => {
  if (typeof value === 'string') {
    return value;
  }
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value.toString();
  }
  return fallback;
};
