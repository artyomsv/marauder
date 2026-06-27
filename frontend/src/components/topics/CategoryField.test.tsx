import { describe, it, expect } from "vitest";
import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { CategoryField } from "./CategoryField";

// Stateful host so the controlled field updates like it does in the real form.
function Host({ suggestions, initial = "" }: { suggestions: string[]; initial?: string }) {
  const [value, setValue] = useState(initial);
  return (
    <>
      <CategoryField value={value} onChange={setValue} suggestions={suggestions} />
      <output data-testid="val">{value}</output>
    </>
  );
}

const val = () => screen.getByTestId("val").textContent;

describe("CategoryField", () => {
  it("renders a plain text input when there are no suggestions", async () => {
    render(<Host suggestions={[]} />);
    expect(screen.queryByRole("combobox")).toBeNull();
    const input = screen.getByLabelText(/category/i);
    await userEvent.type(input, "anime");
    expect(val()).toBe("anime");
  });

  it("offers the categories plus a Custom option in a select", async () => {
    render(<Host suggestions={["TV", "Movies"]} />);
    const select = screen.getByRole("combobox");
    const labels = within(select)
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(labels).toEqual([
      "No category",
      "TV",
      "Movies",
      "✎ Custom category…",
    ]);

    await userEvent.selectOptions(select, "TV");
    expect(val()).toBe("TV");
  });

  it("reveals a free-text box when Custom is chosen and types through", async () => {
    render(<Host suggestions={["TV"]} />);
    await userEvent.selectOptions(screen.getByRole("combobox"), "__custom__");

    const textbox = screen.getByRole("textbox");
    // A brand-new category not in the list types through unhindered — the
    // dropdown suggests, it never constrains.
    await userEvent.type(textbox, "season-1");
    expect(val()).toBe("season-1");
  });

  it("opens in Custom mode when an existing value isn't in the list", () => {
    render(<Host suggestions={["TV", "Movies"]} initial="my/custom/path" />);
    expect(screen.getByRole("textbox")).toHaveValue("my/custom/path");
    expect(screen.getByRole("combobox")).toHaveValue("__custom__");
  });
});
