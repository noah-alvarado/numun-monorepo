import { createSignal, Show } from "solid-js";
import { A, useNavigate } from "@solidjs/router";

import { signUp } from "@/lib/cognito";

export default function SignUp() {
  const [email, setEmail] = createSignal("");
  const [password, setPassword] = createSignal("");
  const [name, setName] = createSignal("");
  const [phone, setPhone] = createSignal("");
  const [pending, setPending] = createSignal(false);
  const [submitted, setSubmitted] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const navigate = useNavigate();

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setPending(true);
    try {
      await signUp({
        email: email().trim(),
        password: password(),
        name: name().trim(),
        phone: phone().trim(),
      });
      setSubmitted(true);
    } catch (err) {
      setError((err as Error).message || "Sign-up failed");
    } finally {
      setPending(false);
    }
  }

  return (
    <main class="mx-auto max-w-md px-6 py-12">
      <h1 class="text-3xl font-bold text-nu-purple">Create your account</h1>

      <Show
        when={!submitted()}
        fallback={
          <div class="mt-6 space-y-4">
            <p class="text-nu-purple-700">
              Check your email for a verification code. Once you have it,
              continue to the verification screen.
            </p>
            <button
              type="button"
              class="rounded bg-nu-purple px-4 py-2 text-white"
              onClick={() =>
                navigate(`/sign-up/verify?email=${encodeURIComponent(email())}`)
              }
            >
              I have my code
            </button>
          </div>
        }
      >
        <form class="mt-6 space-y-4" onSubmit={onSubmit}>
          <label class="block">
            <span class="text-sm">Full name</span>
            <input
              required
              autocomplete="name"
              value={name()}
              onInput={(e) => setName(e.currentTarget.value)}
              class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2"
            />
          </label>
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
            <span class="text-sm">Phone</span>
            <input
              type="tel"
              required
              autocomplete="tel"
              value={phone()}
              onInput={(e) => setPhone(e.currentTarget.value)}
              class="mt-1 block w-full rounded border border-nu-purple-200 px-3 py-2"
            />
          </label>
          <label class="block">
            <span class="text-sm">
              Password{" "}
              <span class="text-xs text-nu-purple-700">
                (12+ characters, lowercase and digits required)
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
            {pending() ? "Submitting…" : "Sign up"}
          </button>
          <Show when={error()}>
            <p class="text-sm text-red-700">{error()}</p>
          </Show>
          <p class="text-sm">
            Already have an account?{" "}
            <A href="/sign-in" class="text-nu-purple-700 underline">
              Sign in
            </A>
          </p>
        </form>
      </Show>
    </main>
  );
}
