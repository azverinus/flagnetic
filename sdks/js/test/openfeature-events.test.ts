// ProviderState in the window between `initialize` resolving and its
// announcement, the next task. A client reports over the network, so what
// it reports in that window depends on timing; here a stand-in client
// reports it at a chosen moment.
import { describe, expect, test } from "vitest";
import type { Stage } from "../src/internal/detail.js";
import { ERROR, ProviderState, READY, STALE } from "../src/openfeature/events.js";

class StandInClient {
  stage: Stage = "fresh";
  private readonly stageListeners: ((stage: Stage) => void)[] = [];

  onChange(): () => void {
    return () => undefined;
  }

  onStage(listener: (stage: Stage) => void): () => void {
    this.stageListeners.push(listener);
    return () => undefined;
  }

  move(stage: Stage): void {
    this.stage = stage;
    for (const listener of this.stageListeners) listener(stage);
  }
}

function provider() {
  const client = new StandInClient();
  const sent: string[] = [];
  const state = new ProviderState(client, { emit: (event) => sent.push(event) });
  return { client, sent, state };
}

const nextTask = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

describe("an event in the window before the announcement", () => {
  test("a key revoked after initialize resolved, before the announcement: one ERROR", async () => {
    const { sent, state } = provider();
    state.initializeResolved();
    state.observe("key_revoked");
    await nextTask();
    expect(sent).toStrictEqual([ERROR]);
  });

  test("a move to last_good after initialize resolved, before the announcement: one STALE", async () => {
    const { client, sent, state } = provider();
    state.initializeResolved();
    client.move("last_good");
    await nextTask();
    expect(sent).toStrictEqual([STALE]);
  });

  test("a context change resolving after the key was revoked: one ERROR", async () => {
    const { sent, state } = provider();
    state.initializeResolved();
    await nextTask();
    state.change();
    state.changeResolved();
    state.observe("key_revoked");
    await nextTask();
    expect(sent).toStrictEqual([ERROR]);
  });

  test("a recovery in the window is sent, and the announcement adds nothing", async () => {
    const { client, sent, state } = provider();
    client.stage = "last_good";
    state.initializeResolved();
    client.move("fresh");
    await nextTask();
    expect(sent).toStrictEqual([READY]);
  });
});

describe("the announcement", () => {
  test("a key revoked before initialize resolved: ERROR in the next task", async () => {
    const { sent, state } = provider();
    state.observe("key_revoked");
    state.initializeResolved();
    expect(sent).toStrictEqual([]);
    await nextTask();
    expect(sent).toStrictEqual([ERROR]);
  });

  test("last_good before initialize resolved: STALE in the next task", async () => {
    const { client, sent, state } = provider();
    client.stage = "last_good";
    state.initializeResolved();
    expect(sent).toStrictEqual([]);
    await nextTask();
    expect(sent).toStrictEqual([STALE]);
  });
});
