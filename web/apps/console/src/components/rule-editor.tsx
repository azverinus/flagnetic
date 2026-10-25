import type { Experiment, Segment, UnitType, Variation } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { Link } from "@tanstack/react-router";
import { ArrowDownIcon, ArrowUpIcon, GripVerticalIcon, Trash2Icon } from "lucide-react";
import {
  type DragEvent,
  type KeyboardEvent,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { ConditionRow } from "@/components/condition-row";
import { ExperimentStatusBadge } from "@/components/experiment-status-badge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectItem } from "@/components/ui/select";
import { formatNumber } from "@/i18n/format";
import { cn } from "@/lib/cn";
import {
  type AttributeOption,
  newAttributeCondition,
  newPrerequisiteCondition,
  newSegmentCondition,
} from "@/lib/conditions";
import { percentText } from "@/lib/experiments";
import {
  maxConditions,
  maxRules,
  type Move,
  moveRule,
  newRule,
  type RuleDraft,
  servableExperiments,
  type ServeDraft,
  withRule,
} from "@/lib/rule-editor";

export type RuleEditorProps = {
  rules: RuleDraft[];
  onChange: (rules: RuleDraft[]) => void;
  variations: readonly Variation[];
  unitTypes: readonly UnitType[];
  attributes: readonly AttributeOption[];
  segments: readonly Segment[];
  /** The project's other flags that are not archived, with their variations' keys. */
  flags: ReadonlyMap<string, readonly string[]>;
  /** The project the flag is in, which an experiment's link points into. */
  project: string;
  /** The experiments comparing this flag's variations, archived ones included. */
  experiments: readonly Experiment[];
  /**
   * What is wrong, by where it shows: a condition by its key, a rule by
   * its key, a serve's field by the rule's key and the field.
   */
  errors: ReadonlyMap<string, string>;
  /** Asks to regenerate a saved rollout's salt; absent where it cannot be. */
  onRegenerateSalt?: (rule: RuleDraft) => void;
  /** The identifiers of the saved rules serving a rollout, whose salt can be regenerated. */
  saltedRules: ReadonlySet<string>;
  /** The card of the rule that served the tester's last result, by its key. */
  matchedRule?: string;
  disabled?: boolean;
};

/**
 * A configuration's rules in the order they are checked, each a card with
 * its conditions and what it serves. A card moves by dragging its handle,
 * with its buttons, or with Alt+Arrow Up and Down from anywhere in it,
 * and every move and removal is announced, so ordering does not need a
 * pointer.
 */
export function RuleEditor({
  rules,
  onChange,
  variations,
  unitTypes,
  attributes,
  segments,
  flags,
  project,
  experiments,
  errors,
  onRegenerateSalt,
  saltedRules,
  matchedRule,
  disabled = false,
}: RuleEditorProps) {
  const { i18n, t } = useLingui();
  const maxRulesText = formatNumber(i18n, maxRules);
  const heading = useId();
  const [announcement, setAnnouncement] = useState("");
  const [dragged, setDragged] = useState<number>();
  // A moved card's DOM node is moved too, which drops focus from it; the
  // element focused before the move gets it back once the cards are laid
  // out, or, when it is a move button that the move disabled (the card
  // reached an end), the card's other move button. A removed card's focus
  // goes to Add rule, so the keyboard stays in the rules.
  const refocus = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => {
    const target = refocus.current;
    refocus.current = null;
    if (!target || document.activeElement === target) return;
    if (target.isConnected && !target.matches(":disabled")) {
      target.focus();
      return;
    }
    const sibling = target.isConnected
      ? target.closest("section")?.querySelector<HTMLElement>("[data-move]:not(:disabled)")
      : undefined;
    (sibling ?? addButton.current)?.focus();
  }, [rules]);

  const announceMove = (move: Move) => {
    const { from, to } = move;
    setAnnouncement(t`Rule ${from} moved to position ${to}.`);
  };
  const move = (from: number, to: number) => {
    const moved = moveRule(rules, from, to);
    if (!moved.announcement) return;
    refocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    onChange(moved.rules);
    announceMove(moved.announcement);
  };
  const remove = (index: number) => {
    const position = index + 1;
    refocus.current = addButton.current;
    onChange(rules.filter((_, at) => at !== index));
    setAnnouncement(t`Rule ${position} removed.`);
  };
  const add = () => {
    onChange([...rules, newRule(variations[0]?.key ?? "", unitTypes[0]?.key ?? "")]);
    const position = rules.length + 1;
    setAnnouncement(t`Rule ${position} added.`);
  };

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id={heading} className="text-base font-semibold">
          <Trans>Rules</Trans>
        </h2>
        <p className="text-sm text-muted-foreground">
          <Trans>
            Checked top to bottom; the first whose conditions all hold serves. Reorder by dragging,
            with the arrows, or with Alt+Arrow Up and Down.
          </Trans>
        </p>
      </div>
      <p role="status" aria-live="polite" className="sr-only">
        {announcement}
      </p>
      {rules.length === 0 ? (
        <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
          <Trans>No rules: every context receives the fallthrough variation.</Trans>
        </p>
      ) : (
        <ol className="flex flex-col gap-3">
          {rules.map((rule, index) => (
            <RuleCard
              key={rule.key}
              rule={rule}
              position={index + 1}
              last={index === rules.length - 1}
              onChange={(next) => onChange(withRule(rules, rule.key, () => next))}
              onMove={(to) => move(index, to - 1)}
              onRemove={() => remove(index)}
              dragging={dragged === index}
              onDragStart={() => setDragged(index)}
              onDragEnd={() => setDragged(undefined)}
              onDrop={(from) => {
                move(from - 1, index);
                setDragged(undefined);
              }}
              variations={variations}
              unitTypes={unitTypes}
              attributes={attributes}
              segments={segments}
              flags={flags}
              project={project}
              experiments={experiments}
              errors={errors}
              onRegenerateSalt={
                onRegenerateSalt &&
                rule.id &&
                saltedRules.has(rule.id) &&
                rule.serve.kind === "rollout"
                  ? () => onRegenerateSalt(rule)
                  : undefined
              }
              matched={matchedRule === rule.key}
              disabled={disabled}
            />
          ))}
        </ol>
      )}
      {disabled ? null : (
        <div className="flex flex-wrap items-center gap-3">
          <Button
            ref={addButton}
            type="button"
            variant="outline"
            onClick={add}
            disabled={rules.length >= maxRules}
          >
            <Trans>Add rule</Trans>
          </Button>
          {rules.length >= maxRules ? (
            <p className="text-sm text-muted-foreground">
              <Trans>A configuration has at most {maxRulesText} rules.</Trans>
            </p>
          ) : null}
        </div>
      )}
    </section>
  );
}

/**
 * The drag data a rule card carries, its position from 1, under a type of
 * its own: a text field the card is dropped over would take plain text
 * as typed.
 */
const dragType = "application/x-flagnetic-rule";

type RuleCardProps = Omit<
  RuleEditorProps,
  "rules" | "onChange" | "onRegenerateSalt" | "saltedRules" | "matchedRule"
> & {
  /** Whether this rule served the tester's last result. */
  matched: boolean;
  rule: RuleDraft;
  position: number;
  last: boolean;
  onChange: (rule: RuleDraft) => void;
  onMove: (to: number) => void;
  onRemove: () => void;
  dragging: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  /** A card was dropped on this one: the dragged card's position, from 1. */
  onDrop: (from: number) => void;
  onRegenerateSalt?: () => void;
};

function RuleCard({
  rule,
  position,
  last,
  onChange,
  onMove,
  onRemove,
  dragging,
  onDragStart,
  onDragEnd,
  onDrop,
  variations,
  unitTypes,
  attributes,
  segments,
  flags,
  project,
  experiments,
  errors,
  onRegenerateSalt,
  matched,
  disabled = false,
}: RuleCardProps) {
  const { i18n, t } = useLingui();
  const maxConditionsText = formatNumber(i18n, maxConditions);
  const heading = useId();
  const descriptionId = useId();
  const ruleError = errors.get(rule.key) ?? errors.get(`${rule.key}/conditions`);
  const full = rule.conditions.length >= maxConditions;
  const [over, setOver] = useState(false);
  const card = useRef<HTMLElement>(null);
  // The rule that served the tester's result is brought into view, so a
  // long configuration shows which rule decided without a search.
  useEffect(() => {
    if (!matched) return;
    const still = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    card.current?.scrollIntoView?.({ block: "center", behavior: still ? "auto" : "smooth" });
  }, [matched]);

  const onKeyDown = (event: KeyboardEvent<HTMLLIElement>) => {
    if (disabled || !event.altKey) return;
    if (event.key === "ArrowUp" && position > 1) {
      event.preventDefault();
      onMove(position - 1);
    } else if (event.key === "ArrowDown" && !last) {
      event.preventDefault();
      onMove(position + 1);
    }
  };
  const dropTarget = disabled
    ? {}
    : {
        onDragOver: (event: DragEvent) => {
          event.preventDefault();
          event.dataTransfer.dropEffect = "move";
          if (!over) setOver(true);
        },
        onDragLeave: () => setOver(false),
        onDrop: (event: DragEvent) => {
          event.preventDefault();
          setOver(false);
          const from = Number(event.dataTransfer.getData(dragType));
          if (Number.isInteger(from) && from > 0) onDrop(from);
        },
      };

  return (
    <li onKeyDown={onKeyDown} {...dropTarget}>
      <section
        ref={card}
        aria-labelledby={heading}
        className={cn(
          "flex flex-col gap-4 rounded-lg border bg-card p-4",
          (rule.reapplied || matched) && "border-primary ring-1 ring-primary",
          dragging && "opacity-50",
          over && "border-primary",
        )}
      >
        <div className="flex flex-wrap items-center gap-2">
          {disabled ? null : (
            <button
              type="button"
              draggable
              // Dragging takes a pointer; the move buttons and Alt+arrows
              // are the keyboard's way, so the handle is left out of the tab order.
              tabIndex={-1}
              aria-label={t`Drag rule ${position}`}
              onDragStart={(event) => {
                event.dataTransfer.effectAllowed = "move";
                event.dataTransfer.setData(dragType, String(position));
                // Changing the page while the browser starts a drag can
                // cancel it, so the dragged card is marked a moment later.
                setTimeout(onDragStart, 0);
              }}
              onDragEnd={onDragEnd}
              className="cursor-grab rounded-sm p-1 text-muted-foreground hover:bg-accent active:cursor-grabbing"
            >
              {/* The icon takes no pointer, so pressing it presses the handle, which drags. */}
              <GripVerticalIcon aria-hidden className="pointer-events-none size-4" />
            </button>
          )}
          <h3 id={heading} className="text-sm font-semibold">
            <Trans>Rule {position}</Trans>
          </h3>
          {rule.reapplied ? (
            <Badge>
              <Trans>Reapplied after a conflict: saves as a new rule</Trans>
            </Badge>
          ) : null}
          {matched ? (
            <Badge>
              <Trans>Matched in the tester</Trans>
            </Badge>
          ) : null}
          {disabled ? null : (
            <div className="ml-auto flex gap-1">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                data-move
                aria-label={t`Move rule ${position} up`}
                disabled={position === 1}
                onClick={() => onMove(position - 1)}
              >
                <ArrowUpIcon aria-hidden />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                data-move
                aria-label={t`Move rule ${position} down`}
                disabled={last}
                onClick={() => onMove(position + 1)}
              >
                <ArrowDownIcon aria-hidden />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={t`Remove rule ${position}`}
                onClick={onRemove}
              >
                <Trash2Icon aria-hidden />
              </Button>
            </div>
          )}
        </div>
        {ruleError ? (
          <p role="alert" className="text-sm text-destructive">
            {ruleError}
          </p>
        ) : null}
        <div className="flex flex-col gap-2">
          <Label htmlFor={descriptionId}>
            <Trans>Description (rule {position}, optional)</Trans>
          </Label>
          <Input
            id={descriptionId}
            autoComplete="off"
            value={rule.description}
            disabled={disabled}
            onChange={(event) => onChange({ ...rule, description: event.target.value })}
          />
        </div>
        <div className="flex flex-col gap-2">
          <p className="text-sm font-medium">
            <Trans>Conditions (all must hold)</Trans>
          </p>
          {rule.conditions.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              <Trans>No conditions: matches every context.</Trans>
            </p>
          ) : (
            <ol className="flex flex-col gap-2">
              {rule.conditions.map((condition, index) => (
                <ConditionRow
                  key={condition.key}
                  position={index + 1}
                  condition={condition}
                  onChange={(next) =>
                    onChange({
                      ...rule,
                      conditions: rule.conditions.map((other) =>
                        other.key === condition.key ? next : other,
                      ),
                    })
                  }
                  onRemove={() =>
                    onChange({
                      ...rule,
                      conditions: rule.conditions.filter((other) => other.key !== condition.key),
                    })
                  }
                  attributes={attributes}
                  segments={segments}
                  flags={flags}
                  error={errors.get(condition.key)}
                  disabled={disabled}
                />
              ))}
            </ol>
          )}
          {disabled ? null : (
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={full || attributes.length === 0}
                onClick={() => {
                  const first = attributes[0]!;
                  onChange({
                    ...rule,
                    conditions: [...rule.conditions, newAttributeCondition(first.key, first.type)],
                  });
                }}
              >
                <Trans>Add attribute condition</Trans>
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={full || segments.length === 0}
                onClick={() =>
                  onChange({
                    ...rule,
                    conditions: [...rule.conditions, newSegmentCondition(segments[0]?.key ?? "")],
                  })
                }
              >
                <Trans>Add segment condition</Trans>
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={full}
                onClick={() =>
                  onChange({
                    ...rule,
                    conditions: [...rule.conditions, newPrerequisiteCondition()],
                  })
                }
              >
                <Trans>Add prerequisite condition</Trans>
              </Button>
              {full ? (
                <p className="text-sm text-muted-foreground">
                  <Trans>A rule has at most {maxConditionsText} conditions.</Trans>
                </p>
              ) : null}
            </div>
          )}
        </div>
        <ServeEditor
          position={position}
          serve={rule.serve}
          onChange={(serve) => onChange({ ...rule, serve })}
          variations={variations}
          unitTypes={unitTypes}
          project={project}
          experiments={experiments}
          errors={{
            serve: errors.get(`${rule.key}/serve`),
            variation: errors.get(`${rule.key}/variation`),
            experiment: errors.get(`${rule.key}/experiment`),
            unitType: errors.get(`${rule.key}/unit_type`),
            percent: errors.get(`${rule.key}/percent`),
          }}
          onRegenerateSalt={onRegenerateSalt}
          disabled={disabled}
        />
      </section>
    </li>
  );
}

function ServeEditor({
  position,
  serve,
  onChange,
  variations,
  unitTypes,
  project,
  experiments,
  errors,
  onRegenerateSalt,
  disabled,
}: {
  position: number;
  serve: ServeDraft;
  onChange: (serve: ServeDraft) => void;
  variations: readonly Variation[];
  unitTypes: readonly UnitType[];
  project: string;
  experiments: readonly Experiment[];
  errors: {
    serve?: string;
    variation?: string;
    experiment?: string;
    unitType?: string;
    percent?: string;
  };
  onRegenerateSalt?: () => void;
  disabled: boolean;
}) {
  const { t } = useLingui();
  const name = useId();
  const ids = { percent: useId(), salt: useId(), hint: useId() };
  const errorId = (field: string) => `${name}-${field}-error`;
  const fieldError = (field: string, message?: string) =>
    message ? (
      <p id={errorId(field)} className="text-sm text-destructive">
        {message}
      </p>
    ) : null;

  return (
    <fieldset className="flex flex-col gap-3 border-t pt-3" disabled={disabled}>
      <legend className="float-left mb-1 text-sm font-medium">
        <Trans>Serves (rule {position})</Trans>
      </legend>
      <div className="clear-both flex gap-4 text-sm">
        {(
          [
            ["variation", t`Variation`],
            ["rollout", t`Rollout`],
            ["experiment", t`Experiment`],
          ] as const
        ).map(([kind, label]) => (
          <label key={kind} className="flex items-center gap-2">
            <input
              type="radio"
              name={name}
              value={kind}
              checked={serve.kind === kind}
              onChange={() => onChange({ ...serve, kind })}
              className="size-4 accent-primary"
            />
            {label}
          </label>
        ))}
      </div>
      {serve.kind === "experiment" ? (
        <ExperimentServe
          position={position}
          serve={serve}
          onChange={onChange}
          project={project}
          experiments={experiments}
          error={errors.experiment}
          errorId={errorId("experiment")}
          disabled={disabled}
        />
      ) : null}
      <div
        className={cn(
          "flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-start",
          serve.kind === "experiment" && "hidden",
        )}
      >
        <div className="flex flex-col gap-1">
          <Select
            aria-label={t`Variation (rule ${position})`}
            value={serve.variation}
            placeholder={t`Choose a variation`}
            onValueChange={(variation) => onChange({ ...serve, variation })}
            aria-invalid={errors.variation ? true : undefined}
            aria-describedby={errors.variation ? errorId("variation") : undefined}
            disabled={disabled}
            className="sm:w-48"
          >
            {variations.map((variation) => (
              <SelectItem key={variation.key} value={variation.key}>
                {variation.key}
              </SelectItem>
            ))}
          </Select>
          {fieldError("variation", errors.variation)}
        </div>
        {serve.kind === "rollout" ? (
          <>
            <div className="flex flex-col gap-1">
              <div className="flex items-center gap-1">
                <Input
                  id={ids.percent}
                  aria-label={t`Percentage (rule ${position})`}
                  inputMode="decimal"
                  autoComplete="off"
                  value={serve.percent}
                  onChange={(event) => onChange({ ...serve, percent: event.target.value })}
                  aria-invalid={errors.percent ? true : undefined}
                  aria-describedby={[ids.hint, errors.percent ? errorId("percent") : ""]
                    .filter(Boolean)
                    .join(" ")}
                  className="w-24"
                />
                <span className="text-sm text-muted-foreground">%</span>
              </div>
              {fieldError("percent", errors.percent)}
            </div>
            <div className="flex flex-col gap-1">
              <Select
                aria-label={t`Unit type (rule ${position})`}
                value={serve.unitType}
                placeholder={t`Choose a unit type`}
                onValueChange={(unitType) => onChange({ ...serve, unitType })}
                aria-invalid={errors.unitType ? true : undefined}
                aria-describedby={errors.unitType ? errorId("unit_type") : undefined}
                disabled={disabled}
                className="sm:w-40"
              >
                {unitTypes.map((type) => (
                  <SelectItem key={type.key} value={type.key}>
                    {type.key}
                  </SelectItem>
                ))}
              </Select>
              {fieldError("unit_type", errors.unitType)}
            </div>
          </>
        ) : null}
      </div>
      {serve.kind === "rollout" ? (
        <>
          <p id={ids.hint} className="text-sm text-muted-foreground">
            <Trans>
              Units outside the percentage continue to the next rule. Raising the percentage keeps
              every unit already in.
            </Trans>
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {serve.salt ? (
              <>
                <Label htmlFor={ids.salt} className="text-sm">
                  <Trans>Salt (rule {position})</Trans>
                </Label>
                <Input id={ids.salt} readOnly value={serve.salt} className="w-48 font-mono" />
              </>
            ) : (
              <p className="text-sm text-muted-foreground">
                <Trans>
                  Salt (rule {position}): given by the server when the rule is saved as a rollout.
                </Trans>
              </p>
            )}
            {onRegenerateSalt && !disabled ? (
              <Button type="button" variant="outline" size="sm" onClick={onRegenerateSalt}>
                <Trans>Regenerate salt</Trans>
              </Button>
            ) : null}
          </div>
        </>
      ) : null}
      {errors.serve ? (
        <p role="alert" className="text-sm text-destructive">
          {errors.serve}
        </p>
      ) : null}
    </fieldset>
  );
}

/**
 * The experiment a rule serves: chosen among the flag's draft and running
 * experiments, with its status, traffic allocation and weights shown as
 * they are and a link to change them on the experiment's own page, since
 * a rule only names the experiment.
 */
function ExperimentServe({
  position,
  serve,
  onChange,
  project,
  experiments,
  error,
  errorId,
  disabled,
}: {
  position: number;
  serve: ServeDraft;
  onChange: (serve: ServeDraft) => void;
  project: string;
  experiments: readonly Experiment[];
  error?: string;
  errorId: string;
  disabled: boolean;
}) {
  const { i18n, t } = useLingui();
  const choices = servableExperiments(experiments, serve.experiment);
  const chosen = experiments.find((experiment) => experiment.key === serve.experiment);
  const allocation = chosen ? percentText(i18n, chosen.traffic_allocation) : "";
  const unitType = chosen?.unit_type ?? "";

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <Select
          aria-label={t`Experiment (rule ${position})`}
          value={serve.experiment}
          placeholder={t`Choose an experiment`}
          onValueChange={(experiment) => onChange({ ...serve, experiment })}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorId : undefined}
          disabled={disabled || choices.length === 0}
          className="sm:w-72"
        >
          {choices.map((experiment) => (
            <SelectItem key={experiment.key} value={experiment.key}>
              {experiment.key}
            </SelectItem>
          ))}
        </Select>
        {error ? (
          <p id={errorId} className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        {choices.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            <Trans>
              The flag has no draft or running experiment.{" "}
              <Link
                to="/projects/$project/experiments/-/new"
                params={{ project }}
                className="underline"
              >
                Create one
              </Link>{" "}
              to serve it here.
            </Trans>
          </p>
        ) : null}
      </div>
      {chosen ? (
        <div className="flex flex-col gap-2 rounded-md border bg-muted/40 p-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <Link
              to="/projects/$project/experiments/$experiment"
              params={{ project, experiment: chosen.key }}
              className="font-medium underline"
            >
              {chosen.name}
            </Link>
            <ExperimentStatusBadge status={chosen.status} />
            <span className="text-muted-foreground">
              <Trans>
                {allocation}% of {unitType} units included
              </Trans>
            </span>
          </div>
          <ul aria-label={t`Weights (rule ${position})`} className="flex flex-wrap gap-x-4 gap-y-1">
            {chosen.variations.map((weight) => (
              <li key={weight.variation}>
                <span className="font-mono">{weight.variation}</span>{" "}
                <span className="text-muted-foreground">{percentText(i18n, weight.weight)}%</span>
              </li>
            ))}
          </ul>
          <p className="text-muted-foreground">
            {chosen.status === "running" ? (
              <Trans>
                Units matching this rule are assigned to the variations by the experiment&apos;s
                weights. Its weights and unit type change only on the experiment&apos;s page.
              </Trans>
            ) : chosen.status === "draft" ? (
              <Trans>
                While the experiment is a draft, SDKs do not receive this rule: units continue to
                the next rule until it starts.
              </Trans>
            ) : (
              <Trans>
                The experiment has ended: SDKs no longer receive this rule, and units continue to
                the next rule.
              </Trans>
            )}
          </p>
        </div>
      ) : null}
    </div>
  );
}
