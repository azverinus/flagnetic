// The anonymous ID helper: one ID per origin and unit type, kept outside
// every SDK key's storage, so a rotated key keeps anonymous visitors in
// their experiment arm. It runs in Node and in a real browser.
import { describe, expect, test } from "vitest";
import { anonymousIdIn } from "../src/client/anonymous.js";
import { MemoryStore, STORE_PREFIX, StoreKeys } from "../src/client/storage.js";
import type { ClientStore } from "../src/client/types.js";

const uuidV7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe("anonymousId", () => {
  test("the first call mints a UUIDv7 and every later one returns it", () => {
    const store = new MemoryStore();
    const id = anonymousIdIn(store, "anonymous");
    expect(id).toMatch(uuidV7);
    expect(anonymousIdIn(store, "anonymous")).toBe(id);
  });

  test("each unit type has its own ID", () => {
    const store = new MemoryStore();
    expect(anonymousIdIn(store, "device")).not.toBe(anonymousIdIn(store, "anonymous"));
  });

  test("the ID lives outside every SDK key's scope, so a key rotation keeps it", () => {
    const store = new MemoryStore();
    const id = anonymousIdIn(store, "anonymous");
    // An SDK key names its keys by a digest of itself; this one names none.
    expect(store.getItem(`${STORE_PREFIX}anonymous.anonymous`)).toBe(id);
    const keyScope = new StoreKeys("fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD").events.split(".")[1] ?? "";
    expect(`${STORE_PREFIX}anonymous.anonymous`).not.toContain(keyScope);
  });

  test("an ID already stored is kept as it is", () => {
    const store = new MemoryStore();
    store.setItem(`${STORE_PREFIX}anonymous.anonymous`, "d-8f1c");
    expect(anonymousIdIn(store, "anonymous")).toBe("d-8f1c");
  });

  test("a storage that throws, or none, still gives one ID for the page", () => {
    const throwing: ClientStore = {
      getItem: () => {
        throw new Error("SecurityError");
      },
      setItem: () => {
        throw new Error("QuotaExceededError");
      },
      removeItem: () => undefined,
    };
    const id = anonymousIdIn(throwing, "throwing");
    expect(id).toMatch(uuidV7);
    expect(anonymousIdIn(throwing, "throwing")).toBe(id);
    const none = anonymousIdIn(undefined, "none");
    expect(anonymousIdIn(undefined, "none")).toBe(none);
  });
});
