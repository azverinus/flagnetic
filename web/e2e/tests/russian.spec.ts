import { createChangeRequest, createGrant } from "@flagnetic/api";
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
import { choose, seedFlag, stringFlag, writeConfiguration } from "../support/flags";
import { runningExperimentInProduction } from "../support/experiments";
import { sdkRequest, seedSdkKey } from "../support/sdk";

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
  let anna: Credentials & { id: string; name: string };
  let project: string;
  test.beforeAll(async () => {
    olga = await seedMember("olga", "Ольга Ладыженская", password);
    anna = await seedMember("anna", "Анна Берёзова", password);
    project = await seedProject("tula");
    must(
      `granting maintainer in ${project}`,
      await createGrant({
        client: managementClient(),
        path: { project },
        body: { member: anna.id, role: "maintainer" },
      }),
    );
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

  test("reads a flag's page in Russian, with its rule, its history and why a viewer cannot edit it", async ({
    page,
  }) => {
    await seedFlag(project, stringFlag("promo_banner", { name: "Промобаннер" }));
    await writeConfiguration(project, "promo_banner", "development", () => ({
      rules: [
        {
          description: "Россия",
          conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "RU" }],
          serve: { kind: "variation", variation: "treatment" },
        },
      ],
    }));
    await signIn(page, olga, russian);
    await page.goto(`/projects/${project}/flags/promo_banner?environment=development`);

    await expect(page.getByRole("heading", { name: "Промобаннер", level: 1 })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "ru");
    const edit = page.getByRole("button", { name: "Изменить определение" });
    await expect(edit).toHaveAttribute("aria-disabled", "true");
    await expect(edit).toHaveAccessibleDescription(/Нужно право изменять флаги/);
    await expect(page.getByRole("combobox", { name: "Вариант без совпадений" })).toBeDisabled();
    await expect(page.getByRole("region", { name: "Правило 1", exact: true })).toBeVisible();
    await expectNoSeriousViolations(page, "the flag's page in Russian");

    await page.getByRole("tab", { name: "История" }).click();
    await expect(page.getByRole("tab", { name: "История" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.getByRole("table", { name: "История флага" })).toContainText(
      "Конфигурация изменена",
    );
    await expectNoSeriousViolations(page, "the flag's history in Russian");
  });

  test("reads an environment's SDK keys and SDKs in Russian, with the reason a viewer cannot create a key", async ({
    page,
  }) => {
    await seedSdkKey(project, "staging", { kind: "server", name: "бэкенд" });
    await seedSdkKey(project, "staging", { kind: "client", name: "витрина" });
    await signIn(page, olga, russian);
    await page.goto(`/projects/${project}/environments/staging`);

    await expect(page.getByRole("heading", { name: "staging", level: 1 })).toBeVisible();
    await expect(page.getByRole("button", { name: "Скачать набор правил" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "SDK-ключи", exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const keys = page.getByRole("table", { name: "SDK-ключи" });
    for (const header of [
      "Название",
      "Вид",
      "Идентификатор ключа",
      "Статус",
      "Разрешённые источники",
    ]) {
      await expect(keys.getByRole("columnheader", { name: header })).toBeVisible();
    }
    await expect(keys.getByRole("row", { name: /бэкенд/ })).toContainText("Серверный");
    await expect(keys.getByRole("row", { name: /витрина/ })).toContainText("Клиентский");
    await expect(keys.getByRole("row", { name: /бэкенд/ })).toContainText("Действует");
    const create = page.getByRole("button", { name: "Новый SDK-ключ" });
    await expect(create).toHaveAttribute("aria-disabled", "true");
    await expect(create).toHaveAccessibleDescription(/Нужно право управлять SDK-ключами/);
    await expectNoSeriousViolations(page, "the SDK keys tab in Russian");

    await page.getByRole("tab", { name: "SDK", exact: true }).click();
    await expect(
      page.getByText("Пока ни один SDK не обращался к этому окружению.", { exact: false }),
    ).toBeVisible();
    await expectNoSeriousViolations(page, "the SDKs tab in Russian");
  });

  test("reads the keys dialogs in Russian", async ({ page }) => {
    await signIn(page, anna, russian);
    await page.goto(`/projects/${project}/environments/staging`);

    await page.getByRole("button", { name: "Новый SDK-ключ" }).click();
    const create = page.getByRole("dialog", { name: "Новый SDK-ключ" });
    await expect(create).toBeVisible();
    await expectNoSeriousViolations(page, "the new SDK key dialog in Russian");
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: /^Ротировать ключ бэкенд/ }).click();
    await expect(page.getByRole("dialog", { name: "Ротировать ключ бэкенд" })).toBeVisible();
    await expectNoSeriousViolations(page, "the rotate dialog in Russian");
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: /^Отозвать ключ витрина/ }).click();
    await expect(page.getByRole("dialog", { name: "Отозвать ключ витрина" })).toBeVisible();
    await expectNoSeriousViolations(page, "the revoke dialog in Russian");
  });

  test("reads the Discovered attributes in Russian, declare and dismiss included", async ({
    page,
  }) => {
    const client = await seedSdkKey(project, "staging", { kind: "client", name: "приложение" });
    const body = (attributes: Record<string, unknown>) => ({
      body: { context: { units: { user: "u-1" }, attributes } },
    });
    await sdkRequest(client, "/evaluate", body({ plan: "pro", level: 3 }));
    await sdkRequest(client, "/evaluate", body({ plan: "free", level: "gold" }));

    await signIn(page, anna, russian);
    await page.goto(`/projects/${project}/attributes`);
    await page.getByRole("tab", { name: "Обнаруженные", exact: true }).click();
    const level = page.getByRole("row", { name: /^level/ });
    await expect(async () => {
      await page.reload();
      await expect(level).toContainText("Строка", { timeout: 1_000 });
    }).toPass({ timeout: 20_000, intervals: [1_000] });
    await expect(level).toContainText("Число, Строка");
    await expectNoSeriousViolations(page, "the Discovered tab in Russian");

    await page.getByRole("button", { name: "Объявить level" }).click();
    await expect(page.getByRole("dialog", { name: "Объявить level" })).toBeVisible();
    await expectNoSeriousViolations(page, "the declare dialog in Russian");
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: "Скрыть plan" }).click();
    await expect(page.getByRole("dialog", { name: "Скрыть plan" })).toBeVisible();
    await expectNoSeriousViolations(page, "the dismiss dialog in Russian");
  });

  test("reads the change requests awaiting a decision and one change request in Russian", async ({
    page,
  }) => {
    await seedFlag(project, stringFlag("checkout_banner"));
    const proposed = must(
      "proposing a change to checkout_banner",
      await createChangeRequest({
        client: managementClient(),
        path: { project },
        body: {
          kind: "flag_configuration",
          flag: "checkout_banner",
          environment: "production",
          title: "Включить баннер",
          proposed: { on: true, off_variation: "control", fallthrough: "treatment" },
          open: true,
        },
      }),
    );

    await signIn(page, anna, russian);
    await page.goto(`/projects/${project}/change-requests?tab=awaiting`);
    await expect(
      page.getByRole("heading", { name: "Запросы на изменение", level: 1 }),
    ).toBeVisible();
    await expect(page.getByRole("tab", { name: "Ждут моего решения" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(
      page
        .getByRole("navigation", { name: "Проект" })
        .getByText("1 запрос на изменение ждёт вашего решения"),
    ).toBeAttached();
    await expectNoSeriousViolations(page, "the change requests in Russian");

    await page.goto(`/projects/${project}/change-requests/${proposed.id}`);
    await expect(page.getByRole("heading", { name: "Включить баннер", level: 1 })).toBeVisible();
    await expect(page.getByText("Открыт", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Отклонить", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "Отклонить запрос на изменение" })).toBeVisible();
    await expectNoSeriousViolations(page, "the reject dialog in Russian");
  });

  test("proposes a change to a protected environment in Russian", async ({ page }) => {
    await seedFlag(project, stringFlag("checkout_promo"));
    await signIn(page, anna, russian);
    await page.goto(`/projects/${project}/flags/checkout_promo?environment=production`);
    await expect(page.getByText(/Окружение защищено: изменения его конфигурации/)).toBeVisible();

    await choose(page, "Вариант без совпадений", "treatment");
    await page.getByRole("button", { name: "Предложить", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Предложить изменение" });
    await expect(dialog.getByLabel("Применить в")).toBeVisible();
    await expectNoSeriousViolations(page, "the propose dialog in Russian");
    await dialog.getByRole("button", { name: "Предложить", exact: true }).click();
    await expect(dialog.getByText("Введите заголовок.")).toBeVisible();
  });
  test("proposes an experiment's stop and reads its change request in Russian", async ({
    page,
  }) => {
    const stopping = await seedProject("tula-stop");
    must(
      `granting maintainer in ${stopping}`,
      await createGrant({
        client: managementClient(),
        path: { project: stopping },
        body: { member: anna.id, role: "maintainer" },
      }),
    );
    await runningExperimentInProduction(stopping);

    await signIn(page, anna, russian);
    await page.goto(`/projects/${stopping}/experiments/cta_copy`);
    await page.getByRole("button", { name: "Остановить", exact: true }).click();
    const stop = page.getByRole("dialog", { name: "Остановить cta_copy" });
    await stop.getByLabel(/Убрать: его правила удаляются/).check();
    await stop.getByRole("button", { name: "Остановить эксперимент" }).click();
    const dialog = page.getByRole("dialog", { name: "Предложить изменение" });
    await expect(dialog.getByText(/защищено, поэтому изменить его напрямую нельзя/)).toBeVisible();
    await expectNoSeriousViolations(page, "the propose dialog of an experiment in Russian");
    await dialog.getByLabel("Заголовок").fill("Остановить эксперимент");
    await dialog.getByRole("button", { name: "Предложить", exact: true }).click();

    await expect(page).toHaveURL(/\/change-requests\/[0-9a-f-]{36}$/);
    await expect(
      page
        .locator("[data-sonner-toast]")
        .filter({ hasText: "Запрос на изменение открыт на проверку." }),
    ).toHaveCSS("opacity", "1");
    await expect(
      page.getByText("Останавливает эксперимент и удаляет правила, которые его отдают."),
    ).toBeVisible();
    await expect(
      page.getByRole("list", { name: "Переписываемые правила" }).getByRole("listitem"),
    ).toHaveText(["Production, правило эксперимента 1: удаляется"]);
    await expectNoSeriousViolations(page, "an experiment change request in Russian");

    await page.goto(`/projects/${stopping}/change-requests`);
    await expect(page.getByRole("combobox", { name: "Расписание", exact: true })).toBeVisible();
    await expectNoSeriousViolations(
      page,
      "the change requests with the schedule filter in Russian",
    );
  });
});
