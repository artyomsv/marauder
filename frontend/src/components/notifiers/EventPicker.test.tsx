import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EventPicker } from "@/components/notifiers/EventPicker";

describe("EventPicker", () => {
  it("renders a checkbox per notifiable event and toggles", async () => {
    const onChange = vi.fn();
    render(<EventPicker value={["release.found"]} onChange={onChange} />);
    const completed = screen.getByLabelText(/download finished/i);
    expect(completed).not.toBeChecked();
    await userEvent.click(completed);
    expect(onChange).toHaveBeenCalledWith(
      expect.arrayContaining(["release.found", "download.completed"]),
    );
  });

  it("shows legacy 'updated' as the three release-group boxes checked", () => {
    render(<EventPicker value={["updated", "error"]} onChange={() => {}} />);
    expect(screen.getByLabelText(/new release/i)).toBeChecked();
    expect(screen.getByLabelText(/sent to client/i)).toBeChecked();
    expect(screen.getByLabelText(/download finished/i)).toBeChecked();
    expect(screen.getByLabelText(/error/i)).toBeChecked();
  });
});
