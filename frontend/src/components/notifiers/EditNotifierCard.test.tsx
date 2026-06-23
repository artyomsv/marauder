import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { EditNotifierCard } from "./EditNotifierCard";
import { api } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  api: {
    getNotifier: vi.fn(),
    updateNotifier: vi.fn(),
  },
}));

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("EditNotifierCard", () => {
  beforeEach(() => vi.clearAllMocks());

  it("hydrates is_default and sends it on save", async () => {
    (api.getNotifier as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "n1", notifier_name: "webhook", display_name: "W", events: ["updated"],
      is_default: true, config: { url: "https://e.test/x" },
      created_at: "", updated_at: "",
    });
    (api.updateNotifier as ReturnType<typeof vi.fn>).mockResolvedValue({ id: "n1" });

    wrap(<EditNotifierCard id="n1" onClose={() => {}} onSaved={() => {}} />);

    const defaultCheckbox = await screen.findByLabelText(/default notifier/i);
    expect(defaultCheckbox).toBeChecked();

    await userEvent.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() => expect(api.updateNotifier).toHaveBeenCalledWith(
      "n1",
      expect.objectContaining({ is_default: true, display_name: "W" }),
    ));
  });
});
