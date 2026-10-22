import { Trans } from "@lingui/react/macro";
import { type QueryKey, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { ApiError } from "@/lib/errors";
import { ifMatch } from "@/lib/http";
import { useListMutation } from "@/lib/mutations";

/**
 * The `If-Match` of deleting the row with this key: the version the list
 * holds now, which a refused deletion read again, else the version of the
 * row the dialog was opened for.
 */
export function listedIfMatch<Row extends { key: string; version: number }>(
  rows: readonly Row[] | undefined,
  key: string,
  opened: Row | undefined,
): { "If-Match": string } | undefined {
  const row = rows?.find((candidate) => candidate.key === key) ?? opened;
  return row && ifMatch(row.version);
}

/**
 * A table's columns, followed by the column of each row's actions for a
 * caller holding the permission they need. A caller without it gets no
 * column rather than an empty one, so a list does not fill up with what
 * cannot be done; the screen's primary action says why instead. Call it
 * where the columns are memoized: a cell renderer that changed identity
 * would remount its row's buttons, and a dialog could not give focus back.
 */
export function withRowActions<Row>(
  columns: ColumnDef<Row, unknown>[],
  held: boolean,
  actions: (row: Row) => ReactNode,
): ColumnDef<Row, unknown>[] {
  if (!held) return columns;
  return [
    ...columns,
    {
      id: "actions",
      header: () => (
        <span className="sr-only">
          <Trans>Actions</Trans>
        </span>
      ),
      cell: ({ row }) => <div className="flex justify-end gap-1">{actions(row.original)}</div>,
    },
  ];
}

export type RowActionProps = {
  /** The action's accessible name, naming the row, such as "Delete staging". */
  label: string;
  icon: LucideIcon;
  onClick: () => void;
};

/**
 * One action of a row, as an icon button, in the column `withRowActions`
 * adds only for a caller holding the permission the actions need.
 */
export function RowAction({ label, icon: Icon, onClick }: RowActionProps) {
  return (
    <Button variant="ghost" size="icon" aria-label={label} onClick={onClick}>
      <Icon aria-hidden />
    </Button>
  );
}

export type RowDialogProps<Row> = {
  /** The row the dialog is open for; none while it is closed. */
  row: Row | undefined;
  onClose: () => void;
  title: (row: Row) => string;
  /** The dialog's content for the row, mounted only while it is open. */
  children: (row: Row) => ReactNode;
};

/** A dialog opened for one row of a table, such as the form editing it. */
export function RowDialog<Row>({ row, onClose, title, children }: RowDialogProps<Row>) {
  return (
    <Dialog
      open={row !== undefined}
      onOpenChange={(open) => !open && onClose()}
      title={row === undefined ? "" : title(row)}
    >
      {row === undefined ? null : children(row)}
    </Dialog>
  );
}

export type DeleteRowDialogProps = {
  /** The key of the row to delete; none while the dialog is closed. */
  rowKey: string | undefined;
  onDone: () => void;
  title: (key: string) => string;
  confirmLabel: ReactNode;
  /** Whether the key must be typed out before the deletion is offered. */
  typeToConfirm?: boolean;
  remove: (key: string) => Promise<unknown>;
  /** The list the row is in, read again once it is gone. */
  list: QueryKey;
  /** The consequence of deleting it, said plainly. */
  children: (key: string) => ReactNode;
};

/**
 * The confirmation of deleting one row of a list, which explains a refusal
 * in place and keeps the dialog open until the member closes it.
 */
export function DeleteRowDialog({
  rowKey,
  onDone,
  title,
  confirmLabel,
  typeToConfirm = false,
  remove,
  list,
  children,
}: DeleteRowDialogProps) {
  const queryClient = useQueryClient();
  const deletion = useListMutation(remove, list, onDone);
  const key = rowKey ?? "";
  return (
    <ConfirmDialog
      open={rowKey !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          deletion.reset();
          onDone();
        }
      }}
      title={title(key)}
      confirmLabel={confirmLabel}
      destructive
      pending={deletion.isPending}
      refusal={deletion.error}
      typeToConfirm={typeToConfirm ? key : undefined}
      onConfirm={() =>
        deletion.mutate(key, {
          // Someone changed the row since the list was read: the list is
          // read again, so confirming once more names the version the
          // refusal told the member about.
          onError: (error) => {
            if (error instanceof ApiError && error.code === "version_mismatch") {
              void queryClient.invalidateQueries({ queryKey: list });
            }
          },
        })
      }
    >
      {children(key)}
    </ConfirmDialog>
  );
}
