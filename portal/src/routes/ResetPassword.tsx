import { createSignal, Show } from "solid-js";
import { A, useNavigate, useSearchParams } from "@solidjs/router";

import { authClient } from "@/lib/api";
import { confirmForgotPassword } from "@/lib/cognito";

export default function ResetPassword() {
  const [params] = useSearchParams();
  const [email, setEmail] = createSignal<string>(
    (params.email as string) ?? "",
  );
  const [code, setCode] = createSignal("");
  const [password, setPassword] = createSignal("");
  const [pending, setPending] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const navigate = useNavigate();

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setPending(true);
    try {
      await confirmForgotPassword(email().trim(), code().trim(), password());
      // AUTH.md §10.1 + plan ambiguity #9: portal records the audit event by
      // calling AuthService.RecordPasswordResetCompleted after a successful
      // Cognito ConfirmForgotPassword.
      try {
        await authClient.recordPasswordResetCompleted({
          email: email().trim(),
        });
      } catch {
        /* best-effort */
      }
      navigate("/sign-in");
    } catch (err) {
      setError((err as Error).message || "Could not reset password");
    } finally {
      setPending(false);
    }
  }

  return (
    <main class="mx-auto max-w-md px-6 py-12">
      <h1 class="text-3xl font-bold text-nu-purple">Set a new password</h1>
      <form class="mt-6 space-y-4" onSubmit={onSubmit}>
        <label class="block">
          <span class="text-sm">Email</span>
          <input
            type="email"
            required
            value={email()}
            onInput={(e) => setEmail(e.currentTarget.value)}
            class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2"
          />
        </label>
        <label class="block">
          <span class="text-sm">Code from email</span>
          <input
            required
            inputmode="numeric"
            pattern="[0-9]{6}"
            value={code()}
            onInput={(e) => setCode(e.currentTarget.value)}
            class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2 font-mono"
          />
        </label>
        <label class="block">
          <span class="text-sm">
            New password{" "}
            <span class="text-xs text-nu-purple-700">
              (12+ characters, lowercase and digits)
            </span>
          </span>
          <input
            type="password"
            required
            minlength={12}
            autocomplete="new-password"
            value={password()}
            onInput={(e) => setPassword(e.currentTarget.value)}
            class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2"
          />
        </label>
        <button
          type="submit"
          disabled={pending()}
          class="w-full rounded bg-nu-purple px-4 py-2 text-white disabled:opacity-50"
        >
          {pending() ? "Updating…" : "Reset password"}
        </button>
        <Show when={error()}>
          <p class="text-sm text-red-700">{error()}</p>
        </Show>
        <p class="text-sm">
          <A href="/sign-in/forgot" class="text-nu-purple-700 underline">
            Get a new code
          </A>
        </p>
      </form>
    </main>
  );
}
