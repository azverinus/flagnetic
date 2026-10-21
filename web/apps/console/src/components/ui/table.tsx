import { Trans } from "@lingui/react/macro";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  type RowData,
  useReactTable,
} from "@tanstack/react-table";
import type { ReactNode } from "react";

import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/cn";

export type TableProps<T extends RowData> = {
  columns: ColumnDef<T, unknown>[];
  data: readonly T[];
  /** What the table lists, for a screen reader; shown above it unless hidden. */
  caption: ReactNode;
  hideCaption?: boolean;
  /** Keeps the first column, the row's name, in view while the rest scrolls. */
  stickyFirstColumn?: boolean;
  loading?: boolean;
  /** Shown in place of the rows when there are none. */
  empty?: ReactNode;
  getRowId?: (row: T, index: number) => string;
  className?: string;
};

/**
 * The first column held in view: above the scrolled cells, on the surface
 * they pass under.
 */
const stickyClassName = "sticky left-0 z-[1] bg-background";

/** How many placeholder rows a loading table shows. */
const loadingRows = 3;

/**
 * A table of rows on TanStack Table. It scrolls sideways inside its own
 * positioned wrapper, so neither a wide row nor a long caption can widen
 * the page; while loading it shows placeholder rows and says it is busy,
 * and without rows it shows what would be here.
 */
export function Table<T extends RowData>({
  columns,
  data,
  caption,
  hideCaption = false,
  stickyFirstColumn = false,
  loading = false,
  empty,
  getRowId,
  className,
}: TableProps<T>) {
  // The table instance changes identity on every render by design; the
  // console is not compiled with the React Compiler, which is what the rule
  // guards against memoizing it.
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    columns,
    data: data as T[],
    getCoreRowModel: getCoreRowModel(),
    getRowId,
  });
  const sticky = (index: number) =>
    stickyFirstColumn && index === 0 ? stickyClassName : undefined;
  const width = table.getVisibleLeafColumns().length;

  return (
    <div className={cn("relative w-full max-w-full min-w-0 overflow-x-auto", className)}>
      <table aria-busy={loading || undefined} className="w-full border-collapse text-sm">
        <caption
          className={cn("pb-2 text-left text-sm text-muted-foreground", hideCaption && "sr-only")}
        >
          {caption}
        </caption>
        <thead>
          {table.getHeaderGroups().map((group) => (
            <tr key={group.id} className="border-b">
              {group.headers.map((header, index) => (
                <th
                  key={header.id}
                  scope="col"
                  className={cn(
                    "h-10 px-3 text-left align-middle font-medium whitespace-nowrap text-muted-foreground",
                    sticky(index),
                  )}
                >
                  {header.isPlaceholder
                    ? null
                    : flexRender(header.column.columnDef.header, header.getContext())}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {loading ? (
            Array.from({ length: loadingRows }, (_, row) => (
              <tr key={row} className="border-b">
                {Array.from({ length: width }, (_, cell) => (
                  <td key={cell} className={cn("px-3 py-3", sticky(cell))}>
                    <Skeleton className="h-4 w-24" />
                  </td>
                ))}
              </tr>
            ))
          ) : table.getRowModel().rows.length === 0 ? (
            <tr>
              <td colSpan={width} className="px-3 py-8 text-center text-muted-foreground">
                {empty ?? <Trans>Nothing here yet.</Trans>}
              </td>
            </tr>
          ) : (
            table.getRowModel().rows.map((row) => (
              <tr key={row.id} className="border-b hover:bg-muted/40">
                {row.getVisibleCells().map((cell, index) => (
                  <td key={cell.id} className={cn("px-3 py-2 align-middle", sticky(index))}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
      {loading ? (
        <span role="status" className="sr-only">
          <Trans>Loading…</Trans>
        </span>
      ) : null}
    </div>
  );
}
