// AppShell — wraps authenticated routes. Calls UserService.GetMe on mount;
// redirects to /sign-in if the call returns unauthenticated. Per AUTH.md §9.1,
// gating render on the GetMe response prevents the SameSite-Strict flash.
// Render also waits for the active-conference lookup to settle: screens read
// it once on mount, so on a full page load they'd otherwise see "no conference".

import { onMount, Show, type JSX } from "solid-js";
import { useNavigate } from "@solidjs/router";

import { readCookie } from "@/lib/api";
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

  onMount(async () => {
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
      // Best-effort: cache the active conference for downstream screens.
      // Failures here don't block render — the dependent screens surface
      // their own "no active conference" state.
      try {
        await loadActiveConference();
      } catch {
        /* surfaced inline by dependent screens */
      }
    } catch {
      navigate("/sign-in", { replace: true });
    }
  });

  return (
    <Show
      when={user() && conferenceLoaded()}
      fallback={
        <main class="mx-auto max-w-md px-6 py-12 text-nu-purple-700">
          {loading() || user() ? "Loading…" : "Redirecting…"}
        </main>
      }
    >
      {props.children}
    </Show>
  );
}
