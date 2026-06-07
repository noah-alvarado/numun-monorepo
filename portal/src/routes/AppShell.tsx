// AppShell — wraps authenticated routes. Calls UserService.GetMe on mount;
// redirects to /sign-in if the call returns unauthenticated. Per AUTH.md §9.1,
// gating render on the GetMe response prevents the SameSite-Strict flash.

import { onMount, Show, type JSX } from "solid-js";
import { useNavigate } from "@solidjs/router";

import { loadCurrentUser, loadingSignal, userSignal } from "@/lib/session";

export default function AppShell(props: { children?: JSX.Element }) {
  const [user] = userSignal;
  const [loading] = loadingSignal;
  const navigate = useNavigate();

  onMount(async () => {
    try {
      const u = await loadCurrentUser();
      if (!u) {
        navigate("/sign-in", { replace: true });
      }
    } catch {
      navigate("/sign-in", { replace: true });
    }
  });

  return (
    <Show
      when={user()}
      fallback={
        <main class="mx-auto max-w-md px-6 py-12 text-nu-purple-700">
          {loading() ? "Loading…" : "Redirecting…"}
        </main>
      }
    >
      {props.children}
    </Show>
  );
}
