import { createGrant } from "@flagnetic/api";
import { expect, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  seedMember,
  seedProject,
  signIn,
  type SignInWords,
  submitSignIn,
} from "../support/console";

/**
 * A member who never chose a language, in a browser that asks for Russian:
 * the console follows the browser from the sign-in page on, and every
 * string, server refusal and list it builds reads in Russian.
 */
test.use({ locale: "ru-RU" });

const password = "harness-birch-samovar-4417";

/** The sign-in page as it reads in Russian. */
const russian: SignInWords = { email: "Электронная почта", password: "Пароль", submit: "Войти" };

test.describe("the console in Russian", () => {
  test.describe.configure({ mode: "serial" });

  let olga: Credentials & { id: string; name: string };
  let project: string;
  test.beforeAll(async () => {
    olga = await seedMember("olga", "Ольга Ладыженская", password);
    project = await seedProject("tula");
    must(
      `granting viewer in ${project}`,
      await createGrant({
        client: managementClient(),
        path: { project },
        body: { member: olga.id, role: "viewer" },
      }),
    );
  });

  test("signs in, refusing a wrong password in Russian first", async ({ page }) => {
    await page.goto("/sign-in");
    await expect(page.locator("html")).toHaveAttribute("lang", "ru");
    await expect(page.getByRole("heading", { name: "Войти", level: 1 })).toBeVisible();
    await expectNoSeriousViolations(page, "the sign-in page in Russian");

    await submitSignIn(page, { email: olga.email, password: "not-the-password-at-all" }, russian);
    await expect(page.getByRole("alert")).toHaveText(
      "Неверный адрес электронной почты или пароль.",
    );
    await expect(page).toHaveURL(/\/sign-in/);

    await signIn(page, olga, russian);
    await expect(page.locator("html")).toHaveAttribute("lang", "ru");
  });

  test("reads the shell in Russian, with the reason a viewer's action is disabled", async ({
    page,
  }) => {
    await signIn(page, olga, russian);
    await page.goto(`/projects/${project}/environments`);

    await expect(page.getByRole("heading", { name: "Окружения", level: 1 })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "ru");
    await expect(
      page.getByRole("navigation", { name: "Проект" }).getByRole("link", { name: "Окружения" }),
    ).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Навигационная цепочка" })).toContainText(
      "Окружения",
    );
    await expect(page.getByRole("button", { name: olga.name, exact: true })).toBeVisible();

    // The roles holding the permission are joined as Russian joins a list.
    const create = page.getByRole("button", { name: "Новое окружение" });
    await expect(create).toHaveAttribute("aria-disabled", "true");
    await expect(create).toHaveAccessibleDescription(
      "Нужно право управлять окружениями, оно есть у ролей Владелец и Сопровождающий.",
    );
    await expectNoSeriousViolations(page, "the project shell in Russian");
  });

  test("saves a form and translates the server's refusal of another", async ({ page }) => {
    await signIn(page, olga, russian);
    await page.goto("/settings/profile");
    await expect(page.getByRole("heading", { name: "Личные настройки", level: 1 })).toBeVisible();

    await page.getByLabel("Отображаемое имя").fill("Ольга Александровна");
    await page.getByRole("button", { name: "Сохранить" }).click();
    // The toast is checked with the page once it has faded in: halfway, its
    // text is paler than it will be.
    const saved = page.locator("[data-sonner-toast]").filter({ hasText: "Профиль сохранён." });
    await expect(saved).toHaveCSS("opacity", "1");
    await expect(
      page.getByRole("button", { name: "Ольга Александровна", exact: true }),
    ).toBeVisible();

    // The server refuses by code and points at the field; the console
    // writes the message in Russian on that field.
    const current = page.getByLabel("Текущий пароль");
    await current.fill("not-the-password-at-all");
    await page.getByLabel("Новый пароль", { exact: true }).fill(`${password}-new`);
    await page.getByLabel("Повторите новый пароль").fill(`${password}-new`);
    await page.getByRole("button", { name: "Сменить пароль" }).click();
    await expect(page.getByText("Это не ваш текущий пароль.")).toBeVisible();
    await expect(current).toHaveAttribute("aria-invalid", "true");
    await expectNoSeriousViolations(page, "the profile with a refusal in Russian");
  });
});
