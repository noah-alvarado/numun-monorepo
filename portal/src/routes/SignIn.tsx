import { createSignal, Show } from "solid-js";
import { A, useNavigate } from "@solidjs/router";

import { completeNewPasswordChallenge, devBypass, signIn } from "@/lib/cognito";
import { exchange } from "@/lib/session";
import type { CognitoUser } from "amazon-cognito-identity-js";

export default function SignIn() {
  const [email, setEmail] = createSignal("");
  const [password, setPassword] = createSignal("");
  const [remember, setRemember] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [pending, setPending] = createSignal(false);
  const [challengeUser, setChallengeUser] = createSignal<CognitoUser | null>(
    null,
  );
  const [newPassword, setNewPassword] = createSignal("");
  const navigate = useNavigate();

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setPending(true);
    try {
      const result = await signIn(email().trim(), password());
      if (result.kind === "new-password-required") {
        setChallengeUser(result.user);
        return;
      }
      await exchange({ ...result.tokens, rememberMe: remember() });
      navigate("/dashboard", { replace: true });
    } catch (err) {
      setError((err as Error).message || "Sign-in failed");
    } finally {
      setPending(false);
    }
  }

  async function onCompleteChallenge(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setPending(true);
    try {
      const tokens = await completeNewPasswordChallenge(
        challengeUser()!,
        newPassword(),
      );
      await exchange({ ...tokens, rememberMe: remember() });
      navigate("/dashboard", { replace: true });
    } catch (err) {
      setError((err as Error).message || "Failed to set new password");
    } finally {
      setPending(false);
    }
  }

  return (
    <main class="mx-auto max-w-md px-6 py-12">
      <h1 class="text-3xl font-bold text-nu-purple">Sign in</h1>

      <Show
        when={!challengeUser()}
        fallback={
          <form class="mt-6 space-y-4" onSubmit={onCompleteChallenge}>
            <p class="text-sm text-nu-purple-700">
              Set a new password to finish signing in.
            </p>
            <label class="block">
              <span class="text-sm">New password</span>
              <input
                type="password"
                required
                minlength={12}
                value={newPassword()}
                onInput={(e) => setNewPassword(e.currentTarget.value)}
                class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2"
              />
            </label>
            <button
              type="submit"
              disabled={pending()}
              class="w-full rounded bg-nu-purple px-4 py-2 text-white disabled:opacity-50"
            >
              {pending() ? "Saving…" : "Set password and continue"}
            </button>
            <Show when={error()}>
              <p class="text-sm text-red-700">{error()}</p>
            </Show>
          </form>
        }
      >
        <form class="mt-6 space-y-4" onSubmit={onSubmit}>
          <label class="block">
            <span class="text-sm">Email</span>
            <input
              type="email"
              required
              autocomplete="email"
              value={email()}
              onInput={(e) => setEmail(e.currentTarget.value)}
              class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2"
            />
          </label>
          <label class="block">
            <span class="text-sm">Password</span>
            <input
              type={devBypass ? "text" : "password"}
              required={!devBypass}
              autocomplete="current-password"
              value={password()}
              onInput={(e) => setPassword(e.currentTarget.value)}
              class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2"
            />
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={remember()}
              onChange={(e) => setRemember(e.currentTarget.checked)}
            />
            Remember me on this device
          </label>
          <button
            type="submit"
            disabled={pending()}
            class="w-full rounded bg-nu-purple px-4 py-2 text-white disabled:opacity-50"
          >
            {pending() ? "Signing in…" : "Sign in"}
          </button>
          <Show when={error()}>
            <p class="text-sm text-red-700">{error()}</p>
          </Show>
          <div class="flex justify-between text-sm">
            <A href="/sign-in/forgot" class="text-nu-purple-700 underline">
              Forgot password?
            </A>
            <A href="/sign-up" class="text-nu-purple-700 underline">
              Create account
            </A>
          </div>
          <Show when={devBypass}>
            <p class="rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900">
              Dev-bypass mode. Use a seed user UUID as <code>email</code> (the
              password field is ignored); see <code>/docs/seed-users.md</code>.
            </p>
          </Show>
        </form>
      </Show>
    </main>
  );
}
