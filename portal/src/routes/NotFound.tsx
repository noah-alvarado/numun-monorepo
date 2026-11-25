// NotFound — catch-all for URLs no route matches.

import { A } from "@solidjs/router";

export default function NotFound() {
  return (
    <main class="mx-auto max-w-2xl px-6 py-12">
      <h1 class="text-3xl font-bold text-nu-purple">Page not found</h1>
      <p class="mt-4 text-sm text-nu-purple-700">
        There's nothing at this address.
      </p>
      <A
        href="/dashboard"
        class="mt-6 inline-block rounded bg-nu-purple px-3 py-1 text-sm text-white"
      >
        Go to dashboard
      </A>
    </main>
  );
}
