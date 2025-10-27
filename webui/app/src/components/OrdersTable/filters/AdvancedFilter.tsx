import type { OrderFilterState } from '../../../types/api';
import { Input } from '../../ui/input';
import { Button } from '../../ui/button';
import { Badge } from '../../ui/badge';
import { Filter, X } from 'lucide-react';

interface AdvancedFilterProps {
  filters: OrderFilterState;
  onFiltersChange: (filters: OrderFilterState) => void;
}

export function AdvancedFilter({ filters, onFiltersChange }: AdvancedFilterProps) {
  const updateFilter = (key: keyof OrderFilterState, value: string) => {
    onFiltersChange({
      ...filters,
      [key]: value || undefined
    });
  };

  const clearFilters = () => {
    // Clear all except bot_id and deal_id which are managed by the Bots/Deals popovers
    onFiltersChange({
      bot_id: filters.bot_id,
      deal_id: filters.deal_id,
    });
  };

  // Count active filters (excluding bot_id and deal_id which are shown elsewhere)
  const activeFilterCount = Object.entries(filters).filter(
    ([key, value]) => value && key !== 'bot_id' && key !== 'deal_id'
  ).length;

  return (
    <div>
      <div className="p-3 border-b">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Filter className="h-4 w-4 text-gray-600" />
            <span className="text-sm font-semibold">Advanced Filters</span>
            {activeFilterCount > 0 && (
              <Badge variant="secondary" className="text-xs">{activeFilterCount} active</Badge>
            )}
          </div>
          {activeFilterCount > 0 && (
            <Button
              variant="ghost"
              size="sm"
              onClick={clearFilters}
              className="h-7 px-2 text-xs"
            >
              <X className="h-3 w-3 mr-1" />
              Clear
            </Button>
          )}
        </div>
      </div>

      <div className="p-3">
        <div className="grid grid-cols-1 gap-3">
          <div>
            <label className="text-xs text-gray-600 block mb-1.5">Metadata Hex</label>
            <Input
              placeholder="0x..."
              value={filters.metadata || ''}
              onChange={(e) => updateFilter('metadata', e.target.value)}
              className="h-9 text-sm"
            />
          </div>

          <div>
            <label className="text-xs text-gray-600 block mb-1.5">Bot Event ID</label>
            <Input
              type="number"
              placeholder="11223"
              value={filters.bot_event_id || ''}
              onChange={(e) => updateFilter('bot_event_id', e.target.value)}
              className="h-9 text-sm"
            />
          </div>

          <div>
            <label className="text-xs text-gray-600 block mb-1.5">From Date</label>
            <Input
              type="datetime-local"
              value={filters.observed_from || ''}
              onChange={(e) => updateFilter('observed_from', e.target.value)}
              className="h-9 text-sm"
            />
          </div>

          <div>
            <label className="text-xs text-gray-600 block mb-1.5">To Date</label>
            <Input
              type="datetime-local"
              value={filters.observed_to || ''}
              onChange={(e) => updateFilter('observed_to', e.target.value)}
              className="h-9 text-sm"
            />
          </div>
        </div>
      </div>
    </div>
  );
}
