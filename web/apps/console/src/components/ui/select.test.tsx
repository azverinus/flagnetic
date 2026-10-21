import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Button } from "./button";
import { Field } from "./field";
import { Select, SelectItem } from "./select";

function TypeField() {
  return (
    <Field label="Type">
      <Select defaultValue="string">
        <SelectItem value="string">String</SelectItem>
        <SelectItem value="number">Number</SelectItem>
        <SelectItem value="boolean">Boolean</SelectItem>
      </Select>
    </Field>
  );
}

describe("a select", () => {
  it("is named by its field and chosen from with the keyboard alone", async () => {
    const user = userEvent.setup();
    renderWithProviders(<TypeField />);
    const select = screen.getByRole("combobox", { name: "Type" });
    expect(select).toHaveTextContent("String");

    select.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("listbox")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "String" })).toHaveAttribute("aria-selected", "true");

    await user.keyboard("{ArrowDown}{Enter}");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(select).toHaveTextContent("Number");
    expect(select).toHaveFocus();
  });

  it("opens with the space bar, closes on Escape without choosing and gives focus back", async () => {
    const user = userEvent.setup();
    renderWithProviders(<TypeField />);
    const select = screen.getByRole("combobox", { name: "Type" });

    select.focus();
    await user.keyboard(" ");
    expect(await screen.findByRole("listbox")).toBeInTheDocument();
    await user.keyboard("{ArrowDown}{Escape}");

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(select).toHaveTextContent("String");
    expect(select).toHaveFocus();
  });

  it("chooses by typing the start of an option while closed", async () => {
    const user = userEvent.setup();
    renderWithProviders(<TypeField />);
    const select = screen.getByRole("combobox", { name: "Type" });

    select.focus();
    await user.keyboard("b");

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(select).toHaveTextContent("Boolean");
  });

  it("keeps its options within the viewport and wraps a long one", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Field label="Environment">
        <Select>
          <SelectItem value="long">
            An environment whose name is longer than any phone is wide, and longer still
          </SelectItem>
        </Select>
      </Field>,
    );

    await user.click(screen.getByRole("combobox", { name: "Environment" }));
    const listbox = await screen.findByRole("listbox");

    // jsdom lays nothing out, so the bound is read from the classes that set
    // it: no wider than the room Radix measures beside the trigger.
    expect(listbox).toHaveClass("max-w-(--radix-select-content-available-width)");
    expect(screen.getByRole("option")).toHaveClass("break-words");
  });

  it("reads its hint and its error with it and is marked invalid", () => {
    renderWithProviders(
      <Field label="Type" hint="What the flag serves." error="Choose a type.">
        <Select>
          <SelectItem value="string">String</SelectItem>
        </Select>
      </Field>,
    );
    const select = screen.getByRole("combobox", { name: "Type" });

    expect(select).toHaveAccessibleDescription("What the flag serves. Choose a type.");
    expect(select).toHaveAttribute("aria-invalid", "true");
  });

  it("shows its placeholder until an option is chosen", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Field label="Type">
        <Select placeholder="Choose a type">
          <SelectItem value="string">String</SelectItem>
          <SelectItem value="number">Number</SelectItem>
        </Select>
      </Field>,
    );
    const select = screen.getByRole("combobox", { name: "Type" });
    expect(select).toHaveTextContent("Choose a type");

    await user.click(select);
    await user.click(await screen.findByRole("option", { name: "Number" }));
    expect(select).toHaveTextContent("Number");
    expect(select).not.toHaveTextContent("Choose a type");
  });

  it("follows the value it is given and reports the one chosen", async () => {
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    function Controlled() {
      const [value, setValue] = useState("string");
      return (
        <>
          <Field label="Type">
            <Select
              value={value}
              onValueChange={(next) => {
                onValueChange(next);
                setValue(next);
              }}
            >
              <SelectItem value="string">String</SelectItem>
              <SelectItem value="number">Number</SelectItem>
            </Select>
          </Field>
          <Button onClick={() => setValue("number")}>Reset to number</Button>
        </>
      );
    }
    renderWithProviders(<Controlled />);
    const select = screen.getByRole("combobox", { name: "Type" });

    await user.click(screen.getByRole("button", { name: "Reset to number" }));
    expect(select).toHaveTextContent("Number");
    expect(onValueChange).not.toHaveBeenCalled();

    await user.click(select);
    await user.click(await screen.findByRole("option", { name: "String" }));
    expect(onValueChange).toHaveBeenCalledWith("string");
    expect(select).toHaveTextContent("String");
  });

  it("cannot be opened while disabled", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Field label="Type">
        <Select defaultValue="string" disabled>
          <SelectItem value="string">String</SelectItem>
          <SelectItem value="number">Number</SelectItem>
        </Select>
      </Field>,
    );
    const select = screen.getByRole("combobox", { name: "Type" });

    expect(select).toBeDisabled();
    await user.click(select);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("gives react-hook-form its value and takes focus on its error", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    function TypeForm() {
      const form = useForm<{ type: string }>({ defaultValues: { type: "" } });
      return (
        <form onSubmit={form.handleSubmit(onSubmit)}>
          <Controller
            control={form.control}
            name="type"
            rules={{ required: "Choose a type." }}
            render={({ field, fieldState }) => (
              <Field label="Type" error={fieldState.error?.message}>
                <Select
                  name={field.name}
                  value={field.value}
                  onValueChange={field.onChange}
                  onBlur={field.onBlur}
                  ref={field.ref}
                  placeholder="Choose a type"
                >
                  <SelectItem value="string">String</SelectItem>
                  <SelectItem value="number">Number</SelectItem>
                </Select>
              </Field>
            )}
          />
          <Button type="submit">Create</Button>
        </form>
      );
    }
    renderWithProviders(<TypeForm />);
    const select = screen.getByRole("combobox", { name: "Type" });

    await user.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("Choose a type.")).toBeInTheDocument();
    expect(select).toHaveFocus();
    expect(onSubmit).not.toHaveBeenCalled();

    await user.keyboard("{Enter}");
    await user.click(await screen.findByRole("option", { name: "Number" }));
    await user.click(screen.getByRole("button", { name: "Create" }));
    expect(onSubmit).toHaveBeenCalledWith({ type: "number" }, expect.anything());
    expect(screen.queryByText("Choose a type.")).not.toBeInTheDocument();
  });
});
