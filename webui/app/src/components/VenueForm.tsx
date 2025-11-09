import { useState, useEffect } from 'react';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Label } from './ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './ui/select';
import { Checkbox } from './ui/checkbox';
import { Alert, AlertDescription } from './ui/alert';
import { AlertCircle, Eye, EyeOff, Info } from 'lucide-react';
import type { VenueFormData, VenueRecord } from '../types/api';
import {
  validateDisplayName,
  validateWalletAddress,
  validatePrivateKey,
  validateApiUrl,
  API_ENDPOINTS,
  isDuplicateWallet,
  normalizeWalletAddress,
} from '../lib/venue-utils';

interface VenueFormProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (data: VenueFormData) => void | Promise<void>;
  existingVenues: VenueRecord[];
  editingVenue?: VenueRecord & { isPrimary?: boolean };
  isFirstVenue?: boolean;
  loading?: boolean;
}

export function VenueForm({
  open,
  onOpenChange,
  onSubmit,
  existingVenues,
  editingVenue,
  isFirstVenue = false,
  loading = false,
}: VenueFormProps) {
  const [formData, setFormData] = useState<VenueFormData>({
    display_name: '',
    api_url: API_ENDPOINTS.MAINNET,
    wallet: '',
    private_key: '',
    is_primary: isFirstVenue,
  });

  const [customUrl, setCustomUrl] = useState('');
  const [showPrivateKey, setShowPrivateKey] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [walletWarning, setWalletWarning] = useState<string>('');
  const [privateKeyChanged, setPrivateKeyChanged] = useState(false);

  // Initialize form when editing
  useEffect(() => {
    if (editingVenue) {
      setFormData({
        display_name: editingVenue.display_name,
        api_url: editingVenue.flags?.api_url as string || API_ENDPOINTS.MAINNET,
        wallet: editingVenue.wallet,
        private_key: '', // Never pre-populate private key
        is_primary: editingVenue.isPrimary || false,
      });

      // Set custom URL if not a predefined endpoint
      const apiUrl = editingVenue.flags?.api_url as string || API_ENDPOINTS.MAINNET;
      if (apiUrl !== API_ENDPOINTS.MAINNET && apiUrl !== API_ENDPOINTS.TESTNET) {
        setCustomUrl(apiUrl);
      }
    } else {
      setFormData({
        display_name: '',
        api_url: API_ENDPOINTS.MAINNET,
        wallet: '',
        private_key: '',
        is_primary: isFirstVenue,
      });
      setCustomUrl('');
    }
    setErrors({});
    setWalletWarning('');
    setPrivateKeyChanged(false);
    setShowPrivateKey(false);
  }, [editingVenue, isFirstVenue, open]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    const newErrors: Record<string, string> = {};

    // Validate display name
    const existingNames = existingVenues
      .filter((v) => v.venue_id !== editingVenue?.venue_id)
      .map((v) => v.display_name);
    const nameError = validateDisplayName(
      formData.display_name,
      existingNames,
      editingVenue?.display_name
    );
    if (nameError) {
      newErrors.display_name = nameError;
    }

    // Validate wallet (cannot change when editing)
    if (!editingVenue) {
      const walletError = validateWalletAddress(formData.wallet);
      if (walletError) {
        newErrors.wallet = walletError;
      }
    }

    // Validate private key (only if provided or not editing)
    if (!editingVenue || privateKeyChanged) {
      const keyError = validatePrivateKey(formData.private_key);
      if (keyError) {
        newErrors.private_key = keyError;
      }
    }

    // Validate API URL
    const urlToValidate =
      formData.api_url === API_ENDPOINTS.CUSTOM ? customUrl : formData.api_url;
    const isCustom = formData.api_url === API_ENDPOINTS.CUSTOM;
    const urlError = validateApiUrl(urlToValidate, isCustom);
    if (urlError) {
      newErrors.api_url = urlError;
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    // Build final form data
    const finalData: VenueFormData = {
      ...formData,
      api_url: formData.api_url === API_ENDPOINTS.CUSTOM ? customUrl : formData.api_url,
      wallet: editingVenue ? formData.wallet : normalizeWalletAddress(formData.wallet),
    };

    await onSubmit(finalData);
  };

  const handleWalletChange = (value: string) => {
    setFormData({ ...formData, wallet: value });
    setErrors({ ...errors, wallet: '' });

    // Check for duplicates
    const filteredVenues = existingVenues.filter((v) => v.venue_id !== editingVenue?.venue_id);

    if (isDuplicateWallet(value, filteredVenues, editingVenue?.venue_id)) {
      const duplicateVenue = existingVenues.find(
        (v) => normalizeWalletAddress(v.wallet).toLowerCase() === normalizeWalletAddress(value).toLowerCase()
      );
      setWalletWarning(
        `This wallet address is already configured as "${duplicateVenue?.display_name}". Continue anyway?`
      );
    } else {
      setWalletWarning('');
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {editingVenue ? 'Edit Wallet' : 'Add Wallet'}
          </DialogTitle>
          <DialogDescription>
            {editingVenue
              ? 'Update the configuration for this Hyperliquid wallet.'
              : 'Configure a new Hyperliquid wallet for trading.'}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit}>
          <div className="space-y-4 py-4">
            {/* Display Name */}
            <div className="space-y-2">
              <Label htmlFor="display_name">
                Display Name <span className="text-red-500">*</span>
              </Label>
              <Input
                id="display_name"
                placeholder="e.g., Main Trading Wallet"
                value={formData.display_name}
                onChange={(e) => {
                  setFormData({ ...formData, display_name: e.target.value });
                  setErrors({ ...errors, display_name: '' });
                }}
                className={errors.display_name ? 'border-red-500' : ''}
              />
              {errors.display_name && (
                <p className="text-sm text-red-500">{errors.display_name}</p>
              )}
              <p className="text-sm text-gray-500">
                How you'll identify this wallet (1-50 characters)
              </p>
            </div>

            {/* API Endpoint */}
            <div className="space-y-2">
              <Label htmlFor="api_url">
                API Endpoint <span className="text-red-500">*</span>
              </Label>
              <Select
                value={formData.api_url}
                onValueChange={(value) => {
                  setFormData({ ...formData, api_url: value });
                  setErrors({ ...errors, api_url: '' });
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={API_ENDPOINTS.MAINNET}>
                    Mainnet (https://api.hyperliquid.xyz)
                  </SelectItem>
                  <SelectItem value={API_ENDPOINTS.TESTNET}>
                    Testnet (https://api.hyperliquid-testnet.xyz)
                  </SelectItem>
                  <SelectItem value={API_ENDPOINTS.CUSTOM}>Custom URL</SelectItem>
                </SelectContent>
              </Select>

              {formData.api_url === API_ENDPOINTS.CUSTOM && (
                <div className="mt-2">
                  <Input
                    placeholder="https://custom-api.example.com"
                    value={customUrl}
                    onChange={(e) => {
                      setCustomUrl(e.target.value);
                      setErrors({ ...errors, api_url: '' });
                    }}
                    className={errors.api_url ? 'border-red-500' : ''}
                  />
                </div>
              )}

              {errors.api_url && (
                <p className="text-sm text-red-500">{errors.api_url}</p>
              )}
            </div>

            {/* Wallet Address */}
            <div className="space-y-2">
              <Label htmlFor="wallet">
                Wallet Address <span className="text-red-500">*</span>
              </Label>
              <Input
                id="wallet"
                placeholder="0x1234567890abcdef..."
                value={formData.wallet}
                onChange={(e) => handleWalletChange(e.target.value)}
                disabled={!!editingVenue}
                className={errors.wallet ? 'border-red-500' : ''}
                style={{ fontFamily: 'monospace' }}
              />
              {errors.wallet && (
                <p className="text-sm text-red-500">{errors.wallet}</p>
              )}
              {walletWarning && (
                <Alert>
                  <AlertCircle className="h-4 w-4" />
                  <AlertDescription>{walletWarning}</AlertDescription>
                </Alert>
              )}
              {editingVenue && (
                <p className="text-sm text-gray-500">
                  Wallet address cannot be changed for security reasons
                </p>
              )}
              {!editingVenue && (
                <p className="text-sm text-gray-500">
                  40-character hex address (with or without 0x prefix)
                </p>
              )}
            </div>

            {/* Private Key */}
            <div className="space-y-2">
              <Label htmlFor="private_key">
                Private Key{' '}
                {!editingVenue || privateKeyChanged ? (
                  <span className="text-red-500">*</span>
                ) : null}
              </Label>
              <div className="relative">
                <Input
                  id="private_key"
                  type={showPrivateKey ? 'text' : 'password'}
                  placeholder={
                    editingVenue
                      ? '••••••••••••••••••••••••'
                      : '0xabcdef1234567890...'
                  }
                  value={formData.private_key}
                  onChange={(e) => {
                    setFormData({ ...formData, private_key: e.target.value });
                    setPrivateKeyChanged(true);
                    setErrors({ ...errors, private_key: '' });
                  }}
                  className={errors.private_key ? 'border-red-500 pr-10' : 'pr-10'}
                  style={{ fontFamily: 'monospace' }}
                />
                <button
                  type="button"
                  onClick={() => setShowPrivateKey(!showPrivateKey)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-700"
                >
                  {showPrivateKey ? (
                    <EyeOff className="h-4 w-4" />
                  ) : (
                    <Eye className="h-4 w-4" />
                  )}
                </button>
              </div>
              {errors.private_key && (
                <p className="text-sm text-red-500">{errors.private_key}</p>
              )}
              {editingVenue && !privateKeyChanged && (
                <p className="text-sm text-gray-500">
                  Leave blank to keep existing key, or enter a new key to update
                </p>
              )}
              {(!editingVenue || privateKeyChanged) && (
                <p className="text-sm text-gray-500">
                  64-character hex string (with or without 0x prefix)
                </p>
              )}
              <Alert>
                <Info className="h-4 w-4" />
                <AlertDescription className="text-sm">
                  Your private key is encrypted and never leaves your browser in plain text
                </AlertDescription>
              </Alert>
            </div>

            {/* Primary Checkbox */}
            <div className="flex items-center space-x-2">
              <Checkbox
                id="is_primary"
                checked={formData.is_primary}
                onCheckedChange={(checked) =>
                  setFormData({ ...formData, is_primary: checked as boolean })
                }
                disabled={isFirstVenue}
              />
              <Label htmlFor="is_primary" className="cursor-pointer">
                Set as primary wallet
                {isFirstVenue && ' (required for first wallet)'}
              </Label>
            </div>
            <p className="text-sm text-gray-500 ml-6">
              The primary wallet is the default for bots without specific assignments
            </p>
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={loading}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              {loading ? 'Saving...' : editingVenue ? 'Save Changes' : 'Add Wallet'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
