import { useState } from 'react';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { Label } from '../ui/label';
import { Alert, AlertDescription } from '../ui/alert';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '../ui/dialog';
import { RadioGroup, RadioGroupItem } from '../ui/radio-group';
import { HelpCircle, AlertCircle, Settings2 } from 'lucide-react';

interface HyperliquidSetupProps {
  initialData: {
    apiUrl: string;
    wallet: string;
    privateKey: string;
  };
  onComplete: (data: { apiUrl: string; wallet: string; privateKey: string }) => void;
  onBack: () => void;
}

export function HyperliquidSetup({ initialData, onComplete, onBack }: HyperliquidSetupProps) {
  const [apiUrl, setApiUrl] = useState(initialData.apiUrl);
  const [wallet, setWallet] = useState(initialData.wallet);
  const [privateKey, setPrivateKey] = useState(initialData.privateKey);
  const [customUrl, setCustomUrl] = useState('');
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const predefinedUrls = {
    mainnet: 'https://api.hyperliquid.xyz',
    testnet: 'https://api.hyperliquid-testnet.xyz',
  };

  const handleSubmit = () => {
    setError(null);

    const finalApiUrl = apiUrl === 'custom' ? customUrl : apiUrl;

    if (!finalApiUrl.trim()) {
      setError('API URL is required');
      return;
    }

    if (apiUrl === 'custom' && !customUrl.startsWith('https://')) {
      setError('Custom API URL must use HTTPS');
      return;
    }

    if (!wallet.trim()) {
      setError('Wallet address is required');
      return;
    }

    if (!/^(0x)?[0-9a-fA-F]{40}$/.test(wallet.trim())) {
      setError('Wallet address must be a valid hex string (40 characters)');
      return;
    }

    if (!privateKey.trim()) {
      setError('Private key is required');
      return;
    }

    if (!/^(0x)?[0-9a-fA-F]{64}$/.test(privateKey.trim())) {
      setError('Private key must be a valid hex string (64 characters)');
      return;
    }

    onComplete({
      apiUrl: finalApiUrl,
      wallet: wallet.trim(),
      privateKey: privateKey.trim(),
    });
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="text-slate-900">Hyperliquid API Credentials</h3>
        <Dialog>
          <DialogTrigger asChild>
            <button className="inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md border border-slate-200 bg-white px-3 py-1 transition-colors hover:bg-slate-100 hover:text-slate-900">
              <HelpCircle className="w-4 h-4" />
              How to get these?
            </button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Getting Hyperliquid API Credentials</DialogTitle>
              <DialogDescription className="space-y-4 pt-4">
                <div>
                  <h4 className="text-slate-900 mb-2">Instructions will be added here</h4>
                  <p className="text-slate-600">
                    Detailed steps on how to:
                  </p>
                  <ul className="list-disc list-inside space-y-1 mt-2 text-slate-600">
                    <li>Access your Hyperliquid wallet</li>
                    <li>Locate your wallet address (hex string)</li>
                    <li>Export your private key securely</li>
                    <li>Choose between mainnet and testnet</li>
                  </ul>
                </div>
              </DialogDescription>
            </DialogHeader>
          </DialogContent>
        </Dialog>
      </div>

      <div className="space-y-2">
        <Label>API URL</Label>
        <RadioGroup value={apiUrl} onValueChange={setApiUrl}>
          <div className="flex items-center space-x-2">
            <RadioGroupItem value={predefinedUrls.mainnet} id="mainnet" />
            <Label htmlFor="mainnet" className="cursor-pointer">
              Mainnet ({predefinedUrls.mainnet})
            </Label>
          </div>
          <div className="flex items-center space-x-2">
            <RadioGroupItem value={predefinedUrls.testnet} id="testnet" />
            <Label htmlFor="testnet" className="cursor-pointer">
              Testnet ({predefinedUrls.testnet})
            </Label>
          </div>
          {showAdvanced && (
            <div className="space-y-2 p-4 bg-amber-50 border border-amber-200 rounded-lg mt-2">
              <div className="flex items-center gap-2 mb-3">
                <AlertCircle className="w-4 h-4 text-amber-600" />
                <Label className="text-amber-900">Advanced: Custom API URL</Label>
              </div>
              <div className="flex items-center space-x-2 mb-2">
                <RadioGroupItem value="custom" id="custom" />
                <Label htmlFor="custom" className="cursor-pointer">
                  Custom URL (self-hosted)
                </Label>
              </div>
              {apiUrl === 'custom' && (
                <Input
                  value={customUrl}
                  onChange={(e) => setCustomUrl(e.target.value)}
                  placeholder="https://your-custom-api.example.com"
                  className="bg-white"
                />
              )}
            </div>
          )}
        </RadioGroup>
      </div>

      {!showAdvanced && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setShowAdvanced(true)}
          className="text-slate-600"
        >
          <Settings2 className="w-4 h-4 mr-2" />
          Show advanced options
        </Button>
      )}

      <div className="space-y-2">
        <Label htmlFor="wallet">Wallet Address</Label>
        <Input
          id="wallet"
          value={wallet}
          onChange={(e) => setWallet(e.target.value)}
          placeholder="0x..."
          className="font-mono"
        />
        <p className="text-slate-500 text-sm">
          Hex string of your wallet address
        </p>
      </div>

      <div className="space-y-2">
        <Label htmlFor="privateKey">Private Key</Label>
        <Input
          id="privateKey"
          type="password"
          value={privateKey}
          onChange={(e) => setPrivateKey(e.target.value)}
          placeholder="0x..."
          className="font-mono"
        />
        <p className="text-slate-500 text-sm">
          Hex string of your private key (keep this secure!)
        </p>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="flex gap-3">
        <Button variant="outline" onClick={onBack} className="flex-1">
          Back
        </Button>
        <Button onClick={handleSubmit} className="flex-1">
          Continue
        </Button>
      </div>
    </div>
  );
}
