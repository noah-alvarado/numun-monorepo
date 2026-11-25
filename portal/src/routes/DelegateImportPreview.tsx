// Bulk import preview — paginated editable table of parsed rows. Confirm
// disables when any row has errors. See BULK_IMPORT.md §5.

import { createMemo, createSignal, For, Index, Show } from "solid-js";
import { useLocation, useNavigate, useParams } from "@solidjs/router";

import { create } from "@bufbuild/protobuf";

import { delegateClient } from "@/lib/api";
import {
  DelegateInputSchema,
  ExperienceLevel,
  UpsertMode,
  type PreviewRow,
  type PreviewSummary,
  type PreviewUpsertDelegatesBulkResponse,
} from "@/gen/numun/v1/delegates_pb";
import { connectMessage } from "@/lib/errors";

type ImportSource =
  | { kind: "upload"; uploadKey: string; format: number }
  | { kind: "sheet"; url: string };

type PreviewState = {
  response: PreviewUpsertDelegatesBulkResponse;
  source: ImportSource;
  tabName: string;
  delegationId: string;
};

const PAGE_SIZE = 50;

// EditableRow is what we render in the table and submit to commit. It
// mirrors DelegateInput plus the per-row preview metadata.
type EditableRow = {
  rowNumber: number;
  firstName: string;
  lastName: string;
  email: string;
  experienceLevel: ExperienceLevel;
  errors: { field: string; message: string }[];
  match: PreviewRow["match"];
};

export default function DelegateImportPreview() {
  const params = useParams<{ delegationId: string }>();
  const location = useLocation<PreviewState>();
  const navigate = useNavigate();

  const state = location.state;

  if (!state || !state.response) {
    return (
      <main class="mx-auto max-w-3xl px-6 py-8">
        <p class="rounded bg-amber-50 p-3 text-sm text-amber-900">
          No preview to show. Start an import first.
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
      </main>
    );
  }

  const initialRows: EditableRow[] = state.response.rows.map(toEditable);

  const [rows, setRows] = createSignal<EditableRow[]>(initialRows);
  const [mode, setMode] = createSignal<UpsertMode>(UpsertMode.ADDITIVE);
  const [page, setPage] = createSignal(0);
  const [expanded, setExpanded] = createSignal<number | null>(null);
  const [busy, setBusy] = createSignal(false);
  const [banner, setBanner] = createSignal<string | null>(null);

  const summary: PreviewSummary | undefined = state.response.summary;
  const uploadId = state.response.uploadId;

  const totalPages = createMemo(() =>
    Math.max(1, Math.ceil(rows().length / PAGE_SIZE)),
  );
  const visible = createMemo(() => {
    const start = page() * PAGE_SIZE;
    return rows().slice(start, start + PAGE_SIZE);
  });
  const errorCount = createMemo(
    () => rows().filter((r) => r.errors.length > 0).length,
  );
  const hasErrors = () => errorCount() > 0;

  function updateRow(rowNumber: number, patch: Partial<EditableRow>) {
    // Editing a field clears its preview errors so the advisor can fix rows
    // inline; the commit re-validates server-side (BULK_IMPORT.md §4).
    const edited = Object.keys(patch);
    setRows(
      rows().map((r) =>
        r.rowNumber === rowNumber
          ? {
              ...r,
              ...patch,
              errors: r.errors.filter(
                (e) => !edited.some((f) => e.field.startsWith(f)),
              ),
            }
          : r,
      ),
    );
  }

  async function onCancel() {
    if (uploadId) {
      try {
        await delegateClient.deleteBulkImportPreview({ uploadId });
      } catch {
        /* best-effort */
      }
    }
    navigate("/delegation");
  }

  async function onConfirm() {
    setBusy(true);
    setBanner(null);
    try {
      const wireRows = rows().map((r) =>
        create(DelegateInputSchema, {
          firstName: r.firstName,
          lastName: r.lastName,
          email: r.email,
          experienceLevel: r.experienceLevel,
        }),
      );
      const resp = await delegateClient.upsertDelegatesBulk({
        uploadId,
        delegationId: params.delegationId,
        mode: mode(),
        rows: wireRows,
      });
      navigate(`/delegations/${params.delegationId}/delegates/import/result`, {
        state: {
          summary: resp.summary,
          delegationId: params.delegationId,
        },
      });
    } catch (err) {
      setBanner(connectMessage(err) ?? "Failed to commit import.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="mx-auto max-w-6xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">Review import</h1>
          <p class="mt-1 text-sm text-nu-purple-700">
            Edit cells inline. Nothing is saved until you click "Confirm
            import".
          </p>
        </div>
        <button
          type="button"
          onClick={onCancel}
          class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700"
        >
          Cancel
        </button>
      </header>

      <Show when={banner()}>
        {(b) => (
          <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">{b()}</p>
        )}
      </Show>

      <Show when={summary}>
        {(s) => (
          <SummaryBar
            summary={s()}
            errorCount={errorCount()}
            validCount={rows().length - errorCount()}
            fullSync={mode() === UpsertMode.FULL_SYNC}
          />
        )}
      </Show>

      <section class="mt-4 rounded border border-nu-purple-200 bg-white p-4">
        <legend class="text-sm font-semibold text-nu-purple-700">
          Import mode
        </legend>
        <div class="mt-2 flex gap-4 text-sm">
          <label class="flex items-center gap-2">
            <input
              type="radio"
              name="mode"
              checked={mode() === UpsertMode.ADDITIVE}
              onChange={() => setMode(UpsertMode.ADDITIVE)}
            />
            Additive (default) — only create or update
          </label>
          <label class="flex items-center gap-2">
            <input
              type="radio"
              name="mode"
              checked={mode() === UpsertMode.FULL_SYNC}
              onChange={() => setMode(UpsertMode.FULL_SYNC)}
            />
            Full sync — also soft-delete missing
          </label>
        </div>
        <Show when={mode() === UpsertMode.FULL_SYNC}>
          <p class="mt-3 rounded bg-red-50 p-3 text-sm text-red-900">
            <strong>Warning:</strong> Full sync will soft-delete any existing
            delegates in this delegation that are not present in this file.
            Soft-deleted rows can be restored by staff.
          </p>
        </Show>
      </section>

      <Show
        when={
          summary && summary.ignoredColumns && summary.ignoredColumns.length > 0
        }
      >
        <p class="mt-4 text-xs text-nu-purple-700">
          {summary!.ignoredColumns.length} unrecognized column
          {summary!.ignoredColumns.length === 1 ? "" : "s"} will be ignored:{" "}
          {summary!.ignoredColumns.join(", ")}.
        </p>
      </Show>

      <section class="mt-4 overflow-x-auto rounded border border-nu-purple-200 bg-white">
        <table class="w-full table-fixed text-sm">
          <thead class="bg-nu-purple-50 text-left text-xs uppercase tracking-wide text-nu-purple-700">
            <tr>
              <th class="w-12 px-2 py-2">#</th>
              <th class="w-32 px-2 py-2">First name</th>
              <th class="w-32 px-2 py-2">Last name</th>
              <th class="w-56 px-2 py-2">Email</th>
              <th class="w-32 px-2 py-2">Experience</th>
              <th class="w-44 px-2 py-2">Match</th>
              <th class="w-56 px-2 py-2">Errors</th>
            </tr>
          </thead>
          <tbody>
            {/* Index, not For: edits replace the row object, and For would
                re-mount the row (losing input focus) on every keystroke. */}
            <Index each={visible()}>
              {(row) => (
                <RowView
                  row={row()}
                  expanded={expanded() === row().rowNumber}
                  onToggleExpand={() =>
                    setExpanded(
                      expanded() === row().rowNumber ? null : row().rowNumber,
                    )
                  }
                  onChange={(patch) => updateRow(row().rowNumber, patch)}
                />
              )}
            </Index>
          </tbody>
        </table>
      </section>

      <div class="mt-3 flex items-center justify-between text-sm">
        <div class="text-nu-purple-700">
          Page {page() + 1} of {totalPages()} · {rows().length}{" "}
          {rows().length === 1 ? "row" : "rows"}
        </div>
        <div class="flex gap-2">
          <button
            type="button"
            disabled={page() === 0}
            onClick={() => setPage(Math.max(0, page() - 1))}
            class="rounded border border-nu-purple-300 px-2 py-1 text-nu-purple-700 disabled:opacity-50"
          >
            Prev
          </button>
          <button
            type="button"
            disabled={page() >= totalPages() - 1}
            onClick={() => setPage(Math.min(totalPages() - 1, page() + 1))}
            class="rounded border border-nu-purple-300 px-2 py-1 text-nu-purple-700 disabled:opacity-50"
          >
            Next
          </button>
        </div>
      </div>

      <div class="mt-6 flex items-center justify-between">
        <Show when={hasErrors()}>
          <p class="text-sm text-red-900">
            Fix all row errors before confirming.
          </p>
        </Show>
        <div class="ml-auto flex gap-2">
          <button
            type="button"
            onClick={onCancel}
            class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={busy() || hasErrors()}
            onClick={onConfirm}
            class="rounded bg-nu-purple px-3 py-1 text-sm text-white disabled:opacity-50"
          >
            {busy() ? "Importing…" : "Confirm import"}
          </button>
        </div>
      </div>
    </main>
  );
}

// Valid/Errors track inline fixes; the rest is the server's preview summary.
function SummaryBar(props: {
  summary: PreviewSummary;
  errorCount: number;
  validCount: number;
  fullSync: boolean;
}) {
  const s = props.summary;
  return (
    <section class="mt-4 grid grid-cols-2 gap-2 rounded border border-nu-purple-200 bg-white p-4 text-sm md:grid-cols-4">
      <Stat label="Parsed" value={s.parsedCount} />
      <Stat label="Valid" value={props.validCount} />
      <Stat
        label="Errors"
        value={props.errorCount}
        kind={props.errorCount > 0 ? "warn" : "ok"}
      />
      <Stat label="Create" value={s.createCount} />
      <Stat label="Update" value={s.updateCount} />
      <Stat label="Matched by email" value={s.matchByEmail} />
      <Stat label="Matched by name" value={s.matchByName} />
      {/* Additive imports never delete; the count is what Full sync would. */}
      <Stat
        label="Will soft-delete"
        value={props.fullSync ? s.softDeleteCount : 0}
        kind={props.fullSync && s.softDeleteCount > 0 ? "warn" : "ok"}
      />
    </section>
  );
}

function Stat(props: { label: string; value: number; kind?: "ok" | "warn" }) {
  return (
    <div>
      <div class="text-xs uppercase tracking-wide text-nu-purple-700">
        {props.label}
      </div>
      <div
        class={
          props.kind === "warn"
            ? "text-lg font-semibold text-red-700"
            : "text-lg font-semibold text-nu-purple"
        }
      >
        {props.value}
      </div>
    </div>
  );
}

function RowView(props: {
  row: EditableRow;
  expanded: boolean;
  onToggleExpand: () => void;
  onChange: (patch: Partial<EditableRow>) => void;
}) {
  const hasErr = () => props.row.errors.length > 0;
  return (
    <>
      <tr
        class={
          hasErr()
            ? "border-t border-red-300 bg-red-50"
            : "border-t border-nu-purple-100"
        }
      >
        <td class="px-2 py-1 align-top text-xs text-nu-purple-700">
          {props.row.rowNumber}
        </td>
        <td class="px-2 py-1 align-top">
          <CellInput
            value={props.row.firstName}
            onChange={(v) => props.onChange({ firstName: v })}
            invalid={fieldHasError(props.row, "firstName")}
          />
        </td>
        <td class="px-2 py-1 align-top">
          <CellInput
            value={props.row.lastName}
            onChange={(v) => props.onChange({ lastName: v })}
            invalid={fieldHasError(props.row, "lastName")}
          />
        </td>
        <td class="px-2 py-1 align-top">
          <CellInput
            value={props.row.email}
            onChange={(v) => props.onChange({ email: v })}
            invalid={fieldHasError(props.row, "email")}
          />
        </td>
        <td class="px-2 py-1 align-top">
          <select
            value={String(props.row.experienceLevel)}
            onChange={(e) =>
              props.onChange({
                experienceLevel: Number(
                  e.currentTarget.value,
                ) as ExperienceLevel,
              })
            }
            class="w-full rounded border border-nu-purple-300 px-1 py-0.5 text-sm"
          >
            <option value={String(ExperienceLevel.UNSPECIFIED)}>—</option>
            <option value={String(ExperienceLevel.NOVICE)}>Novice</option>
            <option value={String(ExperienceLevel.INTERMEDIATE)}>
              Intermediate
            </option>
            <option value={String(ExperienceLevel.ADVANCED)}>Advanced</option>
          </select>
        </td>
        <td class="px-2 py-1 align-top">
          <MatchChip
            match={props.row.match}
            expanded={props.expanded}
            onToggle={props.onToggleExpand}
          />
        </td>
        <td class="px-2 py-1 align-top text-xs text-red-900">
          <For each={props.row.errors}>
            {(e) => (
              <div>
                <strong>{e.field}:</strong> {e.message}
              </div>
            )}
          </For>
        </td>
      </tr>
      <Show
        when={
          props.expanded &&
          props.row.match.case === "update" &&
          props.row.match.value
        }
      >
        <tr class="border-t border-nu-purple-100 bg-nu-purple-50">
          <td colspan={7} class="px-4 py-2 text-xs">
            <div class="font-semibold text-nu-purple-700">Diff</div>
            <DiffTable
              diff={
                (props.row.match.case === "update" &&
                  props.row.match.value.diff) ||
                {}
              }
            />
          </td>
        </tr>
      </Show>
    </>
  );
}

function MatchChip(props: {
  match: PreviewRow["match"];
  expanded: boolean;
  onToggle: () => void;
}) {
  if (props.match.case === "create") {
    return (
      <span class="inline-block rounded bg-nu-green/20 px-2 py-0.5 text-xs font-semibold text-nu-green-dark">
        new
      </span>
    );
  }
  if (props.match.case === "update") {
    return (
      <button
        type="button"
        onClick={props.onToggle}
        class="rounded bg-nu-purple-50 px-2 py-0.5 text-xs font-semibold text-nu-purple-700 underline"
      >
        update — {props.expanded ? "hide diff" : "see diff"}
      </button>
    );
  }
  if (props.match.case === "conflict") {
    return (
      <span class="inline-block rounded bg-red-100 px-2 py-0.5 text-xs font-semibold text-red-900">
        conflict — row {props.match.value.withRowNumber}
      </span>
    );
  }
  return <span class="text-xs text-nu-purple-700">—</span>;
}

function DiffTable(props: {
  diff: { [k: string]: { old: string; new: string } };
}) {
  const entries = () => Object.entries(props.diff);
  return (
    <table class="mt-1 w-full text-xs">
      <thead class="text-nu-purple-700">
        <tr>
          <th class="w-32 text-left">Field</th>
          <th class="text-left">Before</th>
          <th class="text-left">After</th>
        </tr>
      </thead>
      <tbody>
        <For each={entries()}>
          {([k, v]) => (
            <tr>
              <td class="py-0.5 pr-2 font-semibold">{fieldLabel(k)}</td>
              <td class="py-0.5 pr-2 text-nu-purple-700 line-through">
                {diffValue(k, v.old)}
              </td>
              <td class="py-0.5">{diffValue(k, v.new)}</td>
            </tr>
          )}
        </For>
      </tbody>
    </table>
  );
}

const FIELD_LABELS: Record<string, string> = {
  firstName: "First name",
  lastName: "Last name",
  email: "Email",
  experienceLevel: "Experience",
};

function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field;
}

function diffValue(field: string, v: string): string {
  if (!v) return "—";
  return field === "experienceLevel"
    ? v.charAt(0).toUpperCase() + v.slice(1)
    : v;
}

function CellInput(props: {
  value: string;
  onChange: (v: string) => void;
  invalid?: boolean;
}) {
  return (
    <input
      type="text"
      value={props.value}
      onInput={(e) => props.onChange(e.currentTarget.value)}
      class={
        props.invalid
          ? "w-full rounded border border-red-400 bg-red-50 px-1 py-0.5 text-sm"
          : "w-full rounded border border-nu-purple-200 px-1 py-0.5 text-sm"
      }
    />
  );
}

function fieldHasError(row: EditableRow, field: string): boolean {
  return row.errors.some((e) => e.field === field || e.field.startsWith(field));
}

function toEditable(r: PreviewRow): EditableRow {
  const input = r.input;
  return {
    rowNumber: r.rowNumber,
    firstName: input?.firstName ?? "",
    lastName: input?.lastName ?? "",
    email: input?.email ?? "",
    experienceLevel: input?.experienceLevel ?? ExperienceLevel.UNSPECIFIED,
    errors: r.errors.map((e) => ({ field: e.field, message: e.message })),
    match: r.match,
  };
}
