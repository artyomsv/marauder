import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TitleSyncToggle } from "./TitleSyncToggle";

describe("TitleSyncToggle", () => {
  it("renders the stored state", () => {
    render(<TitleSyncToggle checked={true} onChange={() => {}} />);
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    expect(box.checked).toBe(true);
  });

  it("reports a toggle", async () => {
    const onChange = vi.fn();
    render(<TitleSyncToggle checked={false} onChange={onChange} />);
    await userEvent.click(screen.getByLabelText(/follow the tracker's title/i));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("explains that a typed name turns it off", () => {
    render(<TitleSyncToggle checked={false} onChange={() => {}} />);
    expect(screen.getByText(/type a name yourself/i)).toBeInTheDocument();
  });
});
