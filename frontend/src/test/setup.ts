// Test setup — runs once per test file before any tests execute.
//
// 1. Wires @testing-library/jest-dom matchers (toBeInTheDocument,
//    toBeDisabled, etc.) into vitest's expect.
// 2. Calls cleanup() after every test so React Testing Library
//    unmounts trees between tests, even though we set globals: true
//    (vitest only auto-cleans when running with the jest-dom preset
//    in some setups; explicit afterEach is the safe path).
import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});
