// Bulk import result — success or partial-failure resume banner. See
// BULK_IMPORT.md §6.4.

import { createSignal, Show } from "solid-js";
import { useLocation, useNavigate, useParams } from "@solidjs/router";

import { delegateClient } from "@/lib/api";
import type { CommitSummary } from "@/gen/numun/v1/delegates_pb";
import { connectMessage } from "@/lib/errors";

type ResultState = {
  summary?: CommitSummary;
  delegationId: string;
};

export default function DelegateImportResult() {
  const params = useParams<{ delegationId: string }>();
  const location = useLocation<ResultState>();
  const navigate = useNavigate();

  const initial = location.state?.summary;
  const [summary, setSummary] = createSignal<CommitSummary | undefined>(
    initial,
  );
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  function isPartial(s: CommitSummary | undefined): boolean {
    if (!s) return false;
    return s.status === "failed" && !!s.bulkImportJobId;
  }

  async function onResume() {
    const s = summary();
    if (!s?.bulkImportJobId) return;
    setBusy(true);
    setError(null);
    try {
      const resp = await delegateClient.resumeBulkImport({
        bulkImportJobId: s.bulkImportJobId,
      });
      // Resume reports only the job's progress; the counts are the commit's.
      const job = resp.summary;
      if (job) {
        setSummary({
          ...s,
          status: job.status,
          totalBatches: job.totalBatches,
          completedBatches: job.completedBatches,
          lastError: job.lastError,
        });
      }
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to resume import.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="mx-auto max-w-3xl px-6 py-8">
      <header>
        <h1 class="text-2xl font-bold text-nu-purple">Import result</h1>
      </header>

      <Show
        when={summary()}
        fallback={
          <>
            <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
              No result to show. Start an import first.
            </p>
            <button
              type="button"
              onClick={() =>
                navigate(`/delegations/${params.delegationId}/delegates/import`)
              }
              class="mt-4 rounded bg-nu-purple px-3 py-1 text-sm text-white"
            >
              Go to import
            </button>
          </>
        }
      >
        {(s) => (
          <Show
            when={isPartial(s())}
            fallback={
              <SuccessBlock summary={s()} delegationId={params.delegationId} />
            }
          >
            <section class="mt-6 rounded border border-nu-yellow-dark bg-nu-yellow/20 p-4 text-sm">
              <div class="font-semibold text-nu-black-800">
                Import partially failed
              </div>
              <p class="mt-1 text-nu-black-800">
                Large imports are saved in parts, and part{" "}
                {s().completedBatches + 1} of {s().totalBatches} couldn't be
                saved. The parts before it were. Resume saves the rest within 30
                minutes of the preview; after that, upload the file again.
              </p>
              <div class="mt-3 flex gap-2">
                <button
                  type="button"
                  disabled={busy()}
                  onClick={onResume}
                  class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
                >
                  {busy() ? "Resuming…" : "Resume"}
                </button>
                <button
                  type="button"
                  onClick={() =>
                    navigate(
                      `/delegations/${params.delegationId}?tab=delegates`,
                    )
                  }
                  class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
                >
                  View roster
                </button>
              </div>
              <Show when={error()}>
                {(e) => (
                  <p class="mt-3 rounded bg-red-50 p-2 text-red-900">{e()}</p>
                )}
              </Show>
            </section>
          </Show>
        )}
      </Show>
    </main>
  );
}

function SuccessBlock(props: { summary: CommitSummary; delegationId: string }) {
  const navigate = useNavigate();
  // Soft-deletes and unchanged rows are reported separately; they weren't
  // imported.
  const total = props.summary.createCount + props.summary.updateCount;
  return (
    <section class="mt-6 rounded border border-nu-green-dark/40 bg-nu-green/10 p-4 text-sm">
      <div class="flex items-start gap-3">
        <span
          aria-hidden="true"
          class="mt-0.5 inline-flex h-6 w-6 items-center justify-center rounded-full bg-nu-green-dark text-white"
        >
          ✓
        </span>
        <div>
          <div class="font-semibold text-nu-black-800">
            {total === 0 && props.summary.softDeleteCount === 0
              ? "No changes — the roster already matches the file"
              : `Imported ${total} delegate${total === 1 ? "" : "s"}`}
          </div>
          <p class="mt-1 text-nu-black-700">
            {props.summary.createCount} created · {props.summary.updateCount}{" "}
            updated · {props.summary.unchangedCount} unchanged ·{" "}
            {props.summary.softDeleteCount} soft-deleted
          </p>
          <div class="mt-3">
            <button
              type="button"
              onClick={() =>
                navigate(`/delegations/${props.delegationId}?tab=delegates`)
              }
              class="rounded bg-nu-purple px-3 py-1 text-white"
            >
              View roster
            </button>
          </div>
        </div>
      </div>
    </section>
  );
}
