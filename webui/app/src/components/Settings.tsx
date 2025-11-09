import { useState } from 'react';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';
import { Button } from './ui/button';
import { ArrowLeft } from 'lucide-react';
import { VenueManagement } from './VenueManagement';
import { BotAssignmentManager } from './BotAssignmentManager';
import type { VenueRecord, VenueWithAssignments } from '../types/api';

interface SettingsProps {
  onBack?: () => void;
}

export function Settings({ onBack }: SettingsProps) {
  const [venues, setVenues] = useState<VenueRecord[]>([]);

  const handleVenuesLoaded = (loadedVenues: VenueWithAssignments[]) => {
    setVenues(loadedVenues);
  };

  return (
    <div className="min-h-screen bg-gray-50">
      {/* Header */}
      <div className="border-b bg-white shadow-sm">
        <div className="container mx-auto px-4 py-3">
          <div className="flex items-center gap-3">
            {onBack && (
              <Button variant="ghost" size="sm" onClick={onBack}>
                <ArrowLeft className="h-4 w-4 mr-2" />
                Back
              </Button>
            )}
            <img
              src="/favicon.svg"
              alt="Recomma logo"
              className="h-8 w-8"
            />
            <h1 className="text-gray-900">Settings</h1>
          </div>
        </div>
      </div>

      {/* Content */}
      <div className="container mx-auto px-4 py-6">
        <Card>
          <CardHeader>
            <CardTitle>Application Settings</CardTitle>
            <CardDescription>
              Manage your wallets, bot assignments, and other configuration
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Tabs defaultValue="venues" className="w-full">
              <TabsList className="grid w-full grid-cols-2">
                <TabsTrigger value="venues">Wallets</TabsTrigger>
                <TabsTrigger value="bots">Bot Assignments</TabsTrigger>
              </TabsList>

              <TabsContent value="venues" className="mt-6">
                <VenueManagement
                  context="settings"
                  onVenuesLoaded={handleVenuesLoaded}
                />
              </TabsContent>

              <TabsContent value="bots" className="mt-6">
                <BotAssignmentManager venues={venues} />
              </TabsContent>
            </Tabs>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
