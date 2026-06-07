import { createSignal, Show } from "solid-js";
import { A, useNavigate } from "@solidjs/router";

import { forgotPassword } from "@/lib/cognito";

export default function ForgotPassword() {
  const [email, setEmail] = createSignal("");
  const [pending, setPending] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const navigate = useNavigate();

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setPending(true);
    try {
      await forgotPassword(email().trim());
      navigate(`/sign-in/reset?email=${encodeURIComponent(email().trim())}`);
    } catch (err) {
      setError((err as Error).message || "Could not start password reset");
    } finally {
      setPending(false);
    }
  }

  return (
    <main class="mx-auto max-w-md px-6 py-12">
      <h1 class="text-3xl font-bold text-nu-purple">Forgot password</h1>
      <p class="mt-2 text-nu-purple-700">
        Enter your email and we'll send a code you can use to set a new
        password.
      </p>
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
        <button
          type="submit"
          disabled={pending()}
          class="w-full rounded bg-nu-purple px-4 py-2 text-white disabled:opacity-50"
        >
          {pending() ? "Sending…" : "Send code"}
        </button>
        <Show when={error()}>
          <p class="text-sm text-red-700">{error()}</p>
        </Show>
        <p class="text-sm">
          <A href="/sign-in" class="text-nu-purple-700 underline">
            Back to sign-in
          </A>
        </p>
      </form>
    </main>
  );
}
