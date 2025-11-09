import { useState, useEffect } from 'react';
import { Card } from './ui/card';
import { Button } from './ui/button';
import { Badge } from './ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from './ui/table';
import { Alert, AlertDescription } from './ui/alert';
import { RefreshCw, Edit, AlertCircle, Bot as BotIcon } from 'lucide-react';
import type { BotWithVenue, VenueRecord, BotRecord, ListBotsResponse } from '../types/api';
import { truncateWalletAddress } from '../lib/venue-utils';
import { BotAssignmentDialog } from './BotAssignmentDialog';
import { fetchBotVenues, assignBotToVenue } from '../lib/venue-api';
import { buildOpsApiUrl } from '../config/opsApi';
import { toast } from 'sonner';

interface BotListProps {
  venues: VenueRecord[];
}

export function BotList({ venues }: BotListProps) {
  const [bots, setBots] = useState<BotWithVenue[]>([]);
  const [loading, setLoading] = useState(false);
  const [assignDialogOpen, setAssignDialogOpen] = useState(false);
  const [selectedBot, setSelectedBot] = useState<BotRecord | null>(null);

  useEffect(() => {
    loadBots();
  }, []);

  const loadBots = async () => {
    setLoading(true);
    try {
      // Load all bots
      const botsResponse = await fetch(buildOpsApiUrl('/api/bots?limit=500'), {
        method: 'GET',
        credentials: 'include',
      });

      if (!botsResponse.ok) {
        throw new Error('Failed to load bots');
      }

      const botsData: ListBotsResponse = await botsResponse.json();

      // Load venue assignments for each bot
      const botsWithVenues = await Promise.all(
        botsData.items.map(async (bot: BotRecord) => {
          try {
            const venuesData = await fetchBotVenues(bot.bot_id);

            const primaryVenue = venuesData.find((v) => v.is_primary);
            return {
              ...bot,
              primaryVenue,
              allVenues: venuesData,
            };
          } catch {
            return {
              ...bot,
              primaryVenue: undefined,
              allVenues: [],
            };
          }
        })
      );

      setBots(botsWithVenues);
    } catch (error) {
      console.error('Failed to load bots:', error);
      toast.error('Failed to load bots');
    } finally {
      setLoading(false);
    }
  };

  const handleAssignBot = async (botId: number, venueId: string, isPrimary: boolean) => {
    try {
      await assignBotToVenue(venueId, botId, isPrimary);

      await loadBots();
      toast.success('Bot wallet updated');
    } catch (error) {
      toast.error('Failed to update bot wallet');
      throw error;
    }
  };

  const openAssignDialog = (bot: BotRecord) => {
    setSelectedBot(bot);
    setAssignDialogOpen(true);
  };

  const getVenueDisplay = (bot: BotWithVenue) => {
    if (!bot.primaryVenue) {
      return { name: 'Default Wallet', wallet: '', isDefault: true };
    }

    // Find the venue in the venues list to get the display name
    const venue = venues.find((v) => v.venue_id === bot.primaryVenue?.venue_id);
    return {
      name: venue?.display_name || bot.primaryVenue.venue_id,
      wallet: bot.primaryVenue.wallet,
      isDefault: false,
    };
  };

  return (
    <>
      <Card className="p-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h2 className="text-2xl">Bot Management</h2>
            <p className="text-sm text-gray-600 mt-1">
              Configure which wallet each bot uses for trading
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={loadBots} disabled={loading}>
            <RefreshCw className={`mr-2 h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
            Refresh
          </Button>
        </div>

        {bots.length === 0 ? (
          <Alert>
            <AlertCircle className="h-4 w-4" />
            <AlertDescription>
              No bots found. Make sure your 3Commas integration is configured and you have active
              bots.
            </AlertDescription>
          </Alert>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Bot</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Primary Wallet</TableHead>
                <TableHead>Wallet Address</TableHead>
                <TableHead>Assignments</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {bots.map((bot) => {
                const venueDisplay = getVenueDisplay(bot);
                return (
                  <TableRow key={bot.bot_id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <BotIcon className="h-4 w-4 text-gray-400" />
                        <div>
                          <div>{bot.payload.name}</div>
                          <div className="text-xs text-gray-500">#{bot.bot_id}</div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant={bot.payload.is_enabled ? 'default' : 'secondary'}>
                        {bot.payload.is_enabled ? 'Enabled' : 'Disabled'}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        {venueDisplay.name}
                        {venueDisplay.isDefault && (
                          <Badge variant="outline" className="text-xs">
                            System Default
                          </Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      {venueDisplay.wallet ? (
                        <span className="font-mono text-sm">
                          {truncateWalletAddress(venueDisplay.wallet)}
                        </span>
                      ) : (
                        <span className="text-sm text-gray-400">Not assigned</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <span className="text-sm text-gray-600">
                        {bot.allVenues?.length || 0}{' '}
                        {bot.allVenues?.length === 1 ? 'wallet' : 'wallets'}
                      </span>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => openAssignDialog(bot)}
                        disabled={venues.length === 0}
                      >
                        <Edit className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}

        {venues.length === 0 && (
          <Alert className="mt-4">
            <AlertCircle className="h-4 w-4" />
            <AlertDescription>
              You need to configure at least one wallet before assigning bots. Go to Wallet
              Management to add a wallet.
            </AlertDescription>
          </Alert>
        )}
      </Card>

      {/* Bot Assignment Dialog */}
      {selectedBot && (
        <BotAssignmentDialog
          open={assignDialogOpen}
          onOpenChange={setAssignDialogOpen}
          bot={selectedBot}
          venues={venues}
          currentVenueId={
            bots.find((b) => b.bot_id === selectedBot.bot_id)?.primaryVenue?.venue_id
          }
          onAssign={handleAssignBot}
          loading={loading}
        />
      )}
    </>
  );
}
