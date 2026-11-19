import { beforeEach, describe, expect, it } from "vitest";

import { draftKey, forgetImportDrafts, forgetImportDraftsOfOthers } from "./import-drafts";
import { loadFirebaseSession, saveFirebaseSession } from "./firebase-import";
import { loadImportSession, saveImportSession } from "./project-import";

const document = {
  name: "checkout.yaml",
  mediaType: "application/yaml",
  text: "schema_version: 1\n",
};
const template = JSON.stringify({ parameters: {} });

describe("import drafts belong to the member who made them", () => {
  beforeEach(() => window.sessionStorage.clear());

  it("never shows the second member the file the first one uploaded", () => {
    saveImportSession("ada", "checkout", { step: "review", document });
    saveFirebaseSession("ada", "checkout", {
      step: "parameters",
      fileName: "t.json",
      text: template,
    });

    expect(loadImportSession("grace", "checkout")).toEqual({ step: "upload" });
    expect(loadFirebaseSession("grace", "checkout")).toEqual({ step: "upload" });
    // Not merely hidden: the first member's file is gone from the tab.
    expect(window.sessionStorage.length).toBe(0);
  });

  it("restores a member's own draft after a reload", () => {
    saveImportSession("ada", "checkout", { step: "review", document });
    saveFirebaseSession("ada", "checkout", {
      step: "parameters",
      fileName: "t.json",
      text: template,
    });

    expect(loadImportSession("ada", "checkout")).toEqual({ step: "review", document });
    expect(loadFirebaseSession("ada", "checkout").text).toBe(template);
  });

  it("forgets every draft of every member when the session ends", () => {
    saveImportSession("ada", "checkout", { step: "review", document });
    saveFirebaseSession("grace", "mobile", {
      step: "parameters",
      fileName: "t.json",
      text: template,
    });
    window.sessionStorage.setItem("unrelated", "kept");

    forgetImportDrafts();

    expect(window.sessionStorage.length).toBe(1);
    expect(window.sessionStorage.getItem("unrelated")).toBe("kept");
  });

  it("forgets the drafts of the others and keeps the member's own", () => {
    saveImportSession("ada", "checkout", { step: "review", document });
    saveImportSession("grace", "checkout", { step: "review", document });

    forgetImportDraftsOfOthers("grace");

    expect(window.sessionStorage.getItem(draftKey("project", "ada", "checkout"))).toBeNull();
    expect(window.sessionStorage.getItem(draftKey("project", "grace", "checkout"))).not.toBeNull();
  });

  it("keys the two wizards apart", () => {
    expect(draftKey("project", "ada", "shop")).not.toBe(draftKey("firebase", "ada", "shop"));
  });
});
