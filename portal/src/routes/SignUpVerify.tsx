import { createSignal, Show } from "solid-js";
import { useNavigate, useSearchParams } from "@solidjs/router";

import { cognitoMessage, confirmSignUp, resendSignUpCode } from "@/lib/cognito";

export default function SignUpVerify() {
  const [params] = useSearchParams();
  const [email, setEmail] = createSignal<string>(
    (params.email as string) ?? "",
  );
  const [code, setCode] = createSignal("");
  const [pending, setPending] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [verified, setVerified] = createSignal(false);
  const [resent, setResent] = createSignal(false);
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
      setError(cognitoMessage(err, "Verification failed"));
    } finally {
      setPending(false);
    }
  }

  async function onResend() {
    setError(null);
    setResent(false);
    if (!email().trim()) {
      setError("Enter your email first.");
      return;
    }
    try {
      await resendSignUpCode(email().trim());
      setResent(true);
    } catch (err) {
      setError(cognitoMessage(err, "Could not send a new code"));
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
          <Show when={params.unverified}>
            <p class="rounded border border-amber-300 bg-amber-50 p-2 text-sm text-amber-900">
              Verify your email before signing in. Enter the code we emailed
              you, or send a new one.
            </p>
          </Show>
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
          <Show when={resent()}>
            <p class="text-sm text-green-800">
              We sent a new code to {email().trim()}.
            </p>
          </Show>
          <p class="text-sm">
            Didn't get a code?{" "}
            <button
              type="button"
              onClick={onResend}
              class="text-nu-purple-700 underline"
            >
              Send a new code
            </button>
          </p>
        </form>
      </Show>
    </main>
  );
}
