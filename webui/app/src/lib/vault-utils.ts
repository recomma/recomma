/**
 * Vault utilities for AES-GCM encryption/decryption
 * Used for updating venue credentials in the vault payload
 *
 * Note: Despite the field name "prf_params", this implementation uses standard
 * AES-GCM encryption with a key stored in the vault payload, not WebAuthn PRF.
 */

import type { VaultEncryptedPayload, VaultSecretsBundle } from '../types/api';

/**
 * Converts base64url string to Uint8Array
 */
const base64UrlToUint8Array = (value: string): Uint8Array => {
  const padding = '='.repeat((4 - (value.length % 4)) % 4);
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/') + padding;
  const decoded = atob(base64);
  const bytes = new Uint8Array(decoded.length);
  for (let i = 0; i < decoded.length; i += 1) {
    bytes[i] = decoded.charCodeAt(i);
  }
  return bytes;
};

/**
 * Converts Uint8Array to base64url string
 */
const uint8ArrayToBase64Url = (value: Uint8Array | ArrayBuffer): string => {
  const bytes = value instanceof ArrayBuffer ? new Uint8Array(value) : value;
  let binary = '';
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  const base64 = btoa(binary);
  return base64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/u, '');
};

/**
 * Decrypts a vault payload using the AES-GCM key stored in prf_params
 * @param encryptedPayload - The encrypted payload from the server
 * @returns Decrypted secrets bundle
 */
export async function decryptVaultPayload(
  encryptedPayload: VaultEncryptedPayload
): Promise<VaultSecretsBundle> {
  if (encryptedPayload.version !== 'aes-gcm-v1') {
    throw new Error('Unsupported payload version returned by server.');
  }

  const subtle = globalThis.crypto?.subtle;
  if (!subtle) {
    throw new Error('Browser does not support required cryptography APIs.');
  }

  // Extract encryption key from prf_params
  const prfParams = (encryptedPayload.prf_params ?? undefined) as Record<string, unknown> | undefined;
  const keyBase64 = typeof prfParams?.key === 'string' ? prfParams.key : null;
  if (!keyBase64) {
    throw new Error('Encrypted payload is missing the key material.');
  }

  // Decode payload components
  const ciphertext = base64UrlToUint8Array(encryptedPayload.ciphertext);
  const nonce = base64UrlToUint8Array(encryptedPayload.nonce);
  const associatedDataValue = typeof encryptedPayload.associated_data === 'string'
    ? encryptedPayload.associated_data
    : null;
  const additionalData = associatedDataValue ? base64UrlToUint8Array(associatedDataValue) : undefined;

  // Import the encryption key
  const keyBytes = base64UrlToUint8Array(keyBase64);
  const key = await subtle.importKey(
    'raw',
    keyBytes,
    { name: 'AES-GCM' },
    false,
    ['decrypt'],
  );

  // Decrypt
  const decryptParams: AesGcmParams = { name: 'AES-GCM', iv: nonce };
  if (additionalData) {
    decryptParams.additionalData = additionalData;
  }

  const decrypted = await subtle.decrypt(decryptParams, key, ciphertext);

  // Parse decrypted JSON
  const decoder = new TextDecoder();
  const bundleJson = decoder.decode(decrypted);
  const bundle = JSON.parse(bundleJson) as VaultSecretsBundle;

  if (!bundle?.not_secret || !bundle?.secrets) {
    throw new Error('Decrypted payload is incomplete.');
  }

  return bundle;
}

/**
 * Encrypts a vault secrets bundle using the same key from the original payload
 * @param secretsBundle - The secrets to encrypt
 * @param username - The vault username for associated data
 * @param originalPayload - The original encrypted payload (to reuse the encryption key)
 * @returns Encrypted payload ready to send to server
 */
export async function encryptVaultPayload(
  secretsBundle: VaultSecretsBundle,
  username: string,
  originalPayload: VaultEncryptedPayload
): Promise<VaultEncryptedPayload> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) {
    throw new Error('Browser does not support required cryptography APIs.');
  }

  // Extract the existing encryption key from the original payload
  const prfParams = (originalPayload.prf_params ?? undefined) as Record<string, unknown> | undefined;
  const keyBase64 = typeof prfParams?.key === 'string' ? prfParams.key : null;
  if (!keyBase64) {
    throw new Error('Original payload is missing the key material.');
  }

  // Import the encryption key
  const keyBytes = base64UrlToUint8Array(keyBase64);
  const key = await subtle.importKey(
    'raw',
    keyBytes,
    { name: 'AES-GCM' },
    false,
    ['encrypt'],
  );

  // Generate new nonce and prepare data
  const encoder = new TextEncoder();
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const plaintext = encoder.encode(JSON.stringify(secretsBundle));
  const associatedDataBytes = encoder.encode(JSON.stringify(secretsBundle.not_secret));

  // Encrypt
  const encryptParams: AesGcmParams = {
    name: 'AES-GCM',
    iv: nonce,
    additionalData: associatedDataBytes,
  };

  const encrypted = await subtle.encrypt(encryptParams, key, plaintext);

  // Return new encrypted payload with the same key
  return {
    version: 'aes-gcm-v1',
    ciphertext: uint8ArrayToBase64Url(encrypted),
    nonce: uint8ArrayToBase64Url(nonce),
    associated_data: uint8ArrayToBase64Url(associatedDataBytes),
    prf_params: originalPayload.prf_params, // Reuse the same key
  };
}
