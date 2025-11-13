import { useMemo, useState } from 'react';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../ui/card';
import { Button } from '../ui/button';
import { PasskeySetup } from './PasskeySetup';
import { ThreeCommasSetup } from './ThreeCommasSetup';
import { VenueManagement } from '../VenueManagement';
import { ConfirmationStep } from './ConfirmationStep';
import { StepIndicator } from './StepIndicator';
import { CheckCircle2 } from 'lucide-react';
import type {
  VaultEncryptedPayload,
  VaultSecretsBundle,
  VaultSecretsBundleExtended,
  VaultSetupRequest,
  VaultVenueSecret,
} from '../../types/api';
import { getOpsApiBaseUrl } from '../../config/opsApi';

interface SetupData {
  username: string;
  fqdn: string;
  threeCommas: {
    apiKey: string;
    privateKeyFile: string;
    planTier: VaultSecretsBundle['secrets']['THREECOMMAS_PLAN_TIER'];
  };
  venues: VaultVenueSecret[];
}

interface SetupWizardProps {
  onSetupComplete?: () => void;
}

export function SetupWizard({ onSetupComplete }: SetupWizardProps) {
  const [currentStep, setCurrentStep] = useState(1);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submissionError, setSubmissionError] = useState<string | null>(null);
  const [setupData, setSetupData] = useState<SetupData>({
    username: '',
    fqdn: typeof window !== 'undefined' ? window.location.hostname : '',
    threeCommas: {
      apiKey: '',
      privateKeyFile: '',
      planTier: 'expert',
    },
    venues: [],
  });

  const apiBaseUrl = useMemo(() => {
    return getOpsApiBaseUrl().replace(/\/$/, '');
  }, []);

  const arrayBufferToBase64 = (input: ArrayBuffer | Uint8Array) => {
    const view = input instanceof ArrayBuffer
      ? new Uint8Array(input)
      : new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
    let binary = '';
    view.forEach((byte) => {
      binary += String.fromCharCode(byte);
    });
    return btoa(binary);
  };

  const buildSecretsBundle = (data: SetupData): VaultSecretsBundleExtended => {
    const notSecret: VaultSecretsBundleExtended['not_secret'] = {
      username: data.username.trim(),
    };

    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (timezone) {
      notSecret.timezone = timezone;
    }

    return {
      not_secret: notSecret,
      secrets: {
        THREECOMMAS_API_KEY: data.threeCommas.apiKey.trim(),
        THREECOMMAS_PRIVATE_KEY: data.threeCommas.privateKeyFile.trim(),
        THREECOMMAS_PLAN_TIER: data.threeCommas.planTier,
        venues: data.venues,
      },
    };
  };

  const buildEncryptedPayload = async (data: SetupData): Promise<VaultEncryptedPayload> => {
    const encoder = new TextEncoder();
    const bundle = buildSecretsBundle(data);
    const secretPayload = encoder.encode(JSON.stringify(bundle));
    const nonce = crypto.getRandomValues(new Uint8Array(12));
    const key = await crypto.subtle.generateKey(
      { name: 'AES-GCM', length: 256 },
      true,
      ['encrypt'],
    );
    const associatedDataBytes = encoder.encode(JSON.stringify(bundle.not_secret));
    const ciphertext = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: nonce, additionalData: associatedDataBytes },
      key,
      secretPayload,
    );
    const exportedKey = await crypto.subtle.exportKey('raw', key);

    return {
      version: 'aes-gcm-v1',
      ciphertext: arrayBufferToBase64(ciphertext),
      nonce: arrayBufferToBase64(nonce),
      associated_data: arrayBufferToBase64(associatedDataBytes),
      prf_params: {
        algorithm: 'AES-GCM',
        key: arrayBufferToBase64(exportedKey),
        nonce_size: nonce.byteLength,
      },
    };
  };

  const steps = [
    {
      number: 1,
      title: 'Authentication',
      description: 'Secure passkey',
    },
    {
      number: 2,
      title: '3Commas',
      description: 'API credentials',
    },
    {
      number: 3,
      title: 'Hyperliquid',
      description: 'API credentials',
    },
    {
      number: 4,
      title: 'Confirm',
      description: 'Review & finalize',
    },
  ];

  const handlePasskeyComplete = () => {
    setCurrentStep(2);
  };

  const handleThreeCommasComplete = (data: { apiKey: string; privateKeyFile: string; planTier: SetupData['threeCommas']['planTier'] }) => {
    setSetupData((prev) => ({
      ...prev,
      threeCommas: data,
    }));
    setCurrentStep(3);
  };

  const handleVenuesChange = (venues: VaultVenueSecret[]) => {
    setSetupData((prev) => ({
      ...prev,
      venues,
    }));
  };

  const handleVenuesComplete = () => {
    // Check if at least one venue is configured
    if (setupData.venues.length === 0) {
      return; // Button should be disabled in VenueManagement
    }

    // Go to confirmation step
    setCurrentStep(4);
  };

  const handleConfirmation = async () => {
    setSubmissionError(null);
    setIsSubmitting(true);

    try {
      if (!setupData.username.trim()) {
        throw new Error('Username is required before finalizing setup.');
      }

      const payload = await buildEncryptedPayload(setupData);
      const requestBody: VaultSetupRequest = {
        username: setupData.username.trim(),
        payload,
      };

      const response = await fetch(`${apiBaseUrl}/vault/setup`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(requestBody),
        credentials: 'include',
      });

      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || 'Failed to store encrypted vault payload.');
      }

      setCurrentStep(5);
    } catch (err: unknown) {
      console.error('Finalizing setup failed:', err);
      const message =
        err instanceof Error ? err.message : 'Failed to finalize setup. Please try again.';
      setSubmissionError(message);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-50 to-slate-100 flex items-center justify-center p-4">
      <div className="w-full max-w-3xl">
        <div className="text-center mb-8">
          <h1 className="text-slate-900 mb-2">Recomma Setup</h1>
          <p className="text-slate-600">
            Let's get your application configured securely
          </p>
        </div>

        {currentStep <= 4 && <StepIndicator steps={steps} currentStep={currentStep} />}

        <Card>
          <CardHeader>
            <CardTitle>
              {currentStep === 1 && 'Create Your Passkey'}
              {currentStep === 2 && '3Commas Configuration'}
              {currentStep === 3 && 'Hyperliquid Configuration'}
              {currentStep === 4 && 'Review Your Configuration'}
              {currentStep === 5 && 'Setup Complete!'}
            </CardTitle>
            <CardDescription>
              {currentStep === 1 && 'Secure your credentials with passwordless authentication'}
              {currentStep === 2 && 'Enter your 3Commas API credentials'}
              {currentStep === 3 && 'Enter your Hyperliquid API credentials'}
              {currentStep === 4 && 'Verify your settings before finalizing'}
              {currentStep === 5 && 'Your application is ready to use'}
            </CardDescription>
          </CardHeader>

          <CardContent>
            {currentStep === 1 && (
              <PasskeySetup
                username={setupData.username}
                onComplete={handlePasskeyComplete}
                onUsernameChange={(username) => setSetupData((prev) => ({ ...prev, username }))}
              />
            )}

            {currentStep === 2 && (
              <ThreeCommasSetup
                onComplete={handleThreeCommasComplete}
                onBack={() => setCurrentStep(1)}
              />
            )}

            {currentStep === 3 && (
              <div className="space-y-6">
                <VenueManagement
                  context="setup"
                  initialVenues={setupData.venues}
                  onVenuesChange={handleVenuesChange}
                />
                <div className="flex gap-3">
                  <Button variant="outline" onClick={() => setCurrentStep(2)} className="flex-1">
                    Back
                  </Button>
                  <Button
                    onClick={handleVenuesComplete}
                    className="flex-1"
                    disabled={setupData.venues.length === 0}
                  >
                    Continue
                  </Button>
                </div>
              </div>
            )}

            {currentStep === 4 && (
              <ConfirmationStep
                data={setupData}
                onBack={() => setCurrentStep(3)}
                isSubmitting={isSubmitting}
                error={submissionError}
                onConfirm={handleConfirmation}
              />
            )}

            {currentStep === 5 && (
              <div className="text-center py-8">
                <CheckCircle2 className="w-16 h-16 text-green-600 mx-auto mb-4" />
                <h3 className="text-slate-900 mb-2">All Set!</h3>
                <p className="text-slate-600 mb-6">
                  Your credentials have been encrypted and stored securely.
                </p>
                <Button onClick={() => onSetupComplete?.()}>
                  Launch Recomma
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

export default SetupWizard;
