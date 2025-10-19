import { useMemo, useState } from 'react';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { Label } from '../ui/label';
import { Alert, AlertDescription } from '../ui/alert';
import { Key, AlertCircle, CheckCircle2 } from 'lucide-react';
import type {
  WebAuthnRegistrationBeginResponse,
  WebAuthnRegistrationFinishRequest,
} from '../../types/api';
import { getOpsApiBaseUrl } from '../../config/opsApi';

interface PasskeySetupProps {
  username: string;
  onComplete: () => void;
  onUsernameChange: (username: string) => void;
}

export function PasskeySetup({ username, onComplete, onUsernameChange }: PasskeySetupProps) {
  const [isRegistering, setIsRegistering] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [isInIframe] = useState(() => {
    try {
      return window.self !== window.top;
    } catch {
      return true;
    }
  });

  const apiBaseUrl = useMemo(() => {
    return getOpsApiBaseUrl().replace(/\/$/, '');
  }, []);

  const base64UrlToArrayBuffer = (value: string): ArrayBuffer => {
    const padding = '='.repeat((4 - (value.length % 4)) % 4);
    const base64 = (value + padding).replace(/-/g, '+').replace(/_/g, '/');
    const binary = atob(base64);
    const buffer = new ArrayBuffer(binary.length);
    const bytes = new Uint8Array(buffer);
    for (let i = 0; i < binary.length; i += 1) {
      bytes[i] = binary.charCodeAt(i);
    }
    return buffer;
  };

  const arrayBufferToBase64Url = (buffer: ArrayBuffer): string => {
    const bytes = new Uint8Array(buffer);
    let binary = '';
    bytes.forEach((b) => {
      binary += String.fromCharCode(b);
    });
    const base64 = btoa(binary);
    return base64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  };

  const mapCreationOptions = (options: unknown): PublicKeyCredentialCreationOptions => {
    if (!options || typeof options !== 'object') {
      throw new Error('Invalid WebAuthn creation options received from server.');
    }

    const base = options as PublicKeyCredentialCreationOptions & Record<string, unknown>;

    if (!base.rp || !base.user || !base.pubKeyCredParams) {
      throw new Error('Creation options from server are missing required fields.');
    }

    const challengeSource = new Uint8Array(
      base64UrlToArrayBuffer(String(base.challenge ?? '')),
    );

    const userIdSource = new Uint8Array(
      base64UrlToArrayBuffer(String(base.user.id ?? '')),
    );

    const excludeCredentials = Array.isArray(base.excludeCredentials)
      ? base.excludeCredentials.map((credential) => ({
          ...credential,
          id: new Uint8Array(
            base64UrlToArrayBuffer(String(credential.id ?? '')),
          ),
        }))
      : undefined;

    return {
      ...base,
      challenge: challengeSource,
      user: {
        ...base.user,
        id: userIdSource,
      },
      excludeCredentials,
    };
  };

  const serializeAttestationResponse = (credential: PublicKeyCredential) => {
    const response = credential.response as AuthenticatorAttestationResponse;

    return {
      id: credential.id,
      rawId: arrayBufferToBase64Url(credential.rawId),
      type: credential.type,
      response: {
        attestationObject: arrayBufferToBase64Url(response.attestationObject),
        clientDataJSON: arrayBufferToBase64Url(response.clientDataJSON),
      },
      clientExtensionResults: credential.getClientExtensionResults(),
      authenticatorAttachment: credential.authenticatorAttachment,
    };
  };

  const handleRegisterPasskey = async () => {
    setIsRegistering(true);
    setError(null);

    try {
      if (!username.trim()) {
        setError('Username is required to register a passkey.');
        return;
      }

      if (!window.PublicKeyCredential) {
        setError('WebAuthn is not supported in this environment.');
        return;
      }

      const beginResponse = await fetch(`${apiBaseUrl}/webauthn/registration/begin`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          username: username.trim(),
        }),
        credentials: 'include',
      });

      if (!beginResponse.ok) {
        const message = await beginResponse.text();
        throw new Error(message || 'Failed to start WebAuthn registration.');
      }

      const beginPayload: WebAuthnRegistrationBeginResponse = await beginResponse.json();
      if (!beginPayload.creation_options) {
        throw new Error('Server did not supply WebAuthn creation options.');
      }
      const publicKey = mapCreationOptions(beginPayload.creation_options);

      const credential = await navigator.credentials.create({
        publicKey,
      }) as PublicKeyCredential;

      if (credential) {
        const finishPayload: WebAuthnRegistrationFinishRequest = {
          session_token: beginPayload.session_token,
          client_response: serializeAttestationResponse(credential),
        };

        const finishResponse = await fetch(`${apiBaseUrl}/webauthn/registration/finish`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify(finishPayload),
          credentials: 'include',
        });

        if (!finishResponse.ok) {
          const message = await finishResponse.text();
          throw new Error(message || 'Failed to complete WebAuthn registration.');
        }

        setSuccess(true);
        setTimeout(() => {
          onComplete();
        }, 1500);
      } else {
        throw new Error('No credential returned by authenticator.');
      }
    } catch (err: unknown) {
      console.error('Passkey registration error:', err);
      if (err instanceof DOMException && err.name === 'InvalidStateError') {
        setError('This device already has a passkey for this account. Remove it or use another device.');
        return;
      }

      setError((err as { message?: string })?.message ?? 'Failed to register passkey. Please try again.');
    } finally {
      setIsRegistering(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <Label htmlFor="username">Username</Label>
        <Input
          id="username"
          value={username}
          onChange={(e) => onUsernameChange(e.target.value)}
          placeholder="your.email@example.com"
          disabled={success}
        />
        <p className="text-slate-500 text-sm">
          Enter your email or a unique identifier for this account.
        </p>
      </div>

      <div className="bg-slate-50 rounded-lg p-4 space-y-3">
        <div className="flex items-start gap-3">
          <Key className="w-5 h-5 text-slate-600 mt-0.5" />
          <div>
            <h4 className="text-slate-900 mb-1">About Passkeys</h4>
            <p className="text-slate-600 text-sm">
              Passkeys use WebAuthn/FIDO2 to create a secure, passwordless authentication method. 
              You'll use your device's biometric authentication (fingerprint, face recognition) 
              or security key to protect your credentials.
            </p>
            {isInIframe && (
              <p className="text-amber-600 text-sm mt-2">
                <strong>Preview Notice:</strong> Passkey registration might be blocked inside iframes. 
                Open the app in a standalone window if registration fails.
              </p>
            )}
          </div>
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {success && (
        <Alert className="border-green-200 bg-green-50">
          <CheckCircle2 className="h-4 w-4 text-green-600" />
          <AlertDescription className="text-green-800">
            Passkey registered successfully! Proceeding to next step...
          </AlertDescription>
        </Alert>
      )}

      <Button
        onClick={handleRegisterPasskey}
        disabled={isRegistering || success || !username.trim()}
        className="w-full"
        size="lg"
      >
        {isRegistering ? 'Registering Passkey...' : success ? 'Passkey Registered!' : 'Register Passkey'}
      </Button>
    </div>
  );
}
