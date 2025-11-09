import { useState, useEffect } from 'react';
import { Button } from './ui/button';
import { Alert, AlertDescription } from './ui/alert';
import { Plus, AlertCircle, CheckCircle, Info } from 'lucide-react';
import { VenueList } from './VenueList';
import { VenueForm } from './VenueForm';
import type { VenueFormData, VenueWithAssignments, VaultVenueSecret, VaultSecretsBundle } from '../types/api';
import { generateVenueId, normalizePrivateKey } from '../lib/venue-utils';
import { fetchVenues, fetchVenueAssignments, fetchVaultPayload, updateVaultPayload } from '../lib/venue-api';
import { decryptVaultPayload, encryptVaultPayload } from '../lib/vault-utils';
import { toast } from 'sonner';

interface VenueManagementProps {
  /** Context: 'setup' for wizard, 'settings' for post-setup */
  context: 'setup' | 'settings';
  /** Initial venues (for setup wizard) */
  initialVenues?: VaultVenueSecret[];
  /** Callback when venues change (for setup wizard) */
  onVenuesChange?: (venues: VaultVenueSecret[]) => void;
  /** Callback when venues are loaded (for settings) */
  onVenuesLoaded?: (venues: VenueWithAssignments[]) => void;
  /** Callback when user wants to view venue details */
  onViewDetail?: (venue: VenueWithAssignments) => void;
  /** Whether the component is in a loading state */
  loading?: boolean;
}

export function VenueManagement({
  context,
  initialVenues = [],
  onVenuesChange,
  onVenuesLoaded,
  onViewDetail,
  loading = false,
}: VenueManagementProps) {
  const [venues, setVenues] = useState<VenueWithAssignments[]>([]);
  const [formOpen, setFormOpen] = useState(false);
  const [editingVenue, setEditingVenue] = useState<VenueWithAssignments | undefined>();
  const [apiError, setApiError] = useState<string | null>(null);
  const [apiLoading, setApiLoading] = useState(false);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);
  const [vaultNeedsReseal, setVaultNeedsReseal] = useState(false);

  const isSetupContext = context === 'setup';

  // Initialize venues
  useEffect(() => {
    if (isSetupContext) {
      // Setup wizard: use local state from initialVenues
      const mappedVenues = initialVenues.map((v) => ({
        venue_id: v.id,
        type: v.type,
        display_name: v.display_name || '',
        wallet: v.wallet,
        flags: { api_url: v.api_url },
        isPrimary: v.is_primary,
        assignmentCount: 0,
      }));
      setVenues(mappedVenues);
    } else {
      // Settings: load from API
      loadVenuesFromAPI();
    }
  }, [isSetupContext, initialVenues]);

  // Load venues from API (settings context only)
  const loadVenuesFromAPI = async () => {
    if (isSetupContext) return;

    setApiLoading(true);
    setApiError(null);

    try {
      const venues = await fetchVenues();

      // Load assignment counts for each venue
      const venuesWithCounts = await Promise.all(
        venues.map(async (venue) => {
          try {
            const assignments = await fetchVenueAssignments(venue.venue_id);
            return {
              ...venue,
              assignmentCount: assignments.length,
            };
          } catch {
            return { ...venue, assignmentCount: 0 };
          }
        })
      );

      setVenues(venuesWithCounts);
      onVenuesLoaded?.(venuesWithCounts);
    } catch (error) {
      setApiError(error instanceof Error ? error.message : 'Failed to load wallets');
    } finally {
      setApiLoading(false);
    }
  };

  // Helper to update vault by modifying venues array (settings context only)
  const updateVaultWithModifiedVenues = async (
    modifyVenues: (currentVenues: VaultVenueSecret[]) => VaultVenueSecret[]
  ) => {
    if (isSetupContext) return;

    try {
      // Fetch current encrypted payload
      const encryptedPayload = await fetchVaultPayload();

      // Decrypt to get current secrets
      const decryptedBundle = await decryptVaultPayload(encryptedPayload);

      // Apply the modification to venues
      const updatedVenues = modifyVenues(decryptedBundle.secrets.venues);

      // Update venues in the bundle
      const updatedBundle: VaultSecretsBundle = {
        ...decryptedBundle,
        secrets: {
          ...decryptedBundle.secrets,
          venues: updatedVenues,
        },
      };

      // Re-encrypt the bundle
      const username = decryptedBundle.not_secret.username;
      const newEncryptedPayload = await encryptVaultPayload(updatedBundle, username);

      // Send to server
      await updateVaultPayload(newEncryptedPayload, updatedBundle);

      // Mark that vault needs reseal
      setVaultNeedsReseal(true);
    } catch (error) {
      throw new Error(
        error instanceof Error ? error.message : 'Failed to update vault'
      );
    }
  };

  // Handle adding a venue
  const handleAddVenue = async (formData: VenueFormData) => {
    setApiError(null);
    setSuccessMessage(null);

    const venueId = generateVenueId(formData.display_name);

    const newVenue: VaultVenueSecret = {
      id: venueId,
      type: 'hyperliquid',
      display_name: formData.display_name,
      wallet: formData.wallet,
      private_key: normalizePrivateKey(formData.private_key),
      api_url: formData.api_url,
      flags: { api_url: formData.api_url },
      is_primary: formData.is_primary,
    };

    if (isSetupContext) {
      // Setup wizard: update local state
      let updatedVenues = [...initialVenues];

      // If this is being set as primary, demote others
      if (formData.is_primary) {
        updatedVenues = updatedVenues.map((v) => ({ ...v, is_primary: false }));
      }

      updatedVenues.push(newVenue);
      onVenuesChange?.(updatedVenues);
      setFormOpen(false);
      setSuccessMessage('Wallet added successfully');
    } else {
      // Settings: update vault with new venue
      setApiLoading(true);
      try {
        await updateVaultWithModifiedVenues((currentVenues) => {
          // If new venue is primary, demote all others
          const updatedVenues = formData.is_primary
            ? currentVenues.map((v) => ({ ...v, is_primary: false }))
            : currentVenues;

          // Add the new venue
          return [...updatedVenues, newVenue];
        });

        // Reload venues from API to reflect changes
        await loadVenuesFromAPI();
        setFormOpen(false);
        toast.success('Wallet added successfully. Seal and re-unseal vault to activate.');
      } catch (error) {
        setApiError(error instanceof Error ? error.message : 'Failed to add wallet');
      } finally {
        setApiLoading(false);
      }
    }
  };

  // Handle editing a venue
  const handleEditVenue = async (formData: VenueFormData) => {
    if (!editingVenue) return;

    setApiError(null);
    setSuccessMessage(null);

    if (isSetupContext) {
      // Setup wizard: update local state
      const updatedVenues = initialVenues.map((v) => {
        if (v.id === editingVenue.venue_id) {
          return {
            ...v,
            display_name: formData.display_name,
            api_url: formData.api_url,
            flags: { api_url: formData.api_url },
            is_primary: formData.is_primary,
            // Only update private key if it was changed
            ...(formData.private_key && { private_key: normalizePrivateKey(formData.private_key) }),
          };
        }
        // Demote other primaries if this is being set as primary
        if (formData.is_primary && v.is_primary) {
          return { ...v, is_primary: false };
        }
        return v;
      });

      onVenuesChange?.(updatedVenues);
      setFormOpen(false);
      setEditingVenue(undefined);
      setSuccessMessage('Wallet updated successfully');
    } else {
      // Settings: update vault
      setApiLoading(true);
      try {
        await updateVaultWithModifiedVenues((currentVenues) => {
          return currentVenues.map((v) => {
            if (v.id === editingVenue.venue_id) {
              return {
                ...v,
                display_name: formData.display_name,
                api_url: formData.api_url,
                flags: { api_url: formData.api_url },
                is_primary: formData.is_primary,
                // Only update private key if it was changed
                ...(formData.private_key && { private_key: normalizePrivateKey(formData.private_key) }),
              };
            }
            // Demote other primaries if this is being set as primary
            if (formData.is_primary && v.is_primary) {
              return { ...v, is_primary: false };
            }
            return v;
          });
        });

        await loadVenuesFromAPI();
        setFormOpen(false);
        setEditingVenue(undefined);
        toast.success('Wallet updated successfully. Seal and re-unseal vault to activate.');
      } catch (error) {
        setApiError(error instanceof Error ? error.message : 'Failed to update wallet');
      } finally {
        setApiLoading(false);
      }
    }
  };

  // Handle deleting a venue
  const handleDeleteVenue = async (venue: VenueWithAssignments) => {
    if (venues.length === 1) {
      toast.error('Cannot delete the only wallet');
      return;
    }

    setApiError(null);
    setSuccessMessage(null);

    if (isSetupContext) {
      // Setup wizard: update local state
      const filteredVenues = initialVenues.filter((v) => v.id !== venue.venue_id);

      // If we deleted the primary, promote another one
      const updatedVenues = venue.isPrimary && filteredVenues.length > 0
        ? filteredVenues.map((v, idx) => idx === 0 ? { ...v, is_primary: true } : v)
        : filteredVenues;

      onVenuesChange?.(updatedVenues);
      setSuccessMessage(`Wallet "${venue.display_name}" deleted successfully`);
    } else {
      // Settings: update vault
      setApiLoading(true);
      try {
        await updateVaultWithModifiedVenues((currentVenues) => {
          const filteredVenues = currentVenues.filter((v) => v.id !== venue.venue_id);

          // If we deleted the primary, promote another one
          if (venue.isPrimary && filteredVenues.length > 0) {
            filteredVenues[0].is_primary = true;
          }

          return filteredVenues;
        });

        await loadVenuesFromAPI();
        toast.success(`Wallet "${venue.display_name}" deleted successfully. Seal and re-unseal vault to activate.`);
      } catch (error) {
        setApiError(error instanceof Error ? error.message : 'Failed to delete wallet');
      } finally {
        setApiLoading(false);
      }
    }
  };

  const handleEdit = (venue: VenueWithAssignments) => {
    setEditingVenue(venue);
    setFormOpen(true);
  };

  const handleFormSubmit = async (formData: VenueFormData) => {
    if (editingVenue) {
      await handleEditVenue(formData);
    } else {
      await handleAddVenue(formData);
    }
  };

  const handleFormClose = () => {
    setFormOpen(false);
    setEditingVenue(undefined);
  };

  // Auto-dismiss success messages
  useEffect(() => {
    if (successMessage) {
      const timer = setTimeout(() => setSuccessMessage(null), 5000);
      return () => clearTimeout(timer);
    }
  }, [successMessage]);

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl">
            {isSetupContext ? 'Configure Wallets' : 'Wallet Management'}
          </h2>
          <p className="text-sm text-gray-600 mt-1">
            {isSetupContext
              ? 'Add one or more Hyperliquid wallets for trading'
              : 'Manage your Hyperliquid wallets and bot assignments'}
          </p>
        </div>
        <Button onClick={() => setFormOpen(true)} disabled={loading || apiLoading}>
          <Plus className="mr-2 h-4 w-4" />
          Add Wallet
        </Button>
      </div>

      {/* Vault Reseal Required Banner */}
      {!isSetupContext && vaultNeedsReseal && (
        <Alert className="bg-blue-50 border-blue-200">
          <Info className="h-4 w-4 text-blue-600" />
          <AlertDescription className="text-blue-900">
            <strong>Vault changes saved.</strong> Seal and re-unseal the vault to activate the new
            wallet configuration.
          </AlertDescription>
        </Alert>
      )}

      {/* Error Alert */}
      {apiError && (
        <Alert variant="destructive">
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>{apiError}</AlertDescription>
        </Alert>
      )}

      {/* Success Alert */}
      {successMessage && (
        <Alert>
          <CheckCircle className="h-4 w-4 text-green-600" />
          <AlertDescription className="text-green-600">{successMessage}</AlertDescription>
        </Alert>
      )}

      {/* Setup context requirement */}
      {isSetupContext && venues.length === 0 && (
        <Alert>
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>
            You must add at least one wallet to continue with setup.
          </AlertDescription>
        </Alert>
      )}

      {/* Venue List */}
      <VenueList
        venues={venues}
        onEdit={handleEdit}
        onDelete={handleDeleteVenue}
        onViewDetail={onViewDetail}
        loading={loading || apiLoading}
      />

      {/* Venue Form Dialog */}
      <VenueForm
        open={formOpen}
        onOpenChange={handleFormClose}
        onSubmit={handleFormSubmit}
        existingVenues={venues}
        editingVenue={editingVenue}
        isFirstVenue={venues.length === 0}
        loading={loading || apiLoading}
      />

      {/* Help Text */}
      <div className="text-sm text-gray-500 space-y-2">
        <p>
          <strong>Security:</strong> Your private keys are encrypted and never leave your browser
          in plain text.
        </p>
        <p>
          <strong>Primary Wallet:</strong> The default wallet used for bots without specific
          assignments. You can change the primary wallet at any time.
        </p>
        {!isSetupContext && (
          <p>
            <strong>Best Practice:</strong> Use separate wallets for testing and production
            environments.
          </p>
        )}
      </div>
    </div>
  );
}
