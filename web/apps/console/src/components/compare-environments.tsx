import {
  copyFlagConfiguration,
  type Environment,
  type Flag,
  type FlagConfiguration,
  type Rule,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CopyIcon } from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field } from "@/components/ui/field";
import { Label } from "@/components/ui/label";
import { Select, SelectItem } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { environmentLabel } from "@/lib/environments";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/cn";
import { type ComparisonRow, comparisonRows, sameRuleset } from "@/lib/compare";
import { describeCondition, describeServe } from "@/lib/conditions";
import { ApiError, unwrap } from "@/lib/errors";
import { ifMatch } from "@/lib/http";
import { auditQueryKey, flagQuery } from "@/lib/queries";
import { useConfigurationAnswer } from "@/lib/use-configuration-answer";

export type CompareEnvironmentsProps = {
  project: string;
  flag: Flag;
  /** The project's environments, in their order. */
  environments: readonly Environment[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The environment compared first: the one whose tab is open. */
  initial: string | undefined;
};

/**
 * A flag's configurations in two environments side by side, each part
 * marked where SDKs would receive something different, and copying one
 * into the other: what a promotion from staging to production does.
 */
export function CompareEnvironments(props: CompareEnvironmentsProps) {
  const { t } = useLingui();
  return (
    <Sheet
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t`Compare environments`}
      description={
        <Trans>
          What the flag serves in two environments, judged by what SDKs receive: rule descriptions
          and override notes do not count.
        </Trans>
      }
      className="max-w-4xl"
    >
      {/* Mounted only while open, so each opening starts from the open tab. */}
      {props.open ? <Comparison {...props} /> : null}
    </Sheet>
  );
}

function Comparison({ project, flag, environments, initial }: CompareEnvironmentsProps) {
  const { i18n, t } = useLingui();
  const keys = flag.environments.map((configuration) => configuration.environment);
  const [left, setLeft] = useState(() => initial ?? keys[0] ?? "");
  const [right, setRight] = useState(() => keys.find((key) => key !== left) ?? left);
  const [copying, setCopying] = useState<{ from: string; to: string }>();
  const configurationOf = (key: string) =>
    flag.environments.find((configuration) => configuration.environment === key);
  const nameOf = (key: string) => environments.find((env) => env.key === key)?.name ?? key;
  const labelOf = (key: string) => {
    const environment = environments.find((env) => env.key === key);
    return environment ? environmentLabel(i18n, environment) : key;
  };
  const a = configurationOf(left);
  const b = configurationOf(right);

  const choice = (label: ReactNode, value: string, onChange: (key: string) => void) => (
    <Field label={label} className="w-48">
      <Select value={value} onValueChange={onChange}>
        {keys.map((key) => (
          <SelectItem key={key} value={key}>
            {labelOf(key)}
          </SelectItem>
        ))}
      </Select>
    </Field>
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-4">
        {choice(<Trans>Compare</Trans>, left, setLeft)}
        {choice(<Trans>With</Trans>, right, setRight)}
      </div>
      {a && b && left !== right ? (
        <>
          <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
            {sameRuleset(a, b) ? (
              <Trans>Both environments serve the same.</Trans>
            ) : (
              <Trans>The environments serve differently where marked.</Trans>
            )}
          </p>
          <SideBySide
            rows={comparisonRows(a, b)}
            left={nameOf(left)}
            right={nameOf(right)}
            copyHere={
              flag.archived_at === null
                ? (side) =>
                    setCopying(
                      side === "left" ? { from: right, to: left } : { from: left, to: right },
                    )
                : undefined
            }
          />
        </>
      ) : (
        <p className="text-sm text-muted-foreground">{t`Choose two different environments.`}</p>
      )}
      {copying ? (
        <CopyDialog
          project={project}
          flag={flag}
          from={copying.from}
          target={configurationOf(copying.to)!}
          names={{ from: nameOf(copying.from), to: nameOf(copying.to) }}
          onClose={() => setCopying(undefined)}
        />
      ) : null}
    </div>
  );
}

function SideBySide({
  rows,
  left,
  right,
  copyHere,
}: {
  rows: ComparisonRow[];
  left: string;
  right: string;
  /** Asks to copy the other side into this one; absent while the flag cannot be configured. */
  copyHere?: (side: "left" | "right") => void;
}) {
  const { t } = useLingui();
  const header = (name: string, other: string, side: "left" | "right") => (
    <th scope="col" className="py-2 pr-3 text-left align-bottom font-medium">
      <span className="flex flex-wrap items-center gap-2">
        {name}
        {copyHere ? (
          <Can permission="flag_configuration.update" whenMissing="disable">
            <Button size="sm" variant="outline" onClick={() => copyHere(side)}>
              <CopyIcon aria-hidden />
              <Trans>Copy {other} here</Trans>
            </Button>
          </Can>
        ) : null}
      </span>
    </th>
  );
  return (
    <div className="relative max-w-full overflow-x-auto">
      <table className="w-full border-collapse text-sm">
        <caption className="sr-only">{t`${left} and ${right} side by side`}</caption>
        <thead>
          <tr className="border-b">
            <th scope="col" className="py-2 pr-3 text-left font-medium text-muted-foreground">
              <Trans>Part</Trans>
            </th>
            {header(left, right, "left")}
            {header(right, left, "right")}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={index} className={cn("border-b align-top", row.differs && "bg-accent/40")}>
              <th scope="row" className="py-2 pr-3 text-left font-normal">
                <span className="flex flex-col gap-1">
                  <PartName row={row} />
                  {row.differs ? (
                    <span className="w-fit rounded-sm bg-primary/10 px-1 py-0.5 text-xs text-primary">
                      <Trans>Differs</Trans>
                    </span>
                  ) : null}
                </span>
              </th>
              <td className="py-2 pr-3">
                <PartValue row={row} side="left" />
              </td>
              <td className="py-2">
                <PartValue row={row} side="right" />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PartName({ row }: { row: ComparisonRow }) {
  switch (row.part) {
    case "on":
      return <Trans>On or off</Trans>;
    case "off_variation":
      return <Trans>Off variation</Trans>;
    case "overrides":
      return <Trans>Overrides</Trans>;
    case "prerequisites":
      return <Trans>Prerequisites</Trans>;
    case "rule": {
      const position = row.position;
      return <Trans>Rule {position}</Trans>;
    }
    case "fallthrough":
      return <Trans>Fallthrough variation</Trans>;
  }
}

function None() {
  return (
    <span className="text-muted-foreground">
      <Trans>None</Trans>
    </span>
  );
}

function Code({ children }: { children: ReactNode }) {
  return (
    <code className="rounded-sm bg-muted px-1 py-0.5 font-mono text-xs break-all">{children}</code>
  );
}

function PartValue({ row, side }: { row: ComparisonRow; side: "left" | "right" }) {
  switch (row.part) {
    case "on":
      return row[side] ? <Trans>On</Trans> : <Trans>Off</Trans>;
    case "off_variation":
    case "fallthrough":
      return <Code>{row[side]}</Code>;
    case "overrides":
      return row[side].length === 0 ? (
        <None />
      ) : (
        <ul className="flex flex-col gap-1">
          {row[side].map((override) => (
            <li key={`${override.unit_type}\n${override.unit_id}`}>
              {override.unit_type} <Code>{override.unit_id}</Code> →{" "}
              <Code>{override.variation}</Code>
            </li>
          ))}
        </ul>
      );
    case "prerequisites":
      return row[side].length === 0 ? (
        <None />
      ) : (
        <ul className="flex flex-col gap-1">
          {row[side].map((prerequisite) => (
            <li key={prerequisite.flag}>
              <Code>{prerequisite.flag}</Code> → <Code>{prerequisite.variation}</Code>
            </li>
          ))}
        </ul>
      );
    case "rule": {
      const rule = row[side];
      return rule ? <RuleSummary rule={rule} /> : <None />;
    }
  }
}

function RuleSummary({ rule }: { rule: Rule }) {
  const { i18n } = useLingui();
  const salt = rule.serve.salt;
  const serves = describeServe(i18n, rule.serve);
  return (
    <div className="flex flex-col gap-1">
      {rule.description ? <span className="font-medium">{rule.description}</span> : null}
      <ul className="flex flex-col gap-1">
        {rule.conditions.map((condition, index) => (
          <li key={index}>
            <Code>{describeCondition(i18n, condition)}</Code>
          </li>
        ))}
      </ul>
      <span>
        <Trans>
          Serves <Code>{serves}</Code>
        </Trans>
      </span>
      {salt ? (
        <span className="text-xs text-muted-foreground">
          <Trans>
            Salt <Code>{salt}</Code>
          </Trans>
        </span>
      ) : null}
    </div>
  );
}

/**
 * Copying one environment's configuration into another: the rules with
 * their identifiers and salts, so the same units fall in a rollout in
 * both, the prerequisites and the off and fallthrough variations. The
 * target stays on or off as it is, so a copy never switches it on, and
 * keeps its overrides unless the member includes the source's.
 */
function CopyDialog({
  project,
  flag,
  from,
  target,
  names,
  onClose,
}: {
  project: string;
  flag: Flag;
  from: string;
  target: FlagConfiguration;
  names: { from: string; to: string };
  onClose: () => void;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const answered = useConfigurationAnswer(project, flag.key);
  const [overrides, setOverrides] = useState(false);
  const overridesId = useId();
  const { from: source, to } = names;

  const copy = useMutation({
    mutationFn: () =>
      unwrap(
        copyFlagConfiguration({
          path: { project, flag: flag.key, environment: target.environment },
          body: { from, include_overrides: overrides },
          headers: ifMatch(target.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: (configuration) => {
      answered(configuration);
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      onClose();
      toast.success(t`The configuration of ${source} is copied into ${to}.`);
    },
    onError: (error) => {
      // The dialog explains the refusal; the newer version is read, so
      // confirming again is based on it.
      if (error instanceof ApiError && error.code === "version_mismatch") {
        void queryClient.invalidateQueries({ queryKey: flagQuery(project, flag.key).queryKey });
      }
    },
  });

  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={t`Copy ${source} into ${to}`}
      confirmLabel={<Trans>Copy configuration</Trans>}
      pending={copy.isPending}
      refusal={copy.error}
      onConfirm={() => copy.mutate()}
    >
      <p>
        {target.on ? (
          <Trans>
            {to} takes the rules of {source} with their identifiers and salts, its prerequisites and
            its off and fallthrough variations. {to} stays on.
          </Trans>
        ) : (
          <Trans>
            {to} takes the rules of {source} with their identifiers and salts, its prerequisites and
            its off and fallthrough variations. {to} stays off.
          </Trans>
        )}
      </p>
      <div className="flex items-center gap-2 text-foreground">
        <Switch id={overridesId} checked={overrides} onCheckedChange={setOverrides} />
        <Label htmlFor={overridesId}>
          <Trans>
            Replace the overrides of {to} with those of {source}
          </Trans>
        </Label>
      </div>
    </ConfirmDialog>
  );
}
