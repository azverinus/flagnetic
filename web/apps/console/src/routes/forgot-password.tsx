import { createFileRoute } from "@tanstack/react-router";

import { PasswordResetRequest } from "@/components/password-reset-request";
import { SignedOutCard } from "@/components/signed-out-card";

/**
 * The page the sign-in page's forgotten-password link opens: a member asks
 * for a password reset by mail.
 */
export const Route = createFileRoute("/forgot-password")({
  component: ForgotPasswordPage,
});

function ForgotPasswordPage() {
  return (
    <SignedOutCard className="max-w-sm">
      <PasswordResetRequest />
    </SignedOutCard>
  );
}
