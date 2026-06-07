import { createSignal, Show } from "solid-js";
import { A, useNavigate, useSearchParams } from "@solidjs/router";

import { confirmSignUp } from "@/lib/cognito";

export default function SignUpVerify() {
  const [params] = useSearchParams();
  const [email, setEmail] = createSignal<string>(
    (params.email as string) ?? "",
  );
  const [code, setCode] = createSignal("");
  const [pending, setPending] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [verified, setVerified] = createSignal(false);
  const navigate = useNavigate();

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setPending(true);
    try {
      await confirmSignUp(email().trim(), code().trim());
      setVerified(true);
      setTimeout(() => navigate("/sign-in"), 1200);
    } catch (err) {
      setError((err as Error).message || "Verification failed");
    } finally {
      setPending(false);
    }
  }

  return (
    <main class="mx-auto max-w-md px-6 py-12">
      <h1 class="text-3xl font-bold text-nu-purple">Verify your email</h1>

      <Show
        when={!verified()}
        fallback={
          <p class="mt-6 text-nu-purple-700">
            Verified. Redirecting to sign-in…
          </p>
        }
      >
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
            <span class="text-sm">Verification code</span>
            <input
              required
              inputmode="numeric"
              pattern="[0-9]{6}"
              value={code()}
              onInput={(e) => setCode(e.currentTarget.value)}
              class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2 font-mono"
            />
          </label>
          <button
            type="submit"
            disabled={pending()}
            class="w-full rounded bg-nu-purple px-4 py-2 text-white disabled:opacity-50"
          >
            {pending() ? "Verifying…" : "Verify"}
          </button>
          <Show when={error()}>
            <p class="text-sm text-red-700">{error()}</p>
          </Show>
          <p class="text-sm">
            Didn't get a code?{" "}
            <A href="/sign-up" class="text-nu-purple-700 underline">
              Start over
            </A>
          </p>
        </form>
      </Show>
    </main>
  );
}
