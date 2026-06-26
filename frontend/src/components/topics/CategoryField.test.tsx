import { describe, it, expect } from "vitest";
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { CategoryField } from "./CategoryField";

// Stateful host so the controlled input accumulates typed text like it does in
// the real form.
function Host({ suggestions }: { suggestions: string[] }) {
  const [value, setValue] = useState("");
  return (
    <CategoryField value={value} onChange={setValue} suggestions={suggestions} />
  );
}

describe("CategoryField", () => {
  it("renders a datalist with the supplied suggestions", () => {
    const { container } = render(
      <CategoryField value="" onChange={() => {}} suggestions={["TV", "Movies"]} />,
    );
    const input = screen.getByLabelText(/category/i);
    const datalist = container.querySelector("datalist");
    expect(datalist).not.toBeNull();
    // The input's `list` attribute points at the datalist's (generated) id.
    expect(input.getAttribute("list")).toBe(datalist!.id);

    const options = datalist!.querySelectorAll("option");
    expect(Array.from(options).map((o) => o.getAttribute("value"))).toEqual([
      "TV",
      "Movies",
    ]);
  });

  it("renders no datalist when there are no suggestions (free-text only)", () => {
    const { container } = render(
      <CategoryField value="" onChange={() => {}} suggestions={[]} />,
    );
    expect(screen.getByLabelText(/category/i)).not.toHaveAttribute("list");
    expect(container.querySelector("datalist")).toBeNull();
  });

  it("still accepts free-text typing when suggestions exist", async () => {
    render(<Host suggestions={["TV"]} />);
    const input = screen.getByLabelText(/category/i);
    await userEvent.type(input, "anime");
    // A brand-new category (not in the suggestions) types through unhindered —
    // the datalist suggests, it never constrains.
    expect(input).toHaveValue("anime");
  });
});
