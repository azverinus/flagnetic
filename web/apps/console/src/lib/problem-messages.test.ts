import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { ApiError } from "./errors";
import { problemMessage, violationMessage, violationsByPointer } from "./problem-messages";

function refusal(status: number, problem: Record<string, unknown>) {
  return new ApiError(status, { request_id: "req-1", ...problem });
}

describe("a refusal in the member's language", () => {
  it("is written from its code, never from the server's English detail", () => {
    const error = refusal(401, { code: "invalid_credentials", detail: "wrong email or password" });

    expect(problemMessage(i18n, error)).toBe("The email or the password is not right.");
  });

  it("takes the wait from the problem's extension member", () => {
    expect(problemMessage(i18n, refusal(429, { code: "locked", retry_after: 90 }))).toBe(
      "Too many failed attempts for this email. Try again in 2 minutes.",
    );
  });

  it("says too many requests for any request refused as rate limited, not only a sign-in", () => {
    // Every mutation's toast reads this table; the sign-in form says more.
    expect(problemMessage(i18n, refusal(429, { code: "rate_limited", retry_after: 30 }))).toBe(
      "Too many requests. Try again in 30 seconds.",
    );
    expect(problemMessage(i18n, refusal(429, { code: "rate_limited" }))).toBe(
      "Too many requests. Try again later.",
    );
  });

  it("explains the refusals of deleting or changing an environment or a declaration", () => {
    expect(problemMessage(i18n, refusal(409, { code: "last_environment" }))).toBe(
      "This is the project's last environment. A project always keeps one to serve from.",
    );
    expect(problemMessage(i18n, refusal(409, { code: "standard_attribute" }))).toBe(
      "Standard attributes are part of every project, so they cannot be deleted.",
    );
    expect(problemMessage(i18n, refusal(422, { code: "type_immutable" }))).toBe(
      "An attribute's type is fixed once it is declared.",
    );
  });

  it("names the missing permission by its display name", () => {
    expect(
      problemMessage(i18n, refusal(403, { code: "forbidden", permission: "environment.manage" })),
    ).toBe("You need the permission to manage environments.");
  });

  it("shows the server's detail only for a code it does not know", () => {
    expect(
      problemMessage(i18n, refusal(409, { code: "brand_new", detail: "Something new." })),
    ).toBe("Something new.");
  });

  it("is translated, numbers and waits included, when the console reads Russian", () => {
    activateForTests("ru");

    expect(problemMessage(i18n, refusal(429, { code: "locked", retry_after: 300 }))).toBe(
      "Слишком много неудачных попыток для этого адреса. Попробуйте ещё раз через 5 минут.",
    );
    expect(
      violationMessage(i18n, {
        pointer: "/name",
        code: "too_long",
        detail: "at most 200",
        params: { max: 1200 },
      }),
    ).toBe("Не более 1 200 символов.");
  });
});

describe("a field violation", () => {
  it("is explained from its code and params", () => {
    expect(
      violationMessage(i18n, {
        pointer: "/password",
        code: "weak_password",
        detail: "too short",
        params: { rule: "length", min: 12, max: 128 },
      }),
    ).toBe("Use 12 to 128 characters.");
    expect(
      violationMessage(i18n, {
        pointer: "/password",
        code: "weak_password",
        detail: "common",
        params: { rule: "common" },
      }),
    ).toBe("This password is too common. Choose another.");
  });

  it("falls back to the server's detail for a code it does not know", () => {
    expect(violationMessage(i18n, { pointer: "/x", code: "novel", detail: "Novel rule." })).toBe(
      "Novel rule.",
    );
  });

  it("is found by the JSON Pointer of the field it concerns", () => {
    const error = refusal(422, {
      code: "invalid_project",
      errors: [
        { pointer: "/key", code: "invalid_key", detail: "bad key" },
        { pointer: "/name", code: "invalid_name", detail: "empty" },
      ],
    });

    const byPointer = violationsByPointer(i18n, error);

    expect(byPointer.get("/name")).toBe("Enter a name.");
    expect(byPointer.get("/key")).toMatch(/^Start with a lowercase letter/);
    expect(problemMessage(i18n, error)).toBe("Some fields need correcting.");
  });
});
