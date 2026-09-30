import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { CheckIntervalSelect } from "./CheckIntervalSelect";

describe("CheckIntervalSelect", () => {
  it("shows the presets and selects the current value", () => {
    render(<CheckIntervalSelect value={3600} onChange={() => {}} />);
    const select = screen.getByLabelText("Check interval") as HTMLSelectElement;
    expect(select.value).toBe("3600");
    expect(screen.getByRole("option", { name: "15 min" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "7 days" })).toBeInTheDocument();
  });

  it("reports the picked interval in seconds", async () => {
    const onChange = vi.fn();
    render(<CheckIntervalSelect value={900} onChange={onChange} />);
    await userEvent.selectOptions(screen.getByLabelText("Check interval"), "86400");
    expect(onChange).toHaveBeenCalledWith(86400);
  });

  // A topic whose interval was set through the API to a non-preset value must
  // keep it: without its own option the select would silently show (and then
  // save) the first preset instead.
  it("keeps a non-preset value as its own option", () => {
    render(<CheckIntervalSelect value={7200} onChange={() => {}} />);
    const select = screen.getByLabelText("Check interval") as HTMLSelectElement;
    expect(select.value).toBe("7200");
    expect(screen.getByRole("option", { name: "2 h" })).toBeInTheDocument();
  });
});
