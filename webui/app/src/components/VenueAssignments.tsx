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
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from './ui/alert-dialog';
import { Alert, AlertDescription } from './ui/alert';
import { Plus, Trash2, RefreshCw, AlertCircle } from 'lucide-react';
import type { VenueRecord, VenueAssignmentWithBot, BotRecord, ListBotsResponse } from '../types/api';
import { BotAssignmentDialog } from './BotAssignmentDialog';
import { fetchVenueAssignments, assignBotToVenue, unassignBotFromVenue } from '../lib/venue-api';
import { buildOpsApiUrl } from '../config/opsApi';
import { toast } from 'sonner';

interface VenueAssignmentsProps {
  venue: VenueRecord;
  onRefresh?: () => void;
}

export function VenueAssignments({ venue, onRefresh }: VenueAssignmentsProps) {
  const [assignments, setAssignments] = useState<VenueAssignmentWithBot[]>([]);
  const [availableBots, setAvailableBots] = useState<BotRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [assignDialogOpen, setAssignDialogOpen] = useState(false);
  const [removeDialogOpen, setRemoveDialogOpen] = useState(false);
  const [assignmentToRemove, setAssignmentToRemove] = useState<VenueAssignmentWithBot | null>(
    null
  );

  useEffect(() => {
    loadAssignments();
  }, [venue.venue_id]);

  const loadAssignments = async () => {
    setLoading(true);
    try {
      // Load assignments for this venue
      const assignmentsData = await fetchVenueAssignments(venue.venue_id);

      // Load all bots to get names
      const botsResponse = await fetch(buildOpsApiUrl('/api/bots?limit=500'), {
        method: 'GET',
        credentials: 'include',
      });

      if (!botsResponse.ok) {
        throw new Error('Failed to load bots');
      }

      const botsData: ListBotsResponse = await botsResponse.json();

      // Merge bot info with assignments
      const enrichedAssignments = assignmentsData.map((assignment) => {
        const bot = botsData.items.find((b: BotRecord) => b.bot_id === assignment.bot_id);
        return {
          ...assignment,
          bot_name: bot?.payload.name || `Bot #${assignment.bot_id}`,
          bot_enabled: bot?.payload.is_enabled,
        };
      });

      setAssignments(enrichedAssignments);
      setAvailableBots(botsData.items);
    } catch (error) {
      console.error('Failed to load assignments:', error);
      toast.error('Failed to load bot assignments');
    } finally {
      setLoading(false);
    }
  };

  const handleAssignBot = async (botId: number, venueId: string, isPrimary: boolean) => {
    try {
      await assignBotToVenue(venueId, botId, isPrimary);

      await loadAssignments();
      onRefresh?.();
      toast.success('Bot assigned successfully');
    } catch (error) {
      toast.error('Failed to assign bot');
      throw error;
    }
  };

  const handleRemoveAssignment = async () => {
    if (!assignmentToRemove) return;

    try {
      await unassignBotFromVenue(venue.venue_id, assignmentToRemove.bot_id);

      await loadAssignments();
      onRefresh?.();
      toast.success('Assignment removed');
      setRemoveDialogOpen(false);
      setAssignmentToRemove(null);
    } catch {
      toast.error('Failed to remove assignment');
    }
  };

  const openRemoveDialog = (assignment: VenueAssignmentWithBot) => {
    setAssignmentToRemove(assignment);
    setRemoveDialogOpen(true);
  };

  // Filter out already assigned bots for the assignment dialog
  const unassignedBots = availableBots.filter(
    (bot) => !assignments.some((a) => a.bot_id === bot.bot_id)
  );

  return (
    <>
      <Card className="p-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-lg">Bot Assignments</h3>
            <p className="text-sm text-gray-600">
              {assignments.length} {assignments.length === 1 ? 'bot' : 'bots'} using this wallet
            </p>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={loadAssignments} disabled={loading}>
              <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
            </Button>
            <Button size="sm" onClick={() => setAssignDialogOpen(true)} disabled={loading}>
              <Plus className="mr-2 h-4 w-4" />
              Assign Bot
            </Button>
          </div>
        </div>

        {assignments.length === 0 ? (
          <Alert>
            <AlertCircle className="h-4 w-4" />
            <AlertDescription>
              No bots are currently assigned to this wallet. Assign bots to control which wallet
              executes their orders.
            </AlertDescription>
          </Alert>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Bot ID</TableHead>
                <TableHead>Bot Name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Assigned</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {assignments.map((assignment) => (
                <TableRow key={assignment.bot_id}>
                  <TableCell className="font-mono text-sm">#{assignment.bot_id}</TableCell>
                  <TableCell>
                    {assignment.bot_name || `Bot #${assignment.bot_id}`}
                  </TableCell>
                  <TableCell>
                    <Badge variant={assignment.bot_enabled ? 'default' : 'secondary'}>
                      {assignment.bot_enabled ? 'Enabled' : 'Disabled'}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={assignment.is_primary ? 'default' : 'outline'}>
                      {assignment.is_primary ? 'Primary' : 'Secondary'}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-sm text-gray-600">
                    {new Date(assignment.assigned_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => openRemoveDialog(assignment)}
                    >
                      <Trash2 className="h-4 w-4 text-red-600" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>

      {/* Assign Bot Dialog */}
      <BotAssignmentDialog
        open={assignDialogOpen}
        onOpenChange={setAssignDialogOpen}
        bot={unassignedBots[0] || availableBots[0]}
        venues={[venue]}
        currentVenueId={venue.venue_id}
        onAssign={handleAssignBot}
        loading={loading}
      />

      {/* Remove Assignment Dialog */}
      <AlertDialog open={removeDialogOpen} onOpenChange={setRemoveDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove Bot Assignment</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to remove{' '}
              <strong>{assignmentToRemove?.bot_name || `Bot #${assignmentToRemove?.bot_id}`}</strong>{' '}
              from this wallet?
            </AlertDialogDescription>
          </AlertDialogHeader>

          {assignmentToRemove?.is_primary && (
            <Alert>
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>
                This is the primary wallet for this bot. After removal, the bot will fall back to
                using the system default wallet.
              </AlertDescription>
            </Alert>
          )}

          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleRemoveAssignment} className="bg-red-600 hover:bg-red-700">
              Remove Assignment
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
