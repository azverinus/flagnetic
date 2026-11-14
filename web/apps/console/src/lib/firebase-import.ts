import {
  type DocumentCondition,
  type FirebaseImportResult,
  type FlagType,
  importFirebaseTemplate,
  type ImportWarning,
} from "@flagnetic/api";

import { unwrap } from "./errors";

/** The step the wizard is on. */
export type FirebaseStep = "upload" | "parameters" | "target" | "preview";

export const firebaseSteps: readonly FirebaseStep[] = ["upload", "parameters", "target", "preview"];

type TemplateParameter = {
  valueType?: unknown;
  conditionalValues?: Record<string, unknown>;
};
type TemplateGroup = { parameters?: Record<string, TemplateParameter> };

/**
 * A Remote Config template as the console reads it: the parts the wizard
 * lists. The server decodes and validates the rest, so everything else
 * travels untouched.
 */
export type Template = {
  conditions?: { name?: unknown; expression?: unknown }[];
  parameters?: Record<string, TemplateParameter>;
  parameterGroups?: Record<string, TemplateGroup>;
  [field: string]: unknown;
};

/**
 * Reads an uploaded template. The server validates it in full; this only
 * refuses a file that is plainly something else, so the member learns
 * that before the wizard goes on.
 */
export function parseTemplate(
  text: string,
): { template: Template } | { error: "not_json" | "not_a_template" } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return { error: "not_json" };
  }
  if (
    parsed === null ||
    typeof parsed !== "object" ||
    Array.isArray(parsed) ||
    !("parameters" in parsed || "parameterGroups" in parsed)
  ) {
    return { error: "not_a_template" };
  }
  return { template: parsed as Template };
}

/** Every parameter of the template, top-level ones first, each with its group. */
function parametersOf(template: Template) {
  const top = Object.entries(template.parameters ?? {}).map(([key, parameter]) => ({
    key,
    group: undefined as string | undefined,
    parameter,
  }));
  const grouped = Object.entries(template.parameterGroups ?? {}).flatMap(([group, body]) =>
    Object.entries(body.parameters ?? {}).map(([key, parameter]) => ({ key, group, parameter })),
  );
  return [...top, ...grouped];
}

/** The flag type a `valueType` names; an unset one is a string, as the server reads it. */
function flagTypeOf(valueType: unknown): FlagType {
  switch (valueType) {
    case "BOOLEAN":
      return "boolean";
    case "NUMBER":
      return "number";
    case "JSON":
      return "json";
    default:
      return "string";
  }
}

export type ParameterRow = {
  /** The parameter's key, which the flag keeps. */
  key: string;
  type: FlagType;
  tags: string[];
  included: boolean;
};

/**
 * The parameters as the flags they become. Type and tags are the dry
 * run's when it has answered, and what the template says until then.
 */
export function parameterRows(
  template: Template,
  proposed: FirebaseImportResult["flags"] | undefined,
  excluded: readonly string[],
): ParameterRow[] {
  const flags = new Map((proposed ?? []).map((flag) => [flag.key, flag]));
  return parametersOf(template).map(({ key, group, parameter }) => {
    const flag = flags.get(key);
    return {
      key,
      type: (flag?.type as FlagType | undefined) ?? flagTypeOf(parameter.valueType),
      tags: flag?.tags ?? (group === undefined ? [] : [group]),
      included: !excluded.includes(key),
    };
  });
}

/** The deselected keys after one parameter is ticked or unticked. */
export function setIncluded(excluded: readonly string[], key: string, included: boolean): string[] {
  const rest = excluded.filter((candidate) => candidate !== key);
  return included ? rest : [...rest, key];
}

/**
 * The template without its deselected parameters, which is what the dry
 * run and the apply receive. A group left empty goes with them;
 * conditions stay, since one no parameter uses produces nothing.
 */
export function pruneTemplate(template: Template, excluded: readonly string[]): Template {
  if (excluded.length === 0) return template;
  const keep = <T>(parameters: Record<string, T>) =>
    Object.fromEntries(Object.entries(parameters).filter(([key]) => !excluded.includes(key)));
  const pruned: Template = { ...template };
  if (template.parameters) pruned.parameters = keep(template.parameters);
  if (template.parameterGroups) {
    pruned.parameterGroups = Object.fromEntries(
      Object.entries(template.parameterGroups)
        .map(([name, group]) => [name, { ...group, parameters: keep(group.parameters ?? {}) }])
        .filter(([, group]) => Object.keys((group as TemplateGroup).parameters ?? {}).length > 0),
    );
  }
  return pruned;
}

/** The description the server gives every rule that comes from a condition. */
// This is the server's text, matched as written rather than shown.
// eslint-disable-next-line lingui/no-unlocalized-strings
const conditionDescription = (name: string) => `Firebase condition ${JSON.stringify(name)}`;

/** What a condition became in one flow's rule: its conditions and, for `percent`, the share of units. */
export type MappedRule = {
  conditions: DocumentCondition[];
  rollout?: { percentage: number; unitType: string };
};

export type ConditionMapping = {
  name: string;
  expression: string;
  /** The parameters whose conditional values use the condition. */
  parameters: string[];
  /** The rules it became, each distinct one once. */
  rules: MappedRule[];
  /** The warnings that name the condition. */
  warnings: ImportWarning[];
  /** The parameters whose rule the server skipped because it cannot reproduce the condition. */
  skippedFor: string[];
};

/**
 * Each condition of the template beside the rules the dry run made of it
 * and the warnings that name it. The server's table decides what a
 * condition becomes; this only lines the answer up with the condition.
 */
export function conditionMappings(
  template: Template,
  result: FirebaseImportResult,
): ConditionMapping[] {
  const parameters = parametersOf(template);
  return (template.conditions ?? []).map((condition) => {
    const name = String(condition.name ?? "");
    const description = conditionDescription(name);
    const rules = new Map<string, MappedRule>();
    for (const flag of result.flags) {
      for (const rule of flag.rules) {
        if (rule.description !== description) continue;
        // Identifiers, salts and variations differ per flag; what the rule matches does not.
        const mapped: MappedRule = {
          conditions: rule.conditions,
          ...(rule.serve.kind === "rollout"
            ? {
                rollout: {
                  percentage: rule.serve.percentage ?? 0,
                  unitType: rule.serve.unit_type ?? "",
                },
              }
            : {}),
        };
        rules.set(JSON.stringify(mapped), mapped);
      }
    }
    const warnings = result.warnings.filter((warning) => warning.params?.condition === name);
    const skippedFor = [
      ...new Set(
        warnings
          .filter((warning) => warning.code === "condition_skipped")
          .map((warning) => String(warning.params?.parameter ?? "")),
      ),
    ];
    return {
      name,
      expression: String(condition.expression ?? ""),
      parameters: parameters
        .filter(({ parameter }) => Object.hasOwn(parameter.conditionalValues ?? {}, name))
        .map(({ key }) => key),
      rules: [...rules.values()],
      warnings,
      skippedFor,
    };
  });
}

/** The template as the file the importer reads. */
function templateBlob(template: Template): Blob {
  return new Blob([JSON.stringify(template)], { type: "application/json" });
}

/** Where the template is merged and on which unit type percent rollouts bucket. */
export type FirebaseTarget = { environment: string; unitType: string };

/** A dry run's answer with the `ETag` that pins the state of the project it judged. */
export type FirebaseDryRun = { result: FirebaseImportResult; etag: string };

async function send(
  project: string,
  template: Template,
  target: FirebaseTarget,
  dryRun: boolean,
  ifMatch?: string,
) {
  const settled = await importFirebaseTemplate({
    path: { project },
    query: { environment: target.environment, unit_type: target.unitType, dry_run: dryRun },
    body: templateBlob(template),
    headers: {
      "Content-Type": "application/json",
      ...(ifMatch === undefined ? {} : { "If-Match": ifMatch }),
    },
  });
  const result = await unwrap(Promise.resolve(settled));
  return { result, etag: settled.response?.headers.get("etag") ?? "" };
}

/** What importing the template into the environment would do. */
export function dryRunFirebase(
  project: string,
  template: Template,
  target: FirebaseTarget,
): Promise<FirebaseDryRun> {
  return send(project, template, target, true);
}

/** Imports the template, only if the project is still as the dry run found it. */
export async function applyFirebase(
  project: string,
  template: Template,
  target: FirebaseTarget,
  etag: string,
): Promise<FirebaseImportResult> {
  return (await send(project, template, target, false, etag || undefined)).result;
}

/** What the wizard keeps across a reload. */
export type FirebaseSession = {
  step: FirebaseStep;
  fileName?: string;
  /** The template as the file held it. */
  text?: string;
  excluded?: string[];
  environment?: string;
  unitType?: string;
};

const sessionKey = (project: string) => `flagnetic.import-firebase.${project}`;

function isSession(value: unknown): value is FirebaseSession {
  if (typeof value !== "object" || value === null) return false;
  const held = value as Partial<FirebaseSession>;
  return (
    typeof held.step === "string" &&
    firebaseSteps.includes(held.step) &&
    (held.fileName === undefined || typeof held.fileName === "string") &&
    (held.text === undefined || typeof held.text === "string") &&
    (held.excluded === undefined ||
      (Array.isArray(held.excluded) && held.excluded.every((key) => typeof key === "string"))) &&
    (held.environment === undefined || typeof held.environment === "string") &&
    (held.unitType === undefined || typeof held.unitType === "string")
  );
}

/**
 * The session kept for the project. A step past the upload whose
 * template was lost, or no longer reads, returns to the upload.
 */
export function loadFirebaseSession(project: string): FirebaseSession {
  const fresh: FirebaseSession = { step: "upload" };
  try {
    const raw = window.sessionStorage.getItem(sessionKey(project));
    if (raw === null) return fresh;
    const parsed: unknown = JSON.parse(raw);
    if (!isSession(parsed)) return fresh;
    if (
      parsed.step !== "upload" &&
      (parsed.text === undefined || !("template" in parseTemplate(parsed.text)))
    ) {
      return { ...parsed, step: "upload" };
    }
    return parsed;
  } catch {
    return fresh;
  }
}

/** Keeps the session, or forgets it when it is back at the start. */
export function saveFirebaseSession(project: string, session: FirebaseSession): void {
  try {
    if (session.step === "upload" && session.text === undefined) {
      window.sessionStorage.removeItem(sessionKey(project));
    } else {
      window.sessionStorage.setItem(sessionKey(project), JSON.stringify(session));
    }
  } catch {
    // Storage may be full or blocked. What an earlier step kept is dropped,
    // so a reload returns to the upload instead of resuming an older template.
    try {
      window.sessionStorage.removeItem(sessionKey(project));
    } catch {
      // Blocked storage keeps nothing to drop.
    }
  }
}
