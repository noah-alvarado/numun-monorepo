// AssignmentStudioRunControls — sidebar for Propose / Re-shuffle / Dry-run.

import { createSignal, Show } from "solid-js";
import { ConnectError } from "@connectrpc/connect";

import { assignmentClient } from "@/lib/api";
import {
  AssignmentRunStatus,
  type AssignmentRun,
  type ProposeResponse,
} from "@/gen/numun/v1/assignments_pb";

type RunMode = "propose" | "reshuffle" | "dry";

export default function AssignmentStudioRunControls(props: {
  conferenceId: string;
  currentRun: AssignmentRun | null | undefined;
  disabled: boolean;
  onComplete: (resp: ProposeResponse, mode: RunMode) => void;
}) {
  const [busy, setBusy] = createSignal<RunMode | null>(null);
  const [banner, setBanner] = createSignal<string | null>(null);
  const [bannerKind, setBannerKind] = createSignal<"info" | "warn" | "error">(
    "info",
  );

  async function run(mode: RunMode) {
    if (busy() || props.disabled) return;
    setBusy(mode);
    setBanner(null);
    try {
      const req = {
        conferenceId: props.conferenceId,
        dryRun: mode === "dry",
        ...(mode === "reshuffle" ? { seed: randomSeed() } : {}),
      };
      const resp = await assignmentClient.propose(req);
      const r = resp.run;
      if (r && r.status === AssignmentRunStatus.FAILED) {
        setBannerKind("warn");
        setBanner(r.diagnostics || "Run failed without diagnostics.");
      } else {
        setBannerKind("info");
        setBanner(
          mode === "dry"
            ? `Dry run complete — objective ${r?.objective.toFixed(2) ?? "—"}, ${resp.assignments.length} assignments.`
            : `Proposed ${resp.assignments.length} assignments.`,
        );
      }
      props.onComplete(resp, mode);
    } catch (err) {
      setBannerKind("error");
      if (err instanceof ConnectError) {
        const msg = err.message.toLowerCase();
        if (
          msg.includes("deadline") ||
          msg.includes("timeout") ||
          msg.includes("canceled")
        ) {
          setBanner("Run timed out — try again.");
        } else {
          setBanner(err.message);
        }
      } else if (err instanceof Error) {
        setBanner(err.message);
      } else {
        setBanner("Run failed.");
      }
    } finally {
      setBusy(null);
    }
  }

  return (
    <aside class="space-y-3 rounded border border-nu-purple-200 bg-white p-4 text-sm">
      <h2 class="text-base font-semibold text-nu-purple">Run controls</h2>
      <Show when={props.disabled}>
        <p class="rounded bg-amber-50 p-2 text-xs text-amber-900">
          Run in progress — propose disabled until it completes.
        </p>
      </Show>
      <button
        type="button"
        disabled={!!busy() || props.disabled}
        onClick={() => run("propose")}
        class="block w-full rounded bg-nu-purple px-3 py-2 text-white disabled:opacity-50"
      >
        {busy() === "propose" ? "Proposing…" : "Propose"}
      </button>
      <button
        type="button"
        disabled={!!busy() || props.disabled}
        onClick={() => run("reshuffle")}
        class="block w-full rounded border border-nu-purple-300 px-3 py-2 text-nu-purple-700 disabled:opacity-50"
      >
        {busy() === "reshuffle" ? "Shuffling…" : "Re-shuffle"}
      </button>
      <button
        type="button"
        disabled={!!busy() || props.disabled}
        onClick={() => run("dry")}
        class="block w-full rounded border border-nu-purple-300 px-3 py-2 text-nu-purple-700 disabled:opacity-50"
      >
        {busy() === "dry" ? "Running…" : "Dry run"}
      </button>
      <Show when={banner()}>
        {(b) => (
          <p
            class={
              bannerKind() === "error"
                ? "rounded bg-red-50 p-2 text-xs text-red-900"
                : bannerKind() === "warn"
                  ? "rounded bg-amber-50 p-2 text-xs text-amber-900"
                  : "rounded bg-nu-purple-50 p-2 text-xs text-nu-purple-700"
            }
          >
            {b()}
          </p>
        )}
      </Show>
      <p class="text-xs text-nu-purple-700">
        Propose persists. Dry run returns a proposal without writes. Re-shuffle
        uses a fresh random seed.
      </p>
    </aside>
  );
}

function randomSeed(): bigint {
  // Generate a uniformly-random uint64 client-side via crypto.getRandomValues.
  const buf = new Uint32Array(2);
  crypto.getRandomValues(buf);
  return (BigInt(buf[0]) << 32n) | BigInt(buf[1]);
}
