/**
 * Vault utilities for WebAuthn-based encryption/decryption
 * Used for updating venue credentials in the vault payload
 */

import type { VaultEncryptedPayload, VaultSecretsBundle } from '../types/api';

/**
 * Decrypts a vault payload using WebAuthn PRF
 * @param encryptedPayload - The encrypted payload from the server
 * @returns Decrypted secrets bundle
 */
export async function decryptVaultPayload(
  encryptedPayload: VaultEncryptedPayload
): Promise<VaultSecretsBundle> {
  // Get the WebAuthn credential
  const credentialRequestOptions: CredentialRequestOptions = {
    publicKey: {
      challenge: new Uint8Array(32), // Server should provide this
      rpId: window.location.hostname,
      userVerification: 'required',
      extensions: {
        prf: {
          eval: {
            first: new TextEncoder().encode('vault-decrypt'),
          },
        },
      },
    },
  };

  const credential = (await navigator.credentials.get(
    credentialRequestOptions
  )) as PublicKeyCredential | null;

  if (!credential) {
    throw new Error('WebAuthn authentication failed');
  }

  // Extract PRF output
  interface PRFExtensionResults {
    prf?: {
      results?: {
        first?: ArrayBuffer;
      };
    };
  }
  const extensions = credential.getClientExtensionResults() as PRFExtensionResults;
  const prfOutput = extensions?.prf?.results?.first;

  if (!prfOutput) {
    throw new Error('PRF extension not supported or failed');
  }

  // Decrypt the ciphertext using PRF output as key
  const key = await crypto.subtle.importKey(
    'raw',
    prfOutput,
    { name: 'AES-GCM' },
    false,
    ['decrypt']
  );

  const ciphertext = Uint8Array.from(atob(encryptedPayload.ciphertext), (c) => c.charCodeAt(0));
  const nonce = Uint8Array.from(atob(encryptedPayload.nonce), (c) => c.charCodeAt(0));

  const algorithm: AesGcmParams = {
    name: 'AES-GCM',
    iv: nonce,
  };

  if (encryptedPayload.associated_data) {
    algorithm.additionalData = Uint8Array.from(
      atob(encryptedPayload.associated_data),
      (c) => c.charCodeAt(0)
    );
  }

  const decrypted = await crypto.subtle.decrypt(algorithm, key, ciphertext);

  const plaintext = new TextDecoder().decode(decrypted);
  const bundle: VaultSecretsBundle = JSON.parse(plaintext);

  return bundle;
}

/**
 * Encrypts a vault secrets bundle using WebAuthn PRF
 * @param secretsBundle - The secrets to encrypt
 * @param username - The vault username for WebAuthn
 * @returns Encrypted payload ready to send to server
 */
export async function encryptVaultPayload(
  secretsBundle: VaultSecretsBundle,
  username: string
): Promise<VaultEncryptedPayload> {
  // Get the WebAuthn credential with PRF
  const credentialRequestOptions: CredentialRequestOptions = {
    publicKey: {
      challenge: new Uint8Array(32), // Server should provide this
      rpId: window.location.hostname,
      userVerification: 'required',
      extensions: {
        prf: {
          eval: {
            first: new TextEncoder().encode('vault-encrypt'),
          },
        },
      },
    },
  };

  const credential = (await navigator.credentials.get(
    credentialRequestOptions
  )) as PublicKeyCredential | null;

  if (!credential) {
    throw new Error('WebAuthn authentication failed');
  }

  // Extract PRF output
  interface PRFExtensionResults {
    prf?: {
      results?: {
        first?: ArrayBuffer;
      };
    };
  }
  const extensions = credential.getClientExtensionResults() as PRFExtensionResults;
  const prfOutput = extensions?.prf?.results?.first;

  if (!prfOutput) {
    throw new Error('PRF extension not supported or failed');
  }

  // Encrypt the plaintext using PRF output as key
  const key = await crypto.subtle.importKey(
    'raw',
    prfOutput,
    { name: 'AES-GCM' },
    false,
    ['encrypt']
  );

  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const plaintext = new TextEncoder().encode(JSON.stringify(secretsBundle));
  const associatedData = new TextEncoder().encode(username);

  const algorithm: AesGcmParams = {
    name: 'AES-GCM',
    iv: nonce,
    additionalData: associatedData,
  };

  const encrypted = await crypto.subtle.encrypt(algorithm, key, plaintext);

  return {
    version: 'v1',
    ciphertext: btoa(String.fromCharCode(...new Uint8Array(encrypted))),
    nonce: btoa(String.fromCharCode(...nonce)),
    associated_data: btoa(String.fromCharCode(...associatedData)),
    prf_params: null, // PRF params are stored with credentials
  };
}
