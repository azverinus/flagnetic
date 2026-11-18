import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { Lamp } from "./lamp";

beforeEach(() => activateForTests("en"));

describe("Lamp", () => {
  it("says On beside a lit lamp", () => {
    renderWithProviders(<Lamp on data-testid="lamp" />);
    expect(screen.getByTestId("lamp")).toHaveTextContent("On");
  });

  it("says Off beside a hollow one, so the state never rests on colour", () => {
    renderWithProviders(<Lamp on={false} data-testid="lamp" />);
    expect(screen.getByTestId("lamp")).toHaveTextContent("Off");
  });
});
