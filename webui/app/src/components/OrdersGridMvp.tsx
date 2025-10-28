import { useId } from 'react';
import {
  Grid,
  useClientRowDataSource,
} from '@1771technologies/lytenyte-core';
import type { Column } from '@1771technologies/lytenyte-core/types';

type OrderRow = {
  id: string;
  bot: string;
  deal: string;
  side: 'Buy' | 'Sell';
  coin: string;
  price: number;
  status: 'Open' | 'Filled' | 'Cancelled';
};

const sampleOrders: OrderRow[] = [
  {
    id: 'ORD-1001',
    bot: 'Mean Reversion v2',
    deal: 'DL-3811',
    side: 'Buy',
    coin: 'ETH-PERP',
    price: 3281.45,
    status: 'Filled',
  },
  {
    id: 'ORD-1002',
    bot: 'Momentum Scout',
    deal: 'DL-4127',
    side: 'Sell',
    coin: 'BTC-PERP',
    price: 64125.1,
    status: 'Open',
  },
  {
    id: 'ORD-1003',
    bot: 'Grid Trader',
    deal: 'DL-3902',
    side: 'Buy',
    coin: 'SOL-PERP',
    price: 158.32,
    status: 'Cancelled',
  },
  {
    id: 'ORD-1004',
    bot: 'Mean Reversion v2',
    deal: 'DL-3812',
    side: 'Sell',
    coin: 'ETH-PERP',
    price: 3294.67,
    status: 'Open',
  },
];

const columns: Column<OrderRow>[] = [
  { id: 'id', name: 'Order ID', width: 140 },
  { id: 'bot', name: 'Bot', width: 200 },
  { id: 'deal', name: 'Deal', width: 140 },
  { id: 'side', name: 'Side', width: 120 },
  { id: 'coin', name: 'Market', width: 160 },
  { id: 'price', name: 'Price', type: 'number', width: 140 },
  { id: 'status', name: 'Status', width: 140 },
];

export function OrdersGridMvp() {
  const dataSource = useClientRowDataSource<OrderRow>({
    data: sampleOrders,
  });

  const grid = Grid.useLyteNyte({
    gridId: useId(),
    rowDataSource: dataSource,
    columns,
  });

  const view = grid.view.useValue();

  return (
    <div className="lng-grid" style={{ width: '100%', height: 400 }}>
      <Grid.Root grid={grid}>
        <Grid.Viewport>
          <Grid.Header>
            {view.header.layout.map((row, rowIndex) => (
              <Grid.HeaderRow headerRowIndex={rowIndex} key={rowIndex}>
                {row.map(cell => {
                  if (cell.kind === 'group') {
                    return (
                      <Grid.HeaderGroupCell
                        cell={cell}
                        key={cell.idOccurrence}
                      />
                    );
                  }

                  return (
                    <Grid.HeaderCell
                      cell={cell}
                      key={cell.column.id}
                      className="flex h-full items-center px-2"
                    />
                  );
                })}
              </Grid.HeaderRow>
            ))}
          </Grid.Header>
          <Grid.RowsContainer>
            <Grid.RowsCenter>
              {view.rows.center.map(row => {
                if (row.kind === 'full-width') {
                  return <Grid.RowFullWidth key={row.id} row={row} />;
                }

                return (
                  <Grid.Row key={row.id} row={row} accepted={['row']}>
                    {row.cells.map(cell => (
                      <Grid.Cell cell={cell} key={cell.id} />
                    ))}
                  </Grid.Row>
                );
              })}
            </Grid.RowsCenter>
          </Grid.RowsContainer>
        </Grid.Viewport>
      </Grid.Root>
    </div>
  );
}
