import { useCallback, useEffect, useState, lazy, Suspense } from 'react';

const OrdersTable = lazy(async () => {
  const mod = await import('./components/OrdersTable');
  return { default: mod.OrdersTable };
});
import { StatsCards } from './components/StatsCards';
import { Toaster } from './components/ui/sonner';
import { Button } from './components/ui/button';
const SetupWizard = lazy(async () => {
  const mod = await import('./components/setupwizard/SetupWizard');
  return { default: mod.SetupWizard };
});
const Login = lazy(async () => {
  const mod = await import('./components/Login');
  return { default: mod.Login };
});
const Settings = lazy(async () => {
  const mod = await import('./components/Settings');
  return { default: mod.Settings };
});
import type { OrderFilterState, VaultStatus } from './types/api';
import { buildOpsApiUrl } from './config/opsApi';
import { Settings as SettingsIcon } from 'lucide-react';
import { useSystemErrors } from './hooks/useSystemErrors';
import { useToasterReady } from './hooks/useToasterReady';

export type FilterState = OrderFilterState;

export default function App() {
  const [filters, setFilters] = useState<FilterState>({});
  const [selectedBotId, setSelectedBotId] = useState<number | undefined>();
  const [selectedDealId, setSelectedDealId] = useState<number | undefined>();
  const [vaultStatus, setVaultStatus] = useState<VaultStatus | null>(null);
  const [isLoadingVaultStatus, setIsLoadingVaultStatus] = useState(true);
  const [vaultStatusError, setVaultStatusError] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);

  // Track when Toaster component is ready
  const toasterReady = useToasterReady();

  // Only connect to system stream when vault is unsealed AND Toaster is ready
  const isUnsealed = vaultStatus?.state === 'unsealed';
  useSystemErrors(isUnsealed, toasterReady);

  const fetchVaultStatus = useCallback(async () => {
    setIsLoadingVaultStatus(true);
    setVaultStatusError(null);

    try {
      const response = await fetch(buildOpsApiUrl('/vault/status'), {
        method: 'GET',
        credentials: 'include',
      });

      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || 'Unable to determine vault status.');
      }

      const status: VaultStatus = await response.json();
      setVaultStatus(status);
    } catch (err: unknown) {
      console.error('Failed to load vault status:', err);
      const message =
        err instanceof Error ? err.message : 'Failed to load vault status.';
      setVaultStatusError(message);
    } finally {
      setIsLoadingVaultStatus(false);
    }
  }, []);

  useEffect(() => {
    fetchVaultStatus();
  }, [fetchVaultStatus]);

  const handleSetupComplete = () => {
    fetchVaultStatus();
  };

  if (isLoadingVaultStatus) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-50">
        <p className="text-gray-600">Loading vault status…</p>
      </div>
    );
  }

  if (vaultStatusError) {
    return (
      <div className="min-h-screen flex flex-col items-center justify-center bg-gray-50 gap-4">
        <p className="text-red-600">{vaultStatusError}</p>
        <button
          className="rounded-md bg-blue-600 px-4 py-2 text-white hover:bg-blue-700"
          onClick={fetchVaultStatus}
        >
          Retry
        </button>
      </div>
    );
  }

  if (!vaultStatus || vaultStatus.state === 'setup_required') {
    return (
      <Suspense fallback={<div className="min-h-screen flex items-center justify-center bg-gray-50">Loading setup…</div>}>
        <SetupWizard onSetupComplete={handleSetupComplete} />
      </Suspense>
    );
  }

  if (vaultStatus.state === 'sealed') {
    return (
      <Suspense fallback={<div className="min-h-screen flex items-center justify-center bg-gray-50">Loading Login</div>}>
      <Login
        initialUsername={vaultStatus.user?.username ?? ''}
        onAuthenticated={fetchVaultStatus}
      />
      </Suspense>
    );
  }

  const handleBotSelect = (botId: number | undefined) => {
    setSelectedBotId(botId);
    setFilters(prev => ({
      ...prev,
      bot_id: botId?.toString() || undefined
    }));
  };

  const handleDealSelect = (dealId: number | undefined) => {
    setSelectedDealId(dealId);
    setFilters(prev => ({
      ...prev,
      deal_id: dealId?.toString() || undefined
    }));
  };

  // Show Settings page if requested
  if (showSettings) {
    return (
      <Suspense fallback={<div className="min-h-screen flex items-center justify-center bg-gray-50">Loading settings…</div>}>
        <Settings onBack={() => setShowSettings(false)} />
        <Toaster />
      </Suspense>
    );
  }

  return (
    <div className="h-full bg-gray-50 flex flex-col">
      <div className="border-b bg-white shadow-sm flex-shrink-0">
        <div className="container mx-auto px-4 py-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-3">
              <img
                src="/favicon.svg"
                alt="Recomma logo"
                className="h-8 w-8"
              />
              <h1 className="text-gray-900">Recomma - 3Commas to Hyperliquid trade replay</h1>
            </div>
            <Button variant="outline" size="sm" onClick={() => setShowSettings(true)}>
              <SettingsIcon className="h-4 w-4 mr-2" />
              Settings
            </Button>
          </div>
        </div>
      </div>

      <div className="flex-1 flex flex-col min-h-0">
        <div className="container mx-auto px-4 py-3 flex-shrink-0">
          <StatsCards />
        </div>
        <Suspense fallback={<div className="min-h-screen flex items-center justify-center bg-gray-50">Loading orders…</div>}>
        <OrdersTable
          filters={filters}
          selectedBotId={selectedBotId}
          selectedDealId={selectedDealId}
          onBotSelect={handleBotSelect}
          onDealSelect={handleDealSelect}
          onFiltersChange={setFilters}
        />
        </Suspense>
      </div>

      <Toaster />
    </div>
  );
}
