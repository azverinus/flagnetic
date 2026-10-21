import { Trans } from "@lingui/react/macro";
import { Link } from "@tanstack/react-router";

import { Button } from "@/components/ui/button";

/** An address the console has no page for, rendered inside the shell. */
export function NotFound() {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Page not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>There is nothing at this address. The link may be old or mistyped.</Trans>
      </p>
      <Button asChild variant="outline">
        <Link to="/">
          <Trans>Go to the start page</Trans>
        </Link>
      </Button>
    </div>
  );
}
