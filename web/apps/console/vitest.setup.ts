import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

import { activateForTests } from "./src/test/render";

beforeEach(() => {
  activateForTests("en");
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});
