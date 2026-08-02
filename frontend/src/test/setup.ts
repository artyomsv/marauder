// Test setup — runs once per test file before any tests execute.
//
// 1. Wires @testing-library/jest-dom matchers (toBeInTheDocument,
//    toBeDisabled, etc.) into vitest's expect.
// 2. Calls cleanup() after every test so React Testing Library
//    unmounts trees between tests, even though we set globals: true
//    (vitest only auto-cleans when running with the jest-dom preset
//    in some setups; explicit afterEach is the safe path).
import "@testing-library/jest-dom/vitest";
import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";

// 3. Radix primitives (the dropdown menu behind a topic row's actions) call
//    Pointer Capture and scrollIntoView, neither of which jsdom implements.
//    Without these stubs opening a menu throws "not a function" and every test
//    that drives one fails for a reason unrelated to the code under test.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = vi.fn(() => false);
}
if (!Element.prototype.setPointerCapture) {
  Element.prototype.setPointerCapture = vi.fn();
}
if (!Element.prototype.releasePointerCapture) {
  Element.prototype.releasePointerCapture = vi.fn();
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = vi.fn();
}

afterEach(() => {
  cleanup();
});
