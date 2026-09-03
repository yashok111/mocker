import type { QueryClient } from "@tanstack/react-query";
import { getGetMeQueryOptions, getGetMeQueryKey } from "@/api/generated/auth/auth.ts";
import type { AuthResponse } from "@/api/generated/schemas";
import { setCsrfToken } from "@/api/client";

// How long a resolved session may be reused before the guard asks again.
// Short enough that a session killed elsewhere cannot survive a navigation by
// more than a few seconds, long enough that hover-preload plus click is one
// request rather than two.
const SESSION_STALE_MS = 5_000;

// session.ts answers exactly one question for the rest of the app: who is
// logged in, if anyone. Route guards call ensureSession in beforeLoad, so the
// answer is settled BEFORE a screen mounts — no screen ever renders a login
// form for a fraction of a second while /api/me is still in flight, which is
// the most irritating bug a tool like this can have.

/**
 * ensureSession resolves the current session, using the query cache when it is
 * already warm. Returns null for "nobody is logged in" — a 401 from /api/me is
 * the normal anonymous answer, not a failure to report, and so is any other
 * settle this could not read as a session (a network error, an unparseable
 * body): the login screen is always the safe fallback, unlike guessing which
 * failure this was.
 *
 * It also (re)arms the CSRF token as a side effect. That token comes back on
 * the same body as the user, and this is the one function every path to a
 * live session goes through — a page load, a route change, and the redirect
 * that follows a successful login all land here, so there is no second place
 * where the token could be forgotten.
 */
export async function ensureSession(queryClient: QueryClient): Promise<AuthResponse | null> {
  try {
    const res = await queryClient.fetchQuery(
      getGetMeQueryOptions({
        query: {
          // A 401 here means "nobody is logged in", not "the server
          // hiccupped" — retrying it only delays the login screen by a
          // wasted round trip.
          retry: false,
          // fetchQuery, NOT ensureQueryData: ensureQueryData is cache-first
          // and returns a cached answer regardless of staleTime, so a
          // session that expired or was logged out in another tab would keep
          // passing this guard — with a stale CSRF token — for as long as the
          // tab stayed open. fetchQuery honours staleTime, which turns the
          // question into "how long may a dead session keep rendering the
          // shell" and answers it with a bound instead of "forever".
          //
          // SESSION_STALE_MS, not 0: the router preloads on intent, so a
          // hover followed by a click would otherwise ask twice for the same
          // answer, and every guarded navigation would cost a round trip.
          staleTime: SESSION_STALE_MS,
        },
      }),
    );
    if (res.status !== 200) {
      setCsrfToken(null);
      return null;
    }
    setCsrfToken(res.data.csrfToken);
    return res.data;
  } catch {
    setCsrfToken(null);
    return null;
  }
}

/**
 * forgetSession drops the cached /api/me answer and the CSRF token. Called on
 * logout and on the global 401 bounce, so the next guarded navigation asks the
 * server again rather than trusting a cache entry the server has already
 * invalidated.
 */
export function forgetSession(queryClient: QueryClient): void {
  setCsrfToken(null);
  queryClient.removeQueries({ queryKey: getGetMeQueryKey() });
}
