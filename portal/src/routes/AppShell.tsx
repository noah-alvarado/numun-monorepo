// AppShell — wraps authenticated routes. Calls UserService.GetMe on mount;
// redirects to /sign-in if the call returns unauthenticated. Per AUTH.md §9.1,
// gating render on the GetMe response prevents the SameSite-Strict flash.
// Render also waits for the active-conference lookup to succeed: screens read
// it once on mount, so on a full page load they'd otherwise see "no
// conference". If either lookup fails for a reason other than being signed
// out (rate limited, API down), the shell shows the error and a Retry.

import { createSignal, onMount, Show, type JSX } from "solid-js";
import { Code, ConnectError } from "@connectrpc/connect";
import { useNavigate } from "@solidjs/router";

import { readCookie } from "@/lib/api";
import { connectMessage } from "@/lib/errors";
import {
  activeConferenceLoadedSignal,
  loadActiveConference,
  loadCurrentUser,
  loadingSignal,
  userSignal,
} from "@/lib/session";

export default function AppShell(props: { children?: JSX.Element }) {
  const [user] = userSignal;
  const [loading] = loadingSignal;
  const [conferenceLoaded] = activeConferenceLoadedSignal;
  const navigate = useNavigate();
  // Set when GetMe fails for a reason other than being signed out (rate
  // limited, API down); the user stays put and can retry.
  const [failure, setFailure] = createSignal<string | null>(null);

  async function load() {
    setFailure(null);
    // The CSRF cookie lives exactly as long as the session; if it's present
    // but GetMe is refused, the session ended (expired or revoked) rather
    // than never existing.
    const hadSession = !!readCookie("csrf_token");
    try {
      const u = await loadCurrentUser();
      if (!u) {
        navigate(hadSession ? "/sign-in?ended=1" : "/sign-in", {
          replace: true,
        });
        return;
      }
      // Screens read the active conference once on mount and treat "none"
      // as "no conference is running", so a failed lookup must not render
      // them; show the error and a retry instead.
      await loadActiveConference();
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
        navigate("/sign-in", { replace: true });
        return;
      }
      setFailure(
        connectMessage(err) ?? "Couldn't reach NUMUN. Try again in a moment.",
      );
    }
  }

  onMount(load);

  return (
    <Show
      when={user() && conferenceLoaded()}
      fallback={
        <main class="mx-auto max-w-md px-6 py-12 text-nu-purple-700">
          <Show
            when={failure()}
            fallback={loading() || user() ? "Loading…" : "Redirecting…"}
          >
            {(f) => (
              <div class="space-y-3">
                <p class="rounded bg-red-50 p-3 text-sm text-red-900">{f()}</p>
                <button
                  type="button"
                  onClick={load}
                  class="rounded border border-nu-purple-300 px-3 py-1 text-sm"
                >
                  Retry
                </button>
              </div>
            )}
          </Show>
        </main>
      }
    >
      {props.children}
    </Show>
  );
}
