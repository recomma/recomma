import { useState, useEffect } from 'react';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Button } from './ui/button';
import { Label } from './ui/label';
import { RadioGroup, RadioGroupItem } from './ui/radio-group';
import { Alert, AlertDescription } from './ui/alert';
import { Info, Loader2 } from 'lucide-react';
import type { VenueRecord, BotRecord } from '../types/api';
import { truncateWalletAddress } from '../lib/venue-utils';

interface BotAssignmentDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  bot: BotRecord;
  venues: VenueRecord[];
  currentVenueId?: string;
  onAssign: (botId: number, venueId: string, isPrimary: boolean) => Promise<void>;
  loading?: boolean;
}

export function BotAssignmentDialog({
  open,
  onOpenChange,
  bot,
  venues,
  currentVenueId,
  onAssign,
}: BotAssignmentDialogProps) {
  const [selectedVenueId, setSelectedVenueId] = useState<string>('');
  const [isPrimary, setIsPrimary] = useState(true);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) {
      setSelectedVenueId(currentVenueId || venues[0]?.venue_id || '');
      setIsPrimary(true);
    }
  }, [open, currentVenueId, venues]);

  const handleSubmit = async () => {
    if (!selectedVenueId) return;

    setSubmitting(true);
    try {
      await onAssign(bot.bot_id, selectedVenueId, isPrimary);
      onOpenChange(false);
    } catch (error) {
      console.error('Failed to assign bot:', error);
    } finally {
      setSubmitting(false);
    }
  };

  if (venues.length === 0) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>No Wallets Available</DialogTitle>
            <DialogDescription>
              You need to configure at least one wallet before assigning bots.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button onClick={() => onOpenChange(false)}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Assign Bot to Wallet</DialogTitle>
          <DialogDescription>
            Choose which wallet should execute orders for this bot
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-4">
          {/* Bot Info */}
          <div className="p-3 bg-gray-50 rounded-lg">
            <div className="text-sm">
              <strong>Bot:</strong> {bot.payload.name}
            </div>
            <div className="text-sm text-gray-600">
              ID: #{bot.bot_id} • {bot.payload.is_enabled ? 'Enabled' : 'Disabled'}
            </div>
          </div>

          {/* Wallet Selection */}
          <div className="space-y-3">
            <Label>Select Wallet</Label>
            <RadioGroup value={selectedVenueId} onValueChange={setSelectedVenueId}>
              {venues.map((venue) => (
                <div
                  key={venue.venue_id}
                  className="flex items-center space-x-3 p-3 border rounded-lg hover:bg-gray-50"
                >
                  <RadioGroupItem value={venue.venue_id} id={venue.venue_id} />
                  <Label htmlFor={venue.venue_id} className="flex-1 cursor-pointer">
                    <div className="font-medium">{venue.display_name}</div>
                    <div className="text-sm text-gray-600 font-mono">
                      {truncateWalletAddress(venue.wallet)}
                    </div>
                  </Label>
                </div>
              ))}
            </RadioGroup>
          </div>

          {/* Primary Assignment Info */}
          <Alert>
            <Info className="h-4 w-4" />
            <AlertDescription className="text-sm">
              {isPrimary ? (
                <>
                  This wallet will become the <strong>primary wallet</strong> for this bot.
                  Future orders will execute using the selected wallet.
                </>
              ) : (
                <>
                  This will add a secondary assignment. The bot will continue using its current
                  primary wallet for orders.
                </>
              )}
            </AlertDescription>
          </Alert>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!selectedVenueId || submitting}>
            {submitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            {submitting ? 'Assigning...' : 'Assign Wallet'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
