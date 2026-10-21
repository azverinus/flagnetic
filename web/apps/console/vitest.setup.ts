import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

import { activateForTests } from "./src/test/render";

// jsdom has no layout, so it lacks the element methods Radix Select calls
// when it opens: scrolling the chosen option into view and asking about
// pointer capture. Stubs that scroll nothing and report no capture let it
// open as it does in a browser where no pointer is captured.
Element.prototype.scrollIntoView ??= function scrollIntoView() {};
Element.prototype.hasPointerCapture ??= function hasPointerCapture() {
  return false;
};
Element.prototype.releasePointerCapture ??= function releasePointerCapture() {};

beforeEach(() => {
  activateForTests("en");
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});
