import { Card } from './ui/card';
import { Button } from './ui/button';
import { Badge } from './ui/badge';
import { Separator } from './ui/separator';
import { Copy, Check, ArrowLeft, Star } from 'lucide-react';
import type { VenueRecord } from '../types/api';
import { truncateWalletAddress } from '../lib/venue-utils';
import { VenueAssignments } from './VenueAssignments';
import { useState } from 'react';

interface VenueDetailProps {
  venue: VenueRecord & { isPrimary?: boolean };
  onBack: () => void;
  onRefresh?: () => void;
}

export function VenueDetail({ venue, onBack, onRefresh }: VenueDetailProps) {
  const [copiedAddress, setCopiedAddress] = useState(false);

  const copyAddress = async () => {
    try {
      await navigator.clipboard.writeText(venue.wallet);
      setCopiedAddress(true);
      setTimeout(() => setCopiedAddress(false), 2000);
    } catch (error) {
      console.error('Failed to copy address:', error);
    }
  };

  const getApiEndpointDisplay = (): string => {
    const apiUrl = venue.flags?.api_url as string;
    if (!apiUrl) return 'Mainnet';
    if (apiUrl.includes('hyperliquid.xyz') && !apiUrl.includes('testnet')) return 'Mainnet';
    if (apiUrl.includes('testnet')) return 'Testnet';
    return 'Custom';
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center gap-4">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="mr-2 h-4 w-4" />
          Back to Wallets
        </Button>
      </div>

      {/* Venue Info Card */}
      <Card className="p-6">
        <div className="space-y-4">
          <div className="flex items-start justify-between">
            <div>
              <div className="flex items-center gap-3">
                <h2 className="text-2xl">{venue.display_name}</h2>
                {venue.isPrimary && (
                  <Badge variant="default" className="gap-1">
                    <Star className="h-3 w-3 fill-current" />
                    Primary
                  </Badge>
                )}
              </div>
              <p className="text-sm text-gray-600 mt-1">Venue ID: {venue.venue_id}</p>
            </div>
            <Badge variant="outline">{getApiEndpointDisplay()}</Badge>
          </div>

          <Separator />

          <div className="grid md:grid-cols-2 gap-6">
            <div>
              <h3 className="text-sm mb-2 text-gray-600">Wallet Address</h3>
              <div className="flex items-center gap-2">
                <code className="flex-1 p-3 bg-gray-50 rounded border font-mono text-sm break-all">
                  {venue.wallet}
                </code>
                <Button variant="outline" size="sm" onClick={copyAddress}>
                  {copiedAddress ? (
                    <Check className="h-4 w-4 text-green-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                </Button>
              </div>
            </div>

            <div>
              <h3 className="text-sm mb-2 text-gray-600">API Endpoint</h3>
              <div className="p-3 bg-gray-50 rounded border">
                <code className="text-sm break-all">
                  {(venue.flags?.api_url as string) || 'https://api.hyperliquid.xyz'}
                </code>
              </div>
            </div>
          </div>
        </div>
      </Card>

      {/* Bot Assignments */}
      <VenueAssignments venue={venue} onRefresh={onRefresh} />
    </div>
  );
}
