import {
  type AttributeType,
  declareDiscoveredAttribute,
  type DiscoveredAttribute,
  dismissDiscoveredAttribute,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { CheckIcon, EyeOffIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { RowAction, RowDialog, withRowActions } from "@/components/row-dialogs";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Select, SelectItem } from "@/components/ui/select";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatNumber } from "@/i18n/format";
import { useCan } from "@/lib/access";
import { declarableTypes, observedTypeName, typeName } from "@/lib/attribute-types";
import { unwrap } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { attributesQuery, discoveredAttributesQuery } from "@/lib/queries";

export type DiscoveredAttributesProps = {
  project: string;
};

/**
 * The attributes SDKs sent in evaluated contexts that the project has not
 * declared: each with the JSON types seen, when it was first and last
 * seen and how many contexts carried it, never a value. Someone who may
 * manage declarations declares one with a type among those seen, or
 * dismisses it until it is seen with a type not seen before.
 */
export function DiscoveredAttributes({ project }: DiscoveredAttributesProps) {
  const { i18n, t } = useLingui();
  const discovered = useQuery(discoveredAttributesQuery(project));
  const canManage = useCan("declaration.manage");
  const [declaring, setDeclaring] = useState<DiscoveredAttribute>();
  const [dismissing, setDismissing] = useState<DiscoveredAttribute>();

  const columns = useMemo(() => {
    const columns: TableColumn<DiscoveredAttribute>[] = [
      {
        id: "key",
        header: t`Key`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.key}</code>,
      },
      {
        id: "types",
        header: t`Types seen`,
        cell: ({ row }) =>
          row.original.observed_types.map((type) => observedTypeName(i18n, type)).join(", "),
      },
      {
        id: "first_seen",
        header: t`First seen`,
        cell: ({ row }) => <Moment value={row.original.first_seen} />,
      },
      {
        id: "last_seen",
        header: t`Last seen`,
        cell: ({ row }) => <Moment value={row.original.last_seen} />,
      },
      {
        id: "count",
        header: t`Times seen`,
        cell: ({ row }) => formatNumber(i18n, row.original.count),
      },
    ];
    return withRowActions(columns, canManage, (row) => {
      const { key } = row;
      return (
        <>
          {declarableTypes(row.observed_types).length > 0 ? (
            <RowAction
              label={t`Declare ${key}`}
              icon={CheckIcon}
              onClick={() => setDeclaring(row)}
            />
          ) : null}
          <RowAction
            label={t`Dismiss ${key}`}
            icon={EyeOffIcon}
            onClick={() => setDismissing(row)}
          />
        </>
      );
    });
  }, [canManage, i18n, t]);

  const dismissedKey = dismissing?.key ?? "";
  const list = discoveredAttributesQuery(project).queryKey;
  const dismissal = useListMutation(
    (key: string) => unwrap(dismissDiscoveredAttribute({ path: { project, attribute: key } })),
    list,
    () => setDismissing(undefined),
  );

  return (
    <div className="flex flex-col gap-4">
      <p className="max-w-prose text-sm text-muted-foreground">
        <Trans>
          Attributes SDKs sent in evaluated contexts that this project has not declared. Only the
          key and the type of each value are kept, never the value. A dismissed attribute comes back
          if it is seen with a type not seen before.
        </Trans>
      </p>
      {discovered.isError ? (
        <QueryError error={discovered.error} onRetry={() => void discovered.refetch()} />
      ) : discovered.data?.length === 0 ? (
        <EmptyState
          title={<Trans>No undeclared attribute has been seen.</Trans>}
          description={
            <Trans>
              Attributes in the contexts SDKs evaluate or the tester sends, that the project has not
              declared, are listed here within a few seconds.
            </Trans>
          }
        />
      ) : (
        <Table
          caption={t`Discovered attributes`}
          hideCaption
          columns={columns}
          data={discovered.data ?? []}
          loading={discovered.isPending}
          getRowId={(attribute) => attribute.key}
        />
      )}

      <RowDialog
        row={declaring}
        onClose={() => setDeclaring(undefined)}
        title={({ key }) => t`Declare ${key}`}
      >
        {(attribute) => (
          <DeclareForm
            project={project}
            attribute={attribute}
            onDone={() => setDeclaring(undefined)}
          />
        )}
      </RowDialog>
      <ConfirmDialog
        open={dismissing !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            dismissal.reset();
            setDismissing(undefined);
          }
        }}
        title={t`Dismiss ${dismissedKey}`}
        confirmLabel={<Trans>Dismiss attribute</Trans>}
        pending={dismissal.isPending}
        refusal={dismissal.error}
        onConfirm={() => dismissing && dismissal.mutate(dismissing.key)}
      >
        <p>
          <Trans>
            The attribute {dismissedKey} leaves this list. It comes back if SDKs send it with a type
            not seen before.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}

/**
 * Declares a discovered attribute with one of the types seen; when only
 * one was seen, it is chosen already.
 */
function DeclareForm({
  project,
  attribute,
  onDone,
}: {
  project: string;
  attribute: DiscoveredAttribute;
  onDone: () => void;
}) {
  const { i18n, t } = useLingui();
  const { key } = attribute;
  const types = declarableTypes(attribute.observed_types);
  const [chosen, setChosen] = useState<AttributeType | undefined>(
    types.length === 1 ? types[0] : undefined,
  );
  const [missing, setMissing] = useState(false);
  // Declaring adds a declared attribute and removes the discovered one: the
  // declared list is a prefix of both queries, so it reads both again.
  const declare = useListMutation(
    (type: AttributeType) =>
      unwrap(declareDiscoveredAttribute({ path: { project, attribute: key }, body: { type } })),
    attributesQuery(project).queryKey,
    onDone,
  );

  return (
    <DialogForm
      onSubmit={(event) => {
        event.preventDefault();
        if (chosen === undefined) {
          setMissing(true);
          return;
        }
        declare.mutate(chosen);
      }}
      onCancel={onDone}
      submitLabel={<Trans>Declare attribute</Trans>}
      pending={declare.isPending}
      refusal={declare.error}
    >
      <p className="text-sm text-muted-foreground">
        <Trans>Declaring {key} makes it usable in rules. Its type is fixed once declared.</Trans>
      </p>
      <Field
        label={<Trans>Type</Trans>}
        hint={<Trans>One of the types SDKs sent it with.</Trans>}
        error={missing ? t`Choose a type.` : undefined}
        refusal={declare.error}
        pointer="/type"
      >
        <Select
          value={chosen}
          onValueChange={(value) => {
            setChosen(types.find((type) => type === value));
            setMissing(false);
          }}
          placeholder={t`Choose a type`}
        >
          {types.map((type) => (
            <SelectItem key={type} value={type}>
              {typeName(i18n, type)}
            </SelectItem>
          ))}
        </Select>
      </Field>
    </DialogForm>
  );
}
