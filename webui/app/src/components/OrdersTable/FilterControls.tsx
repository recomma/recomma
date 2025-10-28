import { useState } from 'react';
import { Button } from '../ui/button';
import { Badge } from '../ui/badge';
import { Bot, Activity, Filter } from 'lucide-react';
import { BotsFilter } from './filters/BotsFilter';
import { DealsFilter } from './filters/DealsFilter';
import { AdvancedFilter } from './filters/AdvancedFilter';
import type { OrderFilterState } from '../../types/api';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';

interface FilterControlsProps {
  selectedBotId?: number;
  selectedDealId?: number;
  onBotSelect: (botId: number | undefined) => void;
  onDealSelect: (dealId: number | undefined) => void;
  filters: OrderFilterState;
  onFiltersChange: (filters: OrderFilterState) => void;
}

export function FilterControls({
  selectedBotId,
  selectedDealId,
  onBotSelect,
  onDealSelect,
  filters,
  onFiltersChange,
}: FilterControlsProps) {
  const [botsOpen, setBotsOpen] = useState(false);
  const [dealsOpen, setDealsOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  // Count active advanced filters (excluding bot_id and deal_id which have their own buttons)
  const activeFilterCount = Object.entries(filters).filter(
    ([key, value]) => value && key !== 'bot_id' && key !== 'deal_id'
  ).length;

  return (
    <div
      role="group"
      className="inline-flex shrink-0 items-stretch divide-x divide-border overflow-hidden rounded-md border border-border bg-background shadow-xs"
    >
      {/* Bots Button */}
      <Popover open={botsOpen} onOpenChange={setBotsOpen} modal={false}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 rounded-none border-0 px-3 text-xs shadow-none focus-visible:z-10 data-[state=open]:bg-accent data-[state=open]:text-accent-foreground"
          >
            <Bot className="h-3.5 w-3.5 mr-1" />
            Bots
            {selectedBotId && (
              <Badge variant="secondary" className="ml-1.5 h-4 px-1 text-[10px]">
                1
              </Badge>
            )}
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="start"
          sideOffset={8}
          className="w-[800px] p-0 border-gray-200"
        >
          <BotsFilter
            selectedBotId={selectedBotId}
            onBotSelect={(botId) => {
              onBotSelect(botId);
              if (!botId) {
                onDealSelect(undefined);
              }
            }}
            onDealSelect={onDealSelect}
          />
        </PopoverContent>
      </Popover>

      {/* Deals Button */}
      <Popover open={dealsOpen} onOpenChange={setDealsOpen} modal={false}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 rounded-none border-0 px-3 text-xs shadow-none focus-visible:z-10 data-[state=open]:bg-accent data-[state=open]:text-accent-foreground"
          >
            <Activity className="h-3.5 w-3.5 mr-1" />
            Deals
            {selectedDealId && (
              <Badge variant="secondary" className="ml-1.5 h-4 px-1 text-[10px]">
                1
              </Badge>
            )}
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="start"
          sideOffset={8}
          className="w-[750px] p-0 border-gray-200"
        >
          <DealsFilter
            selectedBotId={selectedBotId}
            selectedDealId={selectedDealId}
            onBotSelect={onBotSelect}
            onDealSelect={onDealSelect}
          />
        </PopoverContent>
      </Popover>

      {/* Advanced Filters Button */}
      <Popover open={advancedOpen} onOpenChange={setAdvancedOpen} modal={false}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 rounded-none border-0 px-3 text-xs shadow-none focus-visible:z-10 data-[state=open]:bg-accent data-[state=open]:text-accent-foreground"
          >
            <Filter className="h-3.5 w-3.5 mr-1" />
            Advanced
            {activeFilterCount > 0 && (
              <Badge variant="secondary" className="ml-1.5 h-4 px-1 text-[10px]">
                {activeFilterCount}
              </Badge>
            )}
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="start"
          sideOffset={8}
          className="w-[550px] p-0 border-gray-200"
        >
          <AdvancedFilter
            filters={filters}
            onFiltersChange={onFiltersChange}
          />
        </PopoverContent>
      </Popover>
    </div>
  );
}
