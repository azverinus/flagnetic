import { Trans } from "@lingui/react/macro";
import { Page } from "@/components/ui/page";
import { Link } from "@tanstack/react-router";

import { Button } from "@/components/ui/button";

/** An address the console has no page for, rendered inside the shell. */
export function NotFound() {
  return (
    <Page measure="form">
      <h1 className="text-2xl/7 font-semibold">
        <Trans>Page not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>There is nothing at this address. The link may be old or mistyped.</Trans>
      </p>
      <Button className="self-start" asChild variant="outline">
        <Link to="/">
          <Trans>Go to the start page</Trans>
        </Link>
      </Button>
    </Page>
  );
}
