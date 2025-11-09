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
import { VenueRecord, VenueAssignmentWithBot, BotRecord } from '../types/api';
import { BotAssignmentDialog } from './BotAssignmentDialog';
import { toast } from 'sonner@2.0.3';

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
      const assignmentsResponse = await mockApiCall(
        `/api/venues/${venue.venue_id}/assignments`,
        'GET'
      );
      const assignmentsData = await assignmentsResponse.json();

      // Load all bots to get names
      const botsResponse = await mockApiCall('/api/bots?limit=500', 'GET');
      const botsData = await botsResponse.json();

      // Merge bot info with assignments
      const enrichedAssignments = assignmentsData.items.map((assignment: any) => {
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
      const response = await mockApiCall(
        `/api/venues/${venueId}/assignments/${botId}`,
        'PUT',
        { is_primary: isPrimary }
      );

      if (!response.ok) {
        throw new Error('Failed to assign bot');
      }

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
      const response = await mockApiCall(
        `/api/venues/${venue.venue_id}/assignments/${assignmentToRemove.bot_id}`,
        'DELETE'
      );

      if (!response.ok) {
        throw new Error('Failed to remove assignment');
      }

      await loadAssignments();
      onRefresh?.();
      toast.success('Assignment removed');
      setRemoveDialogOpen(false);
      setAssignmentToRemove(null);
    } catch (error) {
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

// Mock API call - replace with actual implementation
async function mockApiCall(url: string, method: string, body?: any): Promise<Response> {
  await new Promise((resolve) => setTimeout(resolve, 500));

  // Mock data based on endpoint
  if (url.includes('/assignments') && method === 'GET') {
    return new Response(
      JSON.stringify({
        items: [
          {
            bot_id: 12345,
            venue_id: 'hyperliquid:main-trading',
            is_primary: true,
            assigned_at: '2025-01-15T10:30:00Z',
          },
          {
            bot_id: 67890,
            venue_id: 'hyperliquid:main-trading',
            is_primary: true,
            assigned_at: '2025-01-15T11:00:00Z',
          },
        ],
      }),
      { status: 200 }
    );
  }

  if (url.includes('/api/bots') && method === 'GET') {
    return new Response(
      JSON.stringify({
        items: [
          {
            bot_id: 12345,
            last_synced_at: '2025-01-15T12:00:00Z',
            payload: {
              id: 12345,
              name: 'ETH Scalper Bot',
              is_enabled: true,
              account_name: 'My 3Commas Account',
            },
          },
          {
            bot_id: 67890,
            last_synced_at: '2025-01-15T12:00:00Z',
            payload: {
              id: 67890,
              name: 'BTC DCA Bot',
              is_enabled: true,
              account_name: 'My 3Commas Account',
            },
          },
          {
            bot_id: 11111,
            last_synced_at: '2025-01-15T12:00:00Z',
            payload: {
              id: 11111,
              name: 'SOL Swing Trader',
              is_enabled: false,
              account_name: 'My 3Commas Account',
            },
          },
        ],
      }),
      { status: 200 }
    );
  }

  return new Response(JSON.stringify({}), { status: 200 });
}
