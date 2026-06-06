import { createResource, Show } from "solid-js";
import { healthClient } from "@/lib/api";

export default function App() {
  const [health] = createResource(() => healthClient.check({}));

  return (
    <main class="mx-auto max-w-2xl px-6 py-16 font-sans">
      <h1 class="text-4xl font-bold text-nu-purple">NUMUN Portal</h1>
      <p class="mt-2 text-nu-purple-700">
        Foundation milestone (M0). Health check against the local API:
      </p>
      <Show
        when={health()}
        fallback={
          <p class="mt-6 rounded border border-nu-purple-200 bg-white p-4 text-nu-purple-700">
            {health.loading ? "Loading…" : (health.error?.message ?? "")}
          </p>
        }
      >
        {(h) => (
          <pre class="mt-6 overflow-x-auto rounded border border-nu-purple-200 bg-white p-4 text-sm">
            {JSON.stringify(h(), null, 2)}
          </pre>
        )}
      </Show>
    </main>
  );
}
