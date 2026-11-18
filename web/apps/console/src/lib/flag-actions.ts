/**
 * What the flag's context panel asks of the flag's page. The panel is drawn
 * by the shell, outside the page, yet its "Compare environments" and "Edit
 * definition" open dialogs the page owns, beside the configuration they
 * concern; the page registers how while it is shown, and the panel calls
 * it. No registration, no effect: a panel never outlives its page.
 */
export type FlagAction = "compare" | "edit";

type Handlers = Partial<Record<FlagAction, () => void>>;

let registered: Handlers = {};

/** Registers the page's handlers; the returned function withdraws them. */
export function registerFlagActions(handlers: Handlers): () => void {
  registered = handlers;
  return () => {
    if (registered === handlers) registered = {};
  };
}

export function runFlagAction(action: FlagAction): void {
  registered[action]?.();
}
