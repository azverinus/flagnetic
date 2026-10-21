import { signOut } from "@flagnetic/api";
import { Trans } from "@lingui/react/macro";
import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { LogOutIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
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
import { unwrap } from "@/lib/errors";
import { meQuery } from "@/lib/queries";
import { isTheme } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

/** The initials shown for a member: of their name, else of their email. */
export function initialsOf(name: string, email: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  const letters =
    words.length >= 2 ? `${words[0]![0]}${words[words.length - 1]![0]}` : (words[0] ?? email)[0];
  return (letters ?? "?").toUpperCase();
}

/** The signed-in member's menu: who they are, the theme, and signing out. */
export function PersonalMenu() {
  const { data: me } = useSuspenseQuery(meQuery);
  const [theme, setTheme] = useTheme();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const signingOut = useMutation({
    mutationFn: () => unwrap(signOut()),
    onSuccess: () => {
      queryClient.clear();
      void navigate({ to: "/sign-in" });
    },
  });

  const name = me.member?.display_name || me.member?.email || me.credential.token_name || "";
  const email = me.member?.email ?? "";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="min-w-0 gap-2 px-2">
          <span
            aria-hidden
            className="flex size-7 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground"
          >
            {initialsOf(name, email)}
          </span>
          {/* The name is the button's accessible name, read out even where it is not shown. */}
          <span className="sr-only max-w-48 truncate sm:not-sr-only">{name}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="truncate">{name}</span>
          {email && email !== name ? (
            <span className="truncate text-xs font-normal text-muted-foreground">{email}</span>
          ) : null}
        </DropdownMenuLabel>
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
            <Trans>System</Trans>
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled={signingOut.isPending} onSelect={() => signingOut.mutate()}>
          <LogOutIcon aria-hidden />
          <Trans>Sign out</Trans>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
