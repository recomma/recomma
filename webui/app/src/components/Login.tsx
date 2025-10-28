import { useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import { AlertCircle, KeyRound } from 'lucide-react';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card';
import { Button } from './ui/button';
import { Input } from './ui/input';
import type {
  VaultEncryptedPayload,
  VaultSecretsBundleExtended,
  VaultUnsealRequest,
  WebAuthnLoginBeginResponse,
  WebAuthnLoginFinishRequest,
} from '../types/api';
import { buildOpsApiUrl } from '../config/opsApi';

interface LoginProps {
  initialUsername?: string;
  onAuthenticated?: () => void;
}

type ProgressStep =
  | 'idle'
  | 'begin'
  | 'prompt'
  | 'verifying'
  | 'fetchingPayload'
  | 'decrypting'
  | 'unsealing'
  | 'done';

class LoginStageError extends Error {
  constructor(public stage: ProgressStep, message: string, public cause?: unknown) {
    super(message);
    this.name = 'LoginStageError';
  }
}

const raise = (stage: ProgressStep, fallback: string, detail?: string | null, cause?: unknown): never => {
  const message = detail && detail.trim().length > 0 ? detail : fallback;
  throw new LoginStageError(stage, message, cause);
};

function requireValue<T>(
  value: T | null | undefined,
  stage: ProgressStep,
  fallback: string,
  detail?: string,
): T {
  if (value === null || value === undefined) {
    raise(stage, fallback, detail);
  }
  return value as T;
}

const encode = (input: Uint8Array) => {
  const chunkSize = 0x8000;
  let binary = '';
  for (let i = 0; i < input.length; i += chunkSize) {
    binary += String.fromCharCode(...input.subarray(i, i + chunkSize));
  }
  const btoaFn = globalThis.btoa;
  if (!btoaFn) {
    throw new Error('Base64 encoding is not supported in this environment.');
  }
  return btoaFn(binary);
};

const decode = (input: string) => {
  const atobFn = globalThis.atob;
  if (!atobFn) {
    throw new Error('Base64 decoding is not supported in this environment.');
  }
  const decoded = atobFn(input);
  const bytes = new Uint8Array(decoded.length);
  for (let i = 0; i < decoded.length; i += 1) {
    bytes[i] = decoded.charCodeAt(i);
  }
  return bytes;
};

const base64UrlToUint8Array = (value: string) => {
  const padding = '='.repeat((4 - (value.length % 4)) % 4);
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/') + padding;
  return decode(base64);
};

const uint8ArrayToBase64Url = (value: Uint8Array) => {
  const base64 = encode(value);
  return base64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/u, '');
};

const toUint8Array = (value: ArrayBuffer | ArrayBufferView | null | undefined) => {
  if (!value) {
    return undefined;
  }
  if (value instanceof ArrayBuffer) {
    return new Uint8Array(value);
  }
  const view = value as ArrayBufferView;
  return new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
};

const transformAssertionOptions = (options: unknown): PublicKeyCredentialRequestOptions => {
  const publicKey = (options as { publicKey?: Record<string, unknown> })?.publicKey ?? options;
  if (!publicKey || typeof publicKey !== 'object') {
    throw new Error('Invalid assertion options received from server.');
  }

  const challenge = (publicKey as { challenge?: unknown }).challenge;
  if (typeof challenge !== 'string') {
    throw new Error('Assertion challenge is malformed.');
  }

  const allowCredentials = (publicKey as { allowCredentials?: Array<Record<string, unknown>> }).allowCredentials;

  const transformed: PublicKeyCredentialRequestOptions = {
    ...publicKey as Record<string, unknown>,
    challenge: base64UrlToUint8Array(challenge),
  };

  if (Array.isArray(allowCredentials)) {
    transformed.allowCredentials = allowCredentials.map((credential) => {
      const id = credential.id;
      if (typeof id !== 'string') {
        throw new Error('Credential ID is malformed.');
      }

      return {
        ...credential,
        type: 'public-key',
        id: base64UrlToUint8Array(id),
      } as PublicKeyCredentialDescriptor;
    }) as PublicKeyCredentialDescriptor[];
  }

  return transformed;
};

type AssertionData = {
  id: string;
  type: PublicKeyCredential['type'];
  rawId: Uint8Array;
  authenticatorData: Uint8Array;
  clientDataJSON: Uint8Array;
  signature: Uint8Array;
  userHandle?: Uint8Array;
  clientExtensionResults: ReturnType<PublicKeyCredential['getClientExtensionResults']>;
};

const requestLoginChallenge = async (
  username: string,
): Promise<{ sessionToken: string; publicKey: PublicKeyCredentialRequestOptions }> => {
  const response = await fetch(buildOpsApiUrl('/webauthn/login/begin'), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ username }),
    credentials: 'include',
  });

  if (response.status === 404) {
    raise('begin', 'Unable to start WebAuthn login.', 'No passkey registered for this username. Complete setup before logging in.');
  }

  if (!response.ok) {
    const message = await response.text();
    raise('begin', 'Unable to start WebAuthn login.', message);
  }

  const beginPayload = await response.json().catch((err) => {
    raise('begin', 'Unable to start WebAuthn login.', 'Server returned an invalid challenge response.', err);
  });
  const beginData = beginPayload as WebAuthnLoginBeginResponse;

  if (!beginData.session_token) {
    raise('begin', 'Unable to start WebAuthn login.', 'Server response was missing a session token.');
  }

  if (!beginData.assertion_options) {
    raise('begin', 'Unable to start WebAuthn login.', 'Server response was missing assertion options.');
  }

  const publicKey = (() => {
    try {
      return transformAssertionOptions(beginData.assertion_options);
    } catch (err) {
      const detail = err instanceof Error ? err.message : null;
      return raise('begin', 'Unable to start WebAuthn login.', detail, err);
    }
  })();

  if (typeof window !== 'undefined' && typeof publicKey.rpId === 'string' && publicKey.rpId !== window.location.hostname) {
    console.warn('WebAuthn rpId from server does not match current origin.', {
      expectedRpId: publicKey.rpId,
      pageOrigin: window.location.hostname,
    });
  }

  return {
    sessionToken: beginData.session_token,
    publicKey,
  };
};

const collectAssertion = async (publicKey: PublicKeyCredentialRequestOptions): Promise<AssertionData> => {
  const credential = await navigator.credentials.get({ publicKey }).catch((err) => {
    if (err instanceof DOMException && err.name === 'NotAllowedError') {
      raise('prompt', 'Passkey prompt was dismissed. Try again when you are ready.', undefined, err);
    }
    if (err instanceof DOMException && err.name === 'OperationError') {
      const rpId = typeof publicKey.rpId === 'string' ? publicKey.rpId : undefined;
      const hostname = typeof window !== 'undefined' ? window.location.hostname : undefined;
      const detail = rpId && hostname && rpId !== hostname
        ? `Passkey was issued for ${rpId}, but this page is running on ${hostname}. Sign in from ${rpId} or re-register your passkey for this origin.`
        : 'Passkey rejected for this origin. Confirm you registered the passkey on this domain.';
      if (rpId && hostname && rpId !== hostname) {
        console.error('WebAuthn rpId mismatch detected during login', { rpId, origin: hostname });
      }
      raise('prompt', 'Passkey rejected for this origin. Confirm you registered the passkey on this domain.', detail, err);
    }
    const detail = err instanceof Error ? err.message : null;
    raise('prompt', 'Failed to collect passkey response.', detail, err);
  });

  if (!credential) {
    raise('prompt', 'Authenticator did not provide a credential.');
  }

  const assertion = credential as PublicKeyCredential;
  const response = assertion.response as AuthenticatorAssertionResponse;

  const rawId = requireValue(
    toUint8Array(assertion.rawId),
    'prompt',
    'Incomplete credential returned by authenticator.',
    'Missing credential rawId.',
  );

  const authenticatorData = requireValue(
    toUint8Array(response.authenticatorData),
    'prompt',
    'Incomplete credential returned by authenticator.',
    'Missing authenticator data.',
  );

  const clientDataJSON = requireValue(
    toUint8Array(response.clientDataJSON),
    'prompt',
    'Incomplete credential returned by authenticator.',
    'Missing client data JSON.',
  );

  const signature = requireValue(
    toUint8Array(response.signature),
    'prompt',
    'Incomplete credential returned by authenticator.',
    'Missing signature.',
  );

  const userHandle = toUint8Array(response.userHandle);

  return {
    id: assertion.id,
    type: assertion.type,
    rawId,
    authenticatorData,
    clientDataJSON,
    signature,
    userHandle,
    clientExtensionResults: assertion.getClientExtensionResults(),
  };
};

const finishLogin = async (sessionToken: string, assertion: AssertionData) => {
  const finishRequestBody: WebAuthnLoginFinishRequest = {
    session_token: sessionToken,
    client_response: {
      id: assertion.id,
      rawId: uint8ArrayToBase64Url(assertion.rawId),
      type: assertion.type,
      clientExtensionResults: assertion.clientExtensionResults,
      response: {
        authenticatorData: uint8ArrayToBase64Url(assertion.authenticatorData),
        clientDataJSON: uint8ArrayToBase64Url(assertion.clientDataJSON),
        signature: uint8ArrayToBase64Url(assertion.signature),
        userHandle: assertion.userHandle ? uint8ArrayToBase64Url(assertion.userHandle) : undefined,
      },
    },
  };

  const response = await fetch(buildOpsApiUrl('/webauthn/login/finish'), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(finishRequestBody),
    credentials: 'include',
  });

  if (response.status === 401) {
    raise('verifying', 'WebAuthn verification failed.', 'WebAuthn session expired before verification completed.');
  }

  if (!response.ok) {
    const message = await response.text();
    raise('verifying', 'WebAuthn verification failed.', message);
  }
};

const fetchEncryptedPayload = async (): Promise<VaultEncryptedPayload> => {
  const response = await fetch(buildOpsApiUrl('/vault/payload'), {
    method: 'GET',
    credentials: 'include',
  });

  if (response.status === 404) {
    raise('fetchingPayload', 'Unable to fetch encrypted payload.', 'No encrypted payload found. Run the setup wizard first.');
  }

  if (response.status === 423) {
    raise('fetchingPayload', 'Unable to fetch encrypted payload.', 'Vault is still in setup mode. Complete setup to continue.');
  }

  if (response.status === 401) {
    raise('fetchingPayload', 'Unable to fetch encrypted payload.', 'Session expired while fetching payload. Try logging in again.');
  }

  if (!response.ok) {
    const message = await response.text();
    raise('fetchingPayload', 'Unable to fetch encrypted payload.', message);
  }

  let rawPayload: unknown;
  try {
    rawPayload = await response.json();
  } catch (err) {
    const detail = err instanceof Error && err.message
      ? err.message
      : 'Server returned an invalid payload.';
    raise('fetchingPayload', 'Unable to fetch encrypted payload.', detail, err);
  }

  if (!rawPayload || typeof rawPayload !== 'object') {
    raise('fetchingPayload', 'Unable to fetch encrypted payload.', 'Server did not return a valid payload object.');
  }

  return rawPayload as VaultEncryptedPayload;
};

const unsealVault = async (bundle: VaultSecretsBundleExtended) => {
  const unsealPayload: VaultUnsealRequest = { payload: bundle };

  const response = await fetch(buildOpsApiUrl('/vault/unseal'), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(unsealPayload),
    credentials: 'include',
  });

  if (response.status === 409) {
    raise('unsealing', 'Failed to unseal vault.', 'Vault is already unsealed.');
  }

  if (response.status === 423) {
    raise('unsealing', 'Failed to unseal vault.', 'Vault is still in setup mode. Complete setup before unsealing.');
  }

  if (response.status === 401) {
    raise('unsealing', 'Failed to unseal vault.', 'Session expired before unseal completed. Please log in again.');
  }

  if (!response.ok) {
    const message = await response.text();
    raise('unsealing', 'Failed to unseal vault.', message);
  }
};

const decryptVaultPayload = async (
  payload: VaultEncryptedPayload,
): Promise<VaultSecretsBundleExtended> => {
  if (payload.version !== 'aes-gcm-v1') {
    throw new Error('Unsupported payload version returned by server.');
  }

  const subtle = globalThis.crypto?.subtle;
  if (!subtle) {
    throw new Error('Browser does not support required cryptography APIs.');
  }

  const ciphertext = base64UrlToUint8Array(payload.ciphertext);
  const nonce = base64UrlToUint8Array(payload.nonce);
  const associatedDataValue = typeof payload.associated_data === 'string' ? payload.associated_data : null;
  const additionalData = associatedDataValue ? base64UrlToUint8Array(associatedDataValue) : undefined;
  const prfParams = (payload.prf_params ?? undefined) as Record<string, unknown> | undefined;
  const keyBase64 = typeof prfParams?.key === 'string' ? prfParams.key : null;
  if (!keyBase64) {
    throw new Error('Encrypted payload is missing the key material.');
  }

  const keyBytes = base64UrlToUint8Array(keyBase64);
  const key = await subtle.importKey(
    'raw',
    keyBytes,
    { name: 'AES-GCM' },
    false,
    ['decrypt'],
  );

  const decryptParams: AesGcmParams = { name: 'AES-GCM', iv: nonce };
  if (additionalData) {
    decryptParams.additionalData = additionalData;
  }

  const decrypted = await subtle.decrypt(
    decryptParams,
    key,
    ciphertext,
  );

  const decoder = new TextDecoder();
  const bundleJson = decoder.decode(decrypted);
  const bundle = JSON.parse(bundleJson) as VaultSecretsBundleExtended;

  if (!bundle?.not_secret || !bundle?.secrets) {
    throw new Error('Decrypted payload is incomplete.');
  }

  return bundle;
};

const decryptBundle = async (payload: VaultEncryptedPayload): Promise<VaultSecretsBundleExtended> => {
  try {
    return await decryptVaultPayload(payload);
  } catch (err) {
    const detail = err instanceof Error ? err.message : null;
    return raise('decrypting', 'Failed to decrypt vault payload.', detail, err);
  }
};

export function Login({ initialUsername = '', onAuthenticated }: LoginProps) {
  const [username, setUsername] = useState(initialUsername);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [progressStep, setProgressStep] = useState<ProgressStep>('idle');
  const [statusMessage, setStatusMessage] = useState('');
  const [error, setError] = useState<string | null>(null);

  const supportsWebAuthn = useMemo(() => {
    return typeof window !== 'undefined' && typeof window.PublicKeyCredential !== 'undefined';
  }, []);

  async function runStage<T>(
    stage: ProgressStep,
    message: string,
    task: () => Promise<T>,
  ): Promise<T> {
    setProgressStep(stage);
    setStatusMessage(message);
    return task();
  }

  const handleLoginError = (err: unknown) => {
    console.error('Login failed:', err);
    setProgressStep('idle');
    setStatusMessage('');

    if (err instanceof LoginStageError) {
      setError(err.message);
      return;
    }

    if (err instanceof DOMException && err.name === 'NotAllowedError') {
      setError('Passkey prompt was dismissed. Try again when you are ready.');
      return;
    }

    const fallback = err instanceof Error && err.message
      ? err.message
      : typeof err === 'string'
        ? err
        : 'Unable to complete login.';
    setError(fallback);
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);

    if (!supportsWebAuthn) {
      setError('This browser does not support WebAuthn. Try a different browser.');
      return;
    }

    const trimmedUsername = username.trim();
    if (!trimmedUsername) {
      setError('Enter the username associated with your passkey.');
      return;
    }

    setIsSubmitting(true);

    try {
      const { sessionToken, publicKey } = await runStage(
        'begin',
        'Requesting WebAuthn challenge…',
        () => requestLoginChallenge(trimmedUsername),
      );

      const assertion = await runStage(
        'prompt',
        'Verify with your passkey to continue…',
        () => collectAssertion(publicKey),
      );

      await runStage(
        'verifying',
        'Verifying credential with server…',
        () => finishLogin(sessionToken, assertion),
      );

      const payload = await runStage(
        'fetchingPayload',
        'Fetching encrypted vault payload…',
        () => fetchEncryptedPayload(),
      );

      try {
        const bundle = await runStage(
          'decrypting',
          'Decrypting vault secrets…',
          () => decryptBundle(payload),
        );

        await runStage(
          'unsealing',
          'Unsealing vault with decrypted secrets…',
          () => unsealVault(bundle),
        );
      } catch (err: unknown) {
        console.error('decrypt failed:', err);
        throw err;
      }

      setProgressStep('done');
      setStatusMessage('Vault unsealed. Redirecting…');
      onAuthenticated?.();
    } catch (err: unknown) {
      handleLoginError(err);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-50 to-slate-100 flex flex-col items-center justify-center gap-6 p-4">
      <img
        src="/favicon.svg"
        alt="Recomma logo"
        className="h-48 w-48"
      />
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-slate-900">
            <KeyRound className="h-5 w-5" />
            Unlock Recomma Vault
          </CardTitle>
          <CardDescription>
            Sign in with your registered passkey to load trade secrets securely.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={handleSubmit}>
            <div className="space-y-2">
              <label className="text-sm font-medium text-slate-700" htmlFor="username">
                Username
              </label>
              <Input
                id="username"
                name="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="username"
                disabled={isSubmitting}
              />
            </div>

            {statusMessage && (
              <p className="text-sm text-slate-600">
                {statusMessage}
              </p>
            )}

            {error && (
              <div className="flex items-start gap-2 rounded-md bg-red-50 p-3 text-sm text-red-700">
                <AlertCircle className="mt-0.5 h-4 w-4 flex-shrink-0" />
                <span>{error}</span>
              </div>
            )}

            {!supportsWebAuthn && (
              <p className="text-sm text-red-700">
                This browser does not support passkeys. Switch to a modern browser to unlock the vault.
              </p>
            )}

            <Button
              type="submit"
              className="w-full"
              disabled={isSubmitting || !supportsWebAuthn}
            >
              {isSubmitting ? 'Working…' : 'Unlock Vault'}
            </Button>
          </form>
          {progressStep !== 'idle' && progressStep !== 'done' && (
            <p className="mt-4 text-xs text-slate-500">
              Step: {progressStep}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export default Login;
