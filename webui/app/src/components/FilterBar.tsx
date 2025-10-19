import { useState, type MouseEvent } from 'react';
import type { OrderFilterState } from '../types/api';
import { Input } from './ui/input';
import { Button } from './ui/button';
import { Card } from './ui/card';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from './ui/collapsible';
import { X, ChevronDown, Filter } from 'lucide-react';
import { Badge } from './ui/badge';

interface FilterBarProps {
  filters: OrderFilterState;
  onFiltersChange: (filters: OrderFilterState) => void;
}

export function FilterBar({ filters, onFiltersChange }: FilterBarProps) {
  const [isOpen, setIsOpen] = useState(false);

  const updateFilter = (key: keyof OrderFilterState, value: string) => {
    onFiltersChange({
      ...filters,
      [key]: value || undefined
    });
  };

  const clearFilters = () => {
    onFiltersChange({});
  };

  const hasActiveFilters = Object.values(filters).some(v => v);
  const activeFilterCount = Object.values(filters).filter(v => v).length;

  return (
    <Card>
      <Collapsible open={isOpen} onOpenChange={setIsOpen}>
        <div className="p-3">
          <CollapsibleTrigger asChild>
            <div className="flex items-center justify-between cursor-pointer">
              <div className="flex items-center gap-2">
                <Filter className="h-4 w-4 text-gray-600" />
                <span className="text-gray-900">Advanced Filters</span>
                {activeFilterCount > 0 && (
                  <Badge variant="secondary" className="text-xs">{activeFilterCount} active</Badge>
                )}
              </div>
              <div className="flex items-center gap-2">
                {hasActiveFilters && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={(event: MouseEvent<HTMLButtonElement>) => {
                      event.stopPropagation();
                      clearFilters();
                    }}
                    className="h-7 text-xs"
                  >
                    <X className="h-3 w-3 mr-1" />
                    Clear
                  </Button>
                )}
                <ChevronDown className={`h-4 w-4 text-gray-400 transition-transform ${isOpen ? 'rotate-180' : ''}`} />
              </div>
            </div>
          </CollapsibleTrigger>
        </div>

        <CollapsibleContent>
          <div className="px-3 pb-3 border-t">
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3 pt-3">
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
                <label className="text-xs text-gray-600 block mb-1.5">Bot ID</label>
                <Input
                  type="number"
                  placeholder="12345"
                  value={filters.bot_id || ''}
                  onChange={(e) => updateFilter('bot_id', e.target.value)}
                  className="h-9 text-sm"
                />
              </div>

              <div>
                <label className="text-xs text-gray-600 block mb-1.5">Deal ID</label>
                <Input
                  type="number"
                  placeholder="67890"
                  value={filters.deal_id || ''}
                  onChange={(e) => updateFilter('deal_id', e.target.value)}
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
        </CollapsibleContent>
      </Collapsible>
    </Card>
  );
}
