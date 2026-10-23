import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { FolderNameFields, type FolderNameValue } from "./FolderNameFields";

const DEFAULT: FolderNameValue = { folderNameMode: "default", folderName: "" };

describe("FolderNameFields", () => {
  it("offers the three modes", () => {
    render(<FolderNameFields value={DEFAULT} onChange={() => {}} unsupportedClient={null} />);
    const select = screen.getByLabelText("Folder name in the client") as HTMLSelectElement;
    expect(Array.from(select.options).map((o) => o.value)).toEqual(["default", "keep", "custom"]);
  });

  it("reports a mode change", async () => {
    const onChange = vi.fn();
    render(<FolderNameFields value={DEFAULT} onChange={onChange} unsupportedClient={null} />);
    await userEvent.selectOptions(screen.getByLabelText("Folder name in the client"), "keep");
    expect(onChange).toHaveBeenCalledWith({ folderNameMode: "keep", folderName: "" });
  });

  it("shows the name input only in custom mode", () => {
    const { rerender } = render(
      <FolderNameFields value={DEFAULT} onChange={() => {}} unsupportedClient={null} />,
    );
    expect(screen.queryByLabelText("Custom folder name")).toBeNull();
    rerender(
      <FolderNameFields
        value={{ folderNameMode: "custom", folderName: "My Show" }}
        onChange={() => {}}
        unsupportedClient={null}
      />,
    );
    expect((screen.getByLabelText("Custom folder name") as HTMLInputElement).value).toBe("My Show");
  });

  it("reports the typed name", async () => {
    const onChange = vi.fn();
    render(
      <FolderNameFields
        value={{ folderNameMode: "custom", folderName: "" }}
        onChange={onChange}
        unsupportedClient={null}
      />,
    );
    await userEvent.type(screen.getByLabelText("Custom folder name"), "X");
    expect(onChange).toHaveBeenLastCalledWith({ folderNameMode: "custom", folderName: "X" });
  });

  it("warns only when a mode is set and the client cannot rename", () => {
    const { rerender } = render(
      <FolderNameFields value={DEFAULT} onChange={() => {}} unsupportedClient="µTorrent box" />,
    );
    expect(screen.queryByText(/µTorrent box cannot rename folders/)).toBeNull();
    rerender(
      <FolderNameFields
        value={{ folderNameMode: "keep", folderName: "" }}
        onChange={() => {}}
        unsupportedClient="µTorrent box"
      />,
    );
    expect(screen.getByText(/µTorrent box cannot rename folders/)).toBeInTheDocument();
  });
});
