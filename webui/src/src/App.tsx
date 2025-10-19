import { useCallback, useEffect, useState } from 'react';
import { OrdersTable } from './components/OrdersTable';
import { FilterBar } from './components/FilterBar';
import { StatsCards } from './components/StatsCards';
import { BotsDealsExplorer } from './components/BotsDealsExplorer';
import { Toaster } from './components/ui/sonner';
import { SetupWizard } from './components/setupwizard/SetupWizard';
import { Login } from './components/Login';
import type { OrderFilterState, VaultStatus } from './types/api';
import { buildOpsApiUrl } from './config/opsApi';

export type FilterState = OrderFilterState;

export default function App() {
  const [filters, setFilters] = useState<FilterState>({});
  const [selectedBotId, setSelectedBotId] = useState<number | undefined>();
  const [selectedDealId, setSelectedDealId] = useState<number | undefined>();
  const [vaultStatus, setVaultStatus] = useState<VaultStatus | null>(null);
  const [isLoadingVaultStatus, setIsLoadingVaultStatus] = useState(true);
  const [vaultStatusError, setVaultStatusError] = useState<string | null>(null);

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
    } catch (err: any) {
      console.error('Failed to load vault status:', err);
      setVaultStatusError(err?.message || 'Failed to load vault status.');
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
    return <SetupWizard onSetupComplete={handleSetupComplete} />;
  }

  if (vaultStatus.state === 'sealed') {
    return (
      <Login
        initialUsername={vaultStatus.user?.username ?? ''}
        onAuthenticated={fetchVaultStatus}
      />
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

  return (
    <div className="min-h-screen bg-gray-50">
      <div className="border-b bg-white shadow-sm">
        <div className="container mx-auto px-4 py-4">
          <h1 className="text-gray-900">Recomma - 3Commas → Hyperliquid Trade Replay</h1>
        </div>
      </div>

      <div className="container mx-auto px-4 py-4 space-y-4">
        <StatsCards />
        <BotsDealsExplorer 
          onBotSelect={handleBotSelect}
          onDealSelect={handleDealSelect}
          selectedBotId={selectedBotId}
          selectedDealId={selectedDealId}
        />
        <FilterBar filters={filters} onFiltersChange={setFilters} />
        <OrdersTable filters={filters} />
      </div>

      <Toaster />
    </div>
  );
}
