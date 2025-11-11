import { Card } from './ui/card';
import { Alert, AlertDescription } from './ui/alert';
import { Info } from 'lucide-react';
import { BotList } from './BotList';
import type { VenueRecord } from '../types/api';

interface BotAssignmentManagerProps {
  venues: VenueRecord[];
}

export function BotAssignmentManager({ venues }: BotAssignmentManagerProps) {
  return (
    <div className="space-y-6">
      {/* Info Alert */}
      <Alert>
        <Info className="h-4 w-4" />
        <AlertDescription>
          <strong>Bot Assignment</strong> lets you control which wallet each bot uses for trading.
          Bots without assignments use the system default wallet.
        </AlertDescription>
      </Alert>

      {/* Bot List View */}
      <BotList venues={venues} />

      {/* Usage Guide */}
      <Card className="p-6 bg-blue-50 border-blue-200">
        <h3 className="text-lg mb-3">How Bot Assignments Work</h3>
        <div className="text-sm space-y-2 text-gray-700">
          <div>
            <strong>Primary Assignment:</strong> The wallet that executes orders for a bot. Each
            bot can have one primary wallet (or none = uses system default).
          </div>
          <div>
            <strong>Default Behavior:</strong> New bots automatically use the primary wallet from
            your wallet list until you assign them to a specific wallet.
          </div>
          <div>
            <strong>Changing Assignments:</strong> Click the edit icon next to any bot to change
            which wallet it uses. The change takes effect immediately for future orders.
          </div>
          <div>
            <strong>Multiple Wallets:</strong> You can configure multiple wallets and assign
            different bots to different wallets (e.g., production vs test bots).
          </div>
        </div>
      </Card>
    </div>
  );
}
