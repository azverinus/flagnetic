import type { MessageDescriptor } from "@lingui/core";
import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";

import { Toaster } from "@/components/toaster";

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /** What the breadcrumb calls the route, in the member's language. */
    crumb?: MessageDescriptor;
  }
}

declare module "@tanstack/react-query" {
  interface Register {
    mutationMeta: {
      /** False for a mutation that explains its own refusal on the page. */
      toast?: boolean;
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
