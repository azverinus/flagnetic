import { describe, expect, it } from "vitest";

import {
  isFlagsFiltered,
  listFlagsQueryOf,
  resolveEnvironment,
  validateFlagsSearch,
} from "./flags";

describe("the flags list's search", () => {
  it("keeps every filter it can use, trimmed", () => {
    expect(
      validateFlagsSearch({
        q: "  checkout ",
        type: "json",
        tag: " web ",
        owner: "0190a3c1-0000-7000-8000-000000000001",
        archived: true,
        cursor: "abc",
      }),
    ).toEqual({
      q: "checkout",
      type: "json",
      tag: "web",
      owner: "0190a3c1-0000-7000-8000-000000000001",
      archived: true,
      cursor: "abc",
    });
  });

  it("drops what the server would refuse rather than refusing the address", () => {
    expect(
      validateFlagsSearch({ q: "   ", type: "colour", tag: "", archived: "yes", sort: 3 }),
    ).toEqual({});
  });

  it("reads archived only when it is set, as the address writes a boolean", () => {
    expect(validateFlagsSearch({ archived: "true" })).toEqual({ archived: true });
    expect(validateFlagsSearch({ archived: false })).toEqual({});
  });

  it("counts every filter but the page", () => {
    expect(isFlagsFiltered({})).toBe(false);
    expect(isFlagsFiltered({ cursor: "abc" })).toBe(false);
    expect(isFlagsFiltered({ tag: "web" })).toBe(true);
    expect(isFlagsFiltered({ archived: true })).toBe(true);
  });

  it("keeps the environment, its state, the environments to compare and the order", () => {
    expect(
      validateFlagsSearch({
        environment: "staging",
        state: "rollout",
        differs_between: "staging,production",
        sort: "updated",
      }),
    ).toEqual({
      environment: "staging",
      state: "rollout",
      differs_between: "staging,production",
      sort: "updated",
    });
  });

  it("keeps the experiment state, which lists flags with a rule serving an experiment", () => {
    expect(validateFlagsSearch({ environment: "staging", state: "experiment" })).toEqual({
      environment: "staging",
      state: "experiment",
    });
  });

  it("drops a state, a comparison or an order the server would refuse", () => {
    expect(
      validateFlagsSearch({
        state: "running",
        differs_between: "staging,staging",
        sort: "owner",
      }),
    ).toEqual({});
    expect(validateFlagsSearch({ differs_between: "staging" })).toEqual({});
    expect(validateFlagsSearch({ differs_between: "staging,production,qa" })).toEqual({});
    expect(validateFlagsSearch({ differs_between: "Staging,production" })).toEqual({});
  });

  it("counts neither the environment nor the order as a filter, but the state and comparison", () => {
    expect(isFlagsFiltered({ environment: "staging", sort: "name" })).toBe(false);
    expect(isFlagsFiltered({ environment: "staging", state: "on" })).toBe(true);
    expect(isFlagsFiltered({ differs_between: "staging,production" })).toBe(true);
  });
});

describe("the flags list's request", () => {
  it("sends the state with the environment it is judged in", () => {
    expect(listFlagsQueryOf({ environment: "staging", state: "off", q: "checkout" })).toEqual({
      environment: "staging",
      state: "off",
      q: "checkout",
    });
  });

  it("sends the two environments to compare as the pair the API takes", () => {
    expect(listFlagsQueryOf({ differs_between: "staging,production", sort: "key" })).toEqual({
      differs_between: ["staging", "production"],
      sort: "key",
    });
  });

  it("leaves out a state with no environment to judge it in", () => {
    expect(listFlagsQueryOf({ state: "on" })).toEqual({});
  });
});

describe("the environment the flags list shows", () => {
  const keys = ["development", "staging", "production"];

  it("is the one the address names, when the project has it", () => {
    expect(resolveEnvironment(keys, "production", "staging")).toBe("production");
  });

  it("is the one shown last, when the address names none or one the project lacks", () => {
    expect(resolveEnvironment(keys, undefined, "staging")).toBe("staging");
    expect(resolveEnvironment(keys, "sandbox", "staging")).toBe("staging");
  });

  it("is the first, when neither names one of the project's", () => {
    expect(resolveEnvironment(keys, "sandbox", "qa")).toBe("development");
    expect(resolveEnvironment(keys, undefined, null)).toBe("development");
  });

  it("is none in a project without environments", () => {
    expect(resolveEnvironment([], "staging", "staging")).toBeUndefined();
  });
});
