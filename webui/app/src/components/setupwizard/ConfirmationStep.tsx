import { Button } from '../ui/button';
import { Alert, AlertDescription } from '../ui/alert';
import { Badge } from '../ui/badge';
import { Separator } from '../ui/separator';
import { Shield, Key, CheckCircle2, Eye, EyeOff } from 'lucide-react';
import { useState } from 'react';

interface ConfirmationStepProps {
  data: {
    username: string;
    fqdn: string;
    threeCommas: {
      apiKey: string;
      privateKeyFile: string;
    };
    hyperliquid: {
      apiUrl: string;
      wallet: string;
      privateKey: string;
    };
  };
  onBack: () => void;
  onConfirm: () => void;
  isSubmitting: boolean;
  error?: string | null;
}

export function ConfirmationStep({ data, onBack, onConfirm, isSubmitting, error }: ConfirmationStepProps) {
  const [showSensitive, setShowSensitive] = useState(false);

  const maskSecret = (value: string, visibleChars: number = 4) => {
    if (!value) return '';
    if (value.length <= visibleChars * 2) return '••••••••';
    return `${value.slice(0, visibleChars)}${'•'.repeat(8)}${value.slice(-visibleChars)}`;
  };

  return (
    <div className="space-y-6">
      <Alert className="border-blue-200 bg-blue-50">
        <Shield className="h-4 w-4 text-blue-600" />
        <AlertDescription className="text-blue-900">
          Please review your configuration before finalizing. All sensitive data will be encrypted and stored securely.
        </AlertDescription>
      </Alert>

      {/* FQDN & Authentication */}
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <Key className="w-4 h-4 text-slate-600" />
          <h4 className="text-slate-900">Authentication</h4>
        </div>
        <div className="bg-slate-50 rounded-lg p-4 space-y-2">
          <div className="flex justify-between items-center">
            <span className="text-slate-600 text-sm">Username</span>
            <span className="text-slate-900">{data.username}</span>
          </div>
          <div className="flex justify-between items-center">
            <span className="text-slate-600 text-sm">Application FQDN</span>
            <span className="text-slate-900">{data.fqdn}</span>
          </div>
          <div className="flex justify-between items-center">
            <span className="text-slate-600 text-sm">Passkey Status</span>
            <Badge variant="outline" className="bg-green-50 text-green-700 border-green-200">
              <CheckCircle2 className="w-3 h-3 mr-1" />
              Registered
            </Badge>
          </div>
        </div>
      </div>

      <Separator />

      {/* 3Commas */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h4 className="text-slate-900">3Commas Configuration</h4>
        </div>
        <div className="bg-slate-50 rounded-lg p-4 space-y-3">
          <div>
            <div className="text-slate-600 text-sm mb-1">API Key</div>
            <div className="font-mono text-sm text-slate-900 bg-white rounded px-3 py-2 border border-slate-200">
              {showSensitive ? data.threeCommas.apiKey : maskSecret(data.threeCommas.apiKey)}
            </div>
          </div>
          <div>
            <div className="text-slate-600 text-sm mb-1">Private Key File</div>
            <div className="text-sm text-slate-900 bg-white rounded px-3 py-2 border border-slate-200">
              {showSensitive ? (
                <pre className="text-xs overflow-x-auto">{data.threeCommas.privateKeyFile.slice(0, 100)}...</pre>
              ) : (
                <span className="text-slate-500">••••••• PEM file loaded ({data.threeCommas.privateKeyFile.length} characters)</span>
              )}
            </div>
          </div>
        </div>
      </div>

      <Separator />

      {/* Hyperliquid */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h4 className="text-slate-900">Hyperliquid Configuration</h4>
        </div>
        <div className="bg-slate-50 rounded-lg p-4 space-y-3">
          <div>
            <div className="text-slate-600 text-sm mb-1">API URL</div>
            <div className="text-sm text-slate-900 bg-white rounded px-3 py-2 border border-slate-200">
              {data.hyperliquid.apiUrl}
              {data.hyperliquid.apiUrl.includes('testnet') && (
                <Badge variant="outline" className="ml-2 bg-amber-50 text-amber-700 border-amber-200">
                  Testnet
                </Badge>
              )}
              {data.hyperliquid.apiUrl === 'https://api.hyperliquid.xyz' && (
                <Badge variant="outline" className="ml-2 bg-blue-50 text-blue-700 border-blue-200">
                  Mainnet
                </Badge>
              )}
            </div>
          </div>
          <div>
            <div className="text-slate-600 text-sm mb-1">Wallet Address</div>
            <div className="font-mono text-sm text-slate-900 bg-white rounded px-3 py-2 border border-slate-200">
              {showSensitive ? data.hyperliquid.wallet : maskSecret(data.hyperliquid.wallet, 6)}
            </div>
          </div>
          <div>
            <div className="text-slate-600 text-sm mb-1">Private Key</div>
            <div className="font-mono text-sm text-slate-900 bg-white rounded px-3 py-2 border border-slate-200">
              {showSensitive ? data.hyperliquid.privateKey : maskSecret(data.hyperliquid.privateKey, 6)}
            </div>
          </div>
        </div>
      </div>

      {/* Toggle Sensitive Data Visibility */}
      <div className="flex justify-center">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setShowSensitive(!showSensitive)}
          className="text-slate-600"
        >
          {showSensitive ? (
            <>
              <EyeOff className="w-4 h-4 mr-2" />
              Hide sensitive data
            </>
          ) : (
            <>
              <Eye className="w-4 h-4 mr-2" />
              Show sensitive data
            </>
          )}
        </Button>
      </div>

      <div className="flex gap-3 pt-4">
        <Button variant="outline" onClick={onBack} className="flex-1">
          Back
        </Button>
        <Button onClick={onConfirm} className="flex-1" disabled={isSubmitting}>
          {isSubmitting ? 'Saving...' : 'Finalize Setup'}
        </Button>
      </div>

      {error && (
        <Alert variant="destructive">
          <Shield className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}
