import type { Flag } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { KillSwitchDialog } from "./kill-switch-dialog";

beforeAll(() => activateForTests("en"));

const flag = { id: "f1", key: "checkout", variations: [] } as unknown as Flag;
const reason = "Payments are failing for everyone.";

function server() {
  return fakeServer({
    "GET /projects/shop/audit": { status: 200, body: { items: [] } },
    "POST /projects/shop/flags/checkout/environments/production/kill": {
      status: 200,
      body: { changed: true, configuration: { environment: "production", on: false } },
    },
  });
}

function renderDialog(isProtected: boolean) {
  return renderWithProviders(
    <KillSwitchDialog
      project="shop"
      flag={flag}
      environment={{ id: "e1", key: "production", name: "Production", protected: isProtected }}
      open
      onOpenChange={() => {}}
    />,
  );
}

describe("the kill switch dialog", () => {
  it("asks for the flag's key before switching off in a protected environment", async () => {
    const { requests } = server();
    renderDialog(true);

    await userEvent.type(await screen.findByLabelText("Reason"), reason);
    const confirm = screen.getByRole("button", { name: "Switch off" });
    expect(confirm).toBeDisabled();

    const key = screen.getByLabelText("Type checkout to confirm");
    await userEvent.type(key, "checkou");
    expect(confirm).toBeDisabled();

    await userEvent.type(key, "t");
    expect(confirm).toBeEnabled();
    await userEvent.click(confirm);

    await waitFor(() => expect(requests.some((r) => r.method === "POST")).toBe(true));
    const kill = requests.find((r) => r.method === "POST")!;
    expect(await kill.json()).toEqual({ reason });
  });

  it("asks for the reason only in an unprotected environment", async () => {
    server();
    renderDialog(false);

    await userEvent.type(await screen.findByLabelText("Reason"), reason);

    expect(screen.queryByLabelText(/^Type .* to confirm$/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Switch off" })).toBeEnabled();
  });
});
