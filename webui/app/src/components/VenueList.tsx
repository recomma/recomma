import { useState } from 'react';
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './ui/dropdown-menu';
import { Alert, AlertDescription } from './ui/alert';
import { Star, MoreVertical, Edit, Trash2, Copy, Check, AlertCircle } from 'lucide-react';
import type { VenueWithAssignments } from '../types/api';
import { truncateWalletAddress } from '../lib/venue-utils';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './ui/tooltip';

interface VenueListProps {
  venues: VenueWithAssignments[];
  onEdit: (venue: VenueWithAssignments) => void;
  onDelete: (venue: VenueWithAssignments) => void;
  onViewDetail?: (venue: VenueWithAssignments) => void;
  loading?: boolean;
}

export function VenueList({ venues, onEdit, onDelete, onViewDetail, loading }: VenueListProps) {
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [venueToDelete, setVenueToDelete] = useState<VenueWithAssignments | null>(null);
  const [copiedAddress, setCopiedAddress] = useState<string | null>(null);

  const handleDeleteClick = (venue: VenueWithAssignments) => {
    setVenueToDelete(venue);
    setDeleteDialogOpen(true);
  };

  const handleDeleteConfirm = () => {
    if (venueToDelete) {
      onDelete(venueToDelete);
      setDeleteDialogOpen(false);
      setVenueToDelete(null);
    }
  };

  const copyAddress = async (address: string) => {
    try {
      await navigator.clipboard.writeText(address);
      setCopiedAddress(address);
      setTimeout(() => setCopiedAddress(null), 2000);
    } catch (error) {
      console.error('Failed to copy address:', error);
    }
  };

  const getApiEndpointDisplay = (venue: VenueWithAssignments): string => {
    const apiUrl = venue.flags?.api_url as string;
    if (!apiUrl) return 'Mainnet';
    if (apiUrl.includes('hyperliquid.xyz')) return 'Mainnet';
    if (apiUrl.includes('testnet')) return 'Testnet';
    return 'Custom';
  };

  if (venues.length === 0) {
    return (
      <Card className="p-12 text-center">
        <div className="flex flex-col items-center space-y-4">
          <div className="rounded-full bg-gray-100 p-4">
            <Star className="h-8 w-8 text-gray-400" />
          </div>
          <div>
            <h3 className="text-lg">No Wallets Configured</h3>
            <p className="text-sm text-gray-500 mt-1">
              Add your first Hyperliquid wallet to get started
            </p>
          </div>
        </div>
      </Card>
    );
  }

  const canDelete = venues.length > 1;

  return (
    <>
      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Wallet Address</TableHead>
              <TableHead>API Endpoint</TableHead>
              <TableHead>Bots</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {venues.map((venue) => (
              <TableRow key={venue.venue_id}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <span>{venue.display_name}</span>
                    {venue.isPrimary && (
                      <TooltipProvider>
                        <Tooltip>
                          <TooltipTrigger>
                            <Badge variant="default" className="gap-1">
                              <Star className="h-3 w-3 fill-current" />
                              Primary
                            </Badge>
                          </TooltipTrigger>
                          <TooltipContent>
                            <p>This is the default wallet for bots without specific assignments</p>
                          </TooltipContent>
                        </Tooltip>
                      </TooltipProvider>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <TooltipProvider>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="font-mono text-sm cursor-help">
                            {truncateWalletAddress(venue.wallet)}
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>
                          <p className="font-mono text-xs">{venue.wallet}</p>
                        </TooltipContent>
                      </Tooltip>
                    </TooltipProvider>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 w-6 p-0"
                      onClick={() => copyAddress(venue.wallet)}
                    >
                      {copiedAddress === venue.wallet ? (
                        <Check className="h-3 w-3 text-green-500" />
                      ) : (
                        <Copy className="h-3 w-3" />
                      )}
                    </Button>
                  </div>
                </TableCell>
                <TableCell>
                  <Badge variant="outline">{getApiEndpointDisplay(venue)}</Badge>
                </TableCell>
                <TableCell>
                  <span className="text-sm text-gray-600">
                    {venue.assignmentCount || 0} {venue.assignmentCount === 1 ? 'bot' : 'bots'}
                  </span>
                </TableCell>
                <TableCell className="text-right">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="sm" className="h-8 w-8 p-0">
                        <MoreVertical className="h-4 w-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      {onViewDetail && (
                        <DropdownMenuItem onClick={() => onViewDetail(venue)}>
                          View Details
                        </DropdownMenuItem>
                      )}
                      <DropdownMenuItem onClick={() => onEdit(venue)}>
                        <Edit className="mr-2 h-4 w-4" />
                        Edit
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onClick={() => handleDeleteClick(venue)}
                        disabled={!canDelete || loading}
                        className="text-red-600"
                      >
                        <Trash2 className="mr-2 h-4 w-4" />
                        Delete
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>

      {/* Delete Confirmation Dialog */}
      <AlertDialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Wallet</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete <strong>{venueToDelete?.display_name}</strong>?
            </AlertDialogDescription>
          </AlertDialogHeader>

          <div className="space-y-3">
            {venueToDelete && (
              <>
                {venueToDelete.assignmentCount && venueToDelete.assignmentCount > 0 && (
                  <Alert>
                    <AlertCircle className="h-4 w-4" />
                    <AlertDescription>
                      <strong>{venueToDelete.assignmentCount}</strong>{' '}
                      {venueToDelete.assignmentCount === 1 ? 'bot is' : 'bots are'} currently
                      assigned to this wallet. They will be unassigned and fall back to the
                      primary wallet.
                    </AlertDescription>
                  </Alert>
                )}

                {venueToDelete.isPrimary && venues.length > 1 && (
                  <Alert>
                    <AlertCircle className="h-4 w-4" />
                    <AlertDescription>
                      This is your primary wallet. Another wallet will be automatically promoted
                      to primary.
                    </AlertDescription>
                  </Alert>
                )}

                {venues.length === 1 && (
                  <Alert variant="destructive">
                    <AlertCircle className="h-4 w-4" />
                    <AlertDescription>
                      You must have at least one wallet configured. Deletion is not allowed.
                    </AlertDescription>
                  </Alert>
                )}
              </>
            )}

            <p className="text-sm text-gray-600">
              Historical orders will remain intact and will still show the wallet address, even
              though the wallet configuration will be removed.
            </p>
          </div>

          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDeleteConfirm}
              className="bg-red-600 hover:bg-red-700"
              disabled={venues.length === 1}
            >
              Delete Wallet
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
