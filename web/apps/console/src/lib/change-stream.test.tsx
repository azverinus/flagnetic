import { act, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { NamedConflictBanner } from "@/components/conflict-banner";
import { activateForTests, renderWithProviders, testQueryClient } from "@/test/render";

import { ChangeStream, noticeNames, useNewerVersionNotice } from "./change-stream";
import { useFormConflict } from "./use-form-conflict";

beforeAll(() => activateForTests("en"));

class FakeSource extends EventTarget {
  closed = false;
  readyState = 1;
  close() {
    this.closed = true;
    this.readyState = 2;
  }
  send(type: string, data: unknown, id = "") {
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data), lastEventId: id }));
  }
}

function stream() {
  const source = new FakeSource();
  return { source, open: () => source as unknown as EventSource };
}

type Thing = { id: string; key: string; name: string; version: number };

/** A form of one thing with the conflict banner, as the console's forms are. */
function Form({
  dirty,
  saving = false,
  read,
}: {
  dirty: boolean;
  saving?: boolean;
  read: () => Promise<Thing>;
}) {
  const initial: Thing = { id: "t1", key: "web", name: "Web", version: 1 };
  const conflicts = useFormConflict({ project: "shop", kind: "thing", initial, read });
  const [pending] = useState(false);
  useNewerVersionNotice(
    { kind: "thing", key: "web" },
    { dirty, version: conflicts.version, saving },
    conflicts.meetNewer,
  );
  return conflicts.conflict ? (
    <NamedConflictBanner
      base={initial}
      conflict={conflicts.conflict}
      pending={pending}
      onReload={() => {}}
      onKeep={() => {}}
    />
  ) : (
    <p>editing</p>
  );
}

describe("the change stream", () => {
  it("marks stale what a notice names, and everything of the project on refetch", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(["projects", "shop", "segments", "list"], []);
    queryClient.setQueryData(["projects", "shop", "flags", "list"], []);
    const { source, open } = stream();
    renderWithProviders(
      <ChangeStream project="shop" open={open}>
        <p>page</p>
      </ChangeStream>,
      queryClient,
    );
    const stale = (key: string[]) => queryClient.getQueryState(key)?.isInvalidated;

    act(() =>
      source.send(
        "change",
        { kind: "attribute", key: "plan", environment: null, version: 2 },
        "e1",
      ),
    );
    expect(stale(["projects", "shop", "segments", "list"])).toBe(false);

    act(() =>
      source.send("change", { kind: "segment", key: "beta", environment: null, version: 2 }, "e2"),
    );
    expect(stale(["projects", "shop", "segments", "list"])).toBe(true);
    expect(stale(["projects", "shop", "flags", "list"])).toBe(true);
  });

  it("refetches the whole project on a refetch event, and closes with the page", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(["projects", "shop", "tokens"], []);
    const { source, open } = stream();
    const { unmount } = renderWithProviders(
      <ChangeStream project="shop" open={open}>
        <p>page</p>
      </ChangeStream>,
      queryClient,
    );
    act(() => source.send("refetch", {}));
    expect(queryClient.getQueryState(["projects", "shop", "tokens"])?.isInvalidated).toBe(true);
    unmount();
    expect(source.closed).toBe(true);
  });
});

describe("a stream the browser gave up on", () => {
  afterEach(() => vi.useRealTimers());

  it("is opened again, and the project read again in full once it is", () => {
    vi.useFakeTimers();
    const queryClient = testQueryClient();
    queryClient.setQueryData(["projects", "shop", "tokens"], []);
    const sources: FakeSource[] = [];
    const open = () => {
      const source = new FakeSource();
      sources.push(source);
      return source as unknown as EventSource;
    };
    renderWithProviders(
      <ChangeStream project="shop" open={open}>
        <p>page</p>
      </ChangeStream>,
      queryClient,
    );
    const [first] = sources;
    // A dropped connection the browser retries itself is left to it.
    act(() => first!.dispatchEvent(new Event("error")));
    act(() => vi.advanceTimersByTime(120_000));
    expect(sources).toHaveLength(1);

    first!.readyState = 2;
    act(() => first!.dispatchEvent(new Event("error")));
    act(() => vi.advanceTimersByTime(1_000));
    expect(sources).toHaveLength(2);
    expect(queryClient.getQueryState(["projects", "shop", "tokens"])?.isInvalidated).toBe(false);

    act(() => sources[1]!.dispatchEvent(new Event("open")));
    expect(queryClient.getQueryState(["projects", "shop", "tokens"])?.isInvalidated).toBe(true);
  });
});

describe("noticeNames", () => {
  const notice = { id: "e", kind: "flag", key: "checkout", environment: "prod", version: 2 };

  it("names an entity of no environment only by a notice of none", () => {
    expect(noticeNames(notice, { kind: "flag", key: "checkout", environment: null })).toBe(false);
    expect(
      noticeNames(
        { ...notice, environment: null },
        { kind: "flag", key: "checkout", environment: null },
      ),
    ).toBe(true);
    expect(noticeNames(notice, { kind: "flag", key: "checkout", environment: "prod" })).toBe(true);
    expect(noticeNames(notice, { kind: "flag", key: "checkout" })).toBe(true);
  });
});

describe("the conflict banner on a notice", () => {
  const newer: Thing = { id: "t1", key: "web", name: "Web shop", version: 2 };
  const notice = { kind: "thing", key: "web", environment: null, version: 2 };

  it("shows at once for a form with unsaved edits when someone saved a newer version", async () => {
    const { source, open } = stream();
    renderWithProviders(
      <ChangeStream project="shop" open={open}>
        <Form dirty read={async () => newer} />
      </ChangeStream>,
    );
    act(() => source.send("change", notice, "e1"));
    expect(await screen.findByRole("alert")).toHaveTextContent("Someone saved a change");
  });

  async function quiet(dirty: boolean, sent: object[]) {
    const { source, open } = stream();
    let reads = 0;
    renderWithProviders(
      <ChangeStream project="shop" open={open}>
        <Form
          dirty={dirty}
          read={async () => {
            reads += 1;
            return newer;
          }}
        />
      </ChangeStream>,
    );
    for (const data of sent) act(() => source.send("change", data, "e"));
    await waitFor(() => expect(screen.getByText("editing")).toBeVisible());
    expect(reads).toBe(0);
    expect(screen.queryByRole("alert")).toBeNull();
  }

  it("stays quiet for a form without unsaved edits", () => quiet(false, [notice]));

  // The form's own save names the version the form holds.
  it("stays quiet for the form's own save", () => quiet(true, [{ ...notice, version: 1 }]));

  it("stays quiet for another entity", () => quiet(true, [{ ...notice, key: "mobile" }]));

  it("stays quiet while the form is saving: the notice may be of its own save", async () => {
    const { source, open } = stream();
    let reads = 0;
    renderWithProviders(
      <ChangeStream project="shop" open={open}>
        <Form
          dirty
          saving
          read={async () => {
            reads += 1;
            return newer;
          }}
        />
      </ChangeStream>,
    );
    act(() => source.send("change", notice, "e1"));
    await waitFor(() => expect(screen.getByText("editing")).toBeVisible());
    expect(reads).toBe(0);
  });

  it("stays quiet when the form moved on while the newer version was read", async () => {
    const { source, open } = stream();
    let answer: (thing: Thing) => void = () => {};
    const read = () => new Promise<Thing>((resolve) => (answer = resolve));
    function Saving() {
      const [dirty, setDirty] = useState(true);
      return (
        <>
          <button type="button" onClick={() => setDirty(false)}>
            Saved
          </button>
          <Form dirty={dirty} read={read} />
        </>
      );
    }
    renderWithProviders(
      <ChangeStream project="shop" open={open}>
        <Saving />
      </ChangeStream>,
    );
    act(() => source.send("change", notice, "e1"));
    act(() => screen.getByRole("button", { name: "Saved" }).click());
    await act(async () => answer(newer));
    expect(screen.getByText("editing")).toBeVisible();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
