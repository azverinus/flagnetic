import type { MessageDescriptor } from "@lingui/core";
import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";

import type { projectNameCrumb } from "@/components/breadcrumb";
import { Toaster } from "@/components/toaster";

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /**
     * What the breadcrumb calls the route, in the member's language, or
     * `projectNameCrumb` for the route of one project, which the
     * breadcrumb calls by the project's name.
     */
    crumb?: MessageDescriptor | typeof projectNameCrumb;
  }
}

declare module "@tanstack/react-query" {
  interface Register {
    mutationMeta: {
      /** False for a mutation that explains its own refusal on the page. */
      toast?: boolean;
      /**
       * False for signing in and out: they change the session, not what a
       * log shows, and once signed out every read of a log is answered 401.
       */
      readsAuditAgain?: boolean;
    };
  }
}

export type RouterContext = { queryClient: QueryClient };

export const Route = createRootRouteWithContext<RouterContext>()({
  component: Root,
});

function Root() {
  return (
    <>
      <Outlet />
      <Toaster />
    </>
  );
}
