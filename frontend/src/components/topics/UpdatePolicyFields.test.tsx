import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UpdatePolicyFields, type UpdatePolicyValue } from "./UpdatePolicyFields";

const OFF: UpdatePolicyValue = {
  replaceOnUpdate: false,
  replaceDeleteData: true,
  addPausedOnUpdate: false,
  onlyNewFiles: false,
};

describe("UpdatePolicyFields", () => {
  it("reports the add-paused checkbox", async () => {
    const onChange = vi.fn();
    render(<UpdatePolicyFields value={OFF} onChange={onChange} episodic={false} unsupportedClient={null} />);
    await userEvent.click(screen.getByLabelText("Add updates paused"));
    expect(onChange).toHaveBeenCalledWith({ ...OFF, addPausedOnUpdate: true });
  });

  it("turns delete-files off when only-new-files is switched on", async () => {
    const onChange = vi.fn();
    const value = { ...OFF, replaceOnUpdate: true, replaceDeleteData: true };
    render(<UpdatePolicyFields value={value} onChange={onChange} episodic={false} unsupportedClient={null} />);
    await userEvent.click(screen.getByLabelText("Download only new files"));
    expect(onChange).toHaveBeenCalledWith({ ...value, onlyNewFiles: true, replaceDeleteData: false });
  });

  it("locks delete-files while only-new-files is on", () => {
    const value = { ...OFF, replaceOnUpdate: true, replaceDeleteData: false, onlyNewFiles: true };
    render(<UpdatePolicyFields value={value} onChange={() => {}} episodic={false} unsupportedClient={null} />);
    const del = screen.getByLabelText("Also delete the old files from disk") as HTMLInputElement;
    expect(del.disabled).toBe(true);
    expect(del.checked).toBe(false);
    expect(screen.getByText(/old files would be lost/i)).toBeInTheDocument();
  });

  it("hides the new settings for per-episode trackers", () => {
    render(<UpdatePolicyFields value={OFF} onChange={() => {}} episodic={true} unsupportedClient={null} />);
    expect(screen.queryByLabelText("Add updates paused")).toBeNull();
    expect(screen.queryByLabelText("Download only new files")).toBeNull();
    expect(screen.getByLabelText("Replace previous version on update")).toBeInTheDocument();
  });

  it("warns when the client cannot pause or select files", () => {
    const value = { ...OFF, onlyNewFiles: true };
    render(<UpdatePolicyFields value={value} onChange={() => {}} episodic={false} unsupportedClient="µTorrent box" />);
    expect(screen.getByText(/µTorrent box cannot pause or select files/)).toBeInTheDocument();
  });
});
