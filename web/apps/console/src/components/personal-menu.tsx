import { type Me, signOut, updateMe } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ChevronsUpDownIcon, LogOutIcon, SettingsIcon } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { languageName } from "@/i18n/format";
import { locales } from "@/i18n/locale";
import { cn } from "@/lib/cn";
import { unwrap } from "@/lib/errors";
import { chosenLocale, followBrowser, followMemberLocale, localeChoice } from "@/lib/member-locale";
import { meQuery } from "@/lib/queries";
import { announceSessionChange } from "@/lib/signed-in";
import { forgetRecentContexts } from "@/lib/tester";
import { isTheme } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

/** The initials shown for a member: of their name, else of their email. */
export function initialsOf(name: string, email: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  const letters =
    words.length >= 2 ? `${words[0]![0]}${words[words.length - 1]![0]}` : (words[0] ?? email)[0];
  return (letters ?? "?").toUpperCase();
}

/**
 * The signed-in member's menu, the account row at the sidebar's foot: who they are, their settings, the theme,
 * the language, and signing out.
 */
export function PersonalMenu({ collapsed = false }: { collapsed?: boolean }) {
  const { i18n, t } = useLingui();
  const { data: me } = useSuspenseQuery(meQuery);
  const [theme, setTheme] = useTheme();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  // The language is kept with the member, so it follows them to every
  // device; a refusal is toasted, since the menu has closed by then.
  const choosingLocale = useMutation({
    mutationFn: (locale: string | null) => unwrap(updateMe({ body: { locale } })),
    onSuccess: async (saved: Me) => {
      queryClient.setQueryData(meQuery.queryKey, saved);
      await followMemberLocale(i18n, saved);
    },
  });
  const signingOut = useMutation({
    mutationFn: () => unwrap(signOut()),
    meta: { readsAuditAgain: false },
    onSuccess: () => {
      queryClient.clear();
      forgetRecentContexts();
      announceSessionChange("signed-out");
      void navigate({ to: "/sign-in" });
    },
  });

  const name = me.member?.display_name || me.member?.email || me.credential.token_name || "";
  const email = me.member?.email ?? "";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className={cn(
            "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left text-sm transition-colors hover:bg-foreground/[0.06]",
            collapsed && "justify-center px-0",
          )}
        >
          <span
            aria-hidden
            className="flex size-7 shrink-0 items-center justify-center rounded-full border bg-raised text-xs font-medium text-foreground-secondary"
          >
            {initialsOf(name, email)}
          </span>
          {/* The name is the button's accessible name, read out even in the rail. */}
          <span className={cn("min-w-0 flex-1 truncate font-medium", collapsed && "sr-only")}>
            {name}
          </span>
          {collapsed ? null : (
            <ChevronsUpDownIcon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          )}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-64">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="truncate">{name}</span>
          {email && email !== name ? (
            <span className="truncate text-xs font-normal text-muted-foreground">{email}</span>
          ) : null}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/settings/profile">
            <SettingsIcon aria-hidden />
            <Trans>Personal settings</Trans>
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
          <Trans>Theme</Trans>
        </DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={theme}
          onValueChange={(value) => {
            if (isTheme(value)) setTheme(value);
          }}
        >
          <DropdownMenuRadioItem value="light">
            <Trans>Light</Trans>
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">
            <Trans>Dark</Trans>
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="system">
            <Trans context="theme">System</Trans>
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        {me.member ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
              <Trans>Language</Trans>
            </DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={localeChoice(me.member.locale)}
              onValueChange={(value) => {
                if (!choosingLocale.isPending) choosingLocale.mutate(chosenLocale(value));
              }}
            >
              <DropdownMenuRadioItem
                value={followBrowser}
              >{t`Follow the browser`}</DropdownMenuRadioItem>
              {locales.map((locale) => (
                <DropdownMenuRadioItem key={locale} value={locale} lang={locale}>
                  {languageName(locale)}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </>
        ) : null}
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled={signingOut.isPending} onSelect={() => signingOut.mutate()}>
          <LogOutIcon aria-hidden />
          <Trans>Sign out</Trans>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
