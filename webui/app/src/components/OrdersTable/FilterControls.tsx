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
    <>
      {/* Bots Button */}
      <Popover open={botsOpen} onOpenChange={setBotsOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 px-2 text-xs"
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
      <Popover open={dealsOpen} onOpenChange={setDealsOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 px-2 text-xs"
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
      <Popover open={advancedOpen} onOpenChange={setAdvancedOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 px-2 text-xs"
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
    </>
  );
}
