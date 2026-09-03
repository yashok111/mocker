import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { type } from "arktype";
import { LoginPage } from "@/components/LoginPage";
import { ensureSession } from "@/auth/session";

// The path to return to after a successful login. Held in the URL rather than
// in memory so it survives the reload a browser's password manager sometimes
// triggers, and so the redirect a guard threw is visible in the address bar
// instead of being invisible state.
const loginSearch = type({
  "redirect?": "string",
});

export const Route = createFileRoute("/login")({
  validateSearch: loginSearch.assert,
  beforeLoad: async ({ context }) => {
    // Already logged in: this screen has nothing to offer, and leaving it
    // reachable is how a person ends up staring at a login form that would
    // just log them in as themselves again.
    if (await ensureSession(context.queryClient)) {
      throw redirect({ to: "/" });
    }
  },
  component: LoginRoute,
});

function LoginRoute() {
  const navigate = useNavigate();
  const search = Route.useSearch();

  return (
    <LoginPage
      onSuccess={() => {
        // replace, not push: the login screen must not sit in history behind
        // the app, or "back" lands on a form the guard immediately bounces.
        void navigate({ to: search.redirect ?? "/", replace: true });
      }}
    />
  );
}
