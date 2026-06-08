// Bulk delegate import — landing screen. Two paths feed the same Preview RPC:
//   1. CSV/XLSX file → Presign → S3 PUT → PreviewUpsertDelegatesBulk(upload)
//   2. Google Sheets URL → PreviewUpsertDelegatesBulk(google_sheet)
// See BULK_IMPORT.md §5 + §7.

import { createSignal, Show } from "solid-js";
import { useNavigate, useParams } from "@solidjs/router";
import { ConnectError } from "@connectrpc/connect";

import { delegateClient, uploadClient } from "@/lib/api";
import {
  SourceFormat,
  type PreviewUpsertDelegatesBulkResponse,
} from "@/gen/numun/v1/delegates_pb";
import { UploadPurpose } from "@/gen/numun/v1/uploads_pb";
import DelegateImportTabPicker from "@/routes/DelegateImportTabPicker";

type Mode = "file" | "sheet";

const CSV_TYPE = "text/csv";
const XLSX_TYPE =
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";

export default function DelegateImport() {
  const params = useParams<{ delegationId: string }>();
  const navigate = useNavigate();

  const [mode, setMode] = createSignal<Mode>("file");
  const [file, setFile] = createSignal<File | null>(null);
  const [sheetUrl, setSheetUrl] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [pendingTabs, setPendingTabs] = createSignal<{
    available: string[];
    // For re-invoking Preview with a chosen tab_name:
    source:
      | { kind: "upload"; uploadKey: string; format: SourceFormat }
      | { kind: "sheet"; url: string };
  } | null>(null);

  function detectFormat(f: File): SourceFormat | null {
    const name = f.name.toLowerCase();
    if (name.endsWith(".csv")) return SourceFormat.CSV;
    if (name.endsWith(".xlsx")) return SourceFormat.XLSX;
    return null;
  }

  async function presignAndUpload(f: File, fmt: SourceFormat): Promise<string> {
    const contentType = fmt === SourceFormat.CSV ? CSV_TYPE : XLSX_TYPE;
    const presign = await uploadClient.presign({
      purpose: UploadPurpose.BULK_DELEGATES,
      filename: f.name,
      contentType,
      sizeBytes: BigInt(f.size),
    });
    const putResp = await fetch(presign.url, {
      method: "PUT",
      body: f,
      headers: presign.headers,
    });
    if (!putResp.ok) {
      throw new Error(`S3 upload failed (${putResp.status})`);
    }
    return presign.uploadKey;
  }

  function gotoPreview(
    resp: PreviewUpsertDelegatesBulkResponse,
    source:
      | { kind: "upload"; uploadKey: string; format: SourceFormat }
      | { kind: "sheet"; url: string },
    tabName: string,
  ) {
    navigate(`/delegations/${params.delegationId}/delegates/import/preview`, {
      state: {
        response: resp,
        source,
        tabName,
        delegationId: params.delegationId,
      },
    });
  }

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      if (mode() === "file") {
        const f = file();
        if (!f) {
          setError("Choose a CSV or XLSX file to upload.");
          return;
        }
        const fmt = detectFormat(f);
        if (fmt === null) {
          setError("Only .csv and .xlsx files are supported.");
          return;
        }
        const uploadKey = await presignAndUpload(f, fmt);
        const resp = await delegateClient.previewUpsertDelegatesBulk({
          delegationId: params.delegationId,
          source: {
            case: "upload",
            value: { uploadKey, format: fmt, tabName: "" },
          },
        });
        const source = { kind: "upload" as const, uploadKey, format: fmt };
        if (resp.availableTabs.length > 0) {
          setPendingTabs({ available: resp.availableTabs, source });
          return;
        }
        gotoPreview(resp, source, "");
      } else {
        const url = sheetUrl().trim();
        if (!url) {
          setError("Paste a Google Sheets URL.");
          return;
        }
        const resp = await delegateClient.previewUpsertDelegatesBulk({
          delegationId: params.delegationId,
          source: {
            case: "googleSheet",
            value: { url, tabName: "" },
          },
        });
        const source = { kind: "sheet" as const, url };
        if (resp.availableTabs.length > 0) {
          setPendingTabs({ available: resp.availableTabs, source });
          return;
        }
        gotoPreview(resp, source, "");
      }
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to start import.");
    } finally {
      setBusy(false);
    }
  }

  async function onTabPicked(tabName: string) {
    const pending = pendingTabs();
    if (!pending) return;
    setError(null);
    setBusy(true);
    try {
      const resp = await delegateClient.previewUpsertDelegatesBulk({
        delegationId: params.delegationId,
        source:
          pending.source.kind === "upload"
            ? {
                case: "upload",
                value: {
                  uploadKey: pending.source.uploadKey,
                  format: pending.source.format,
                  tabName,
                },
              }
            : {
                case: "googleSheet",
                value: { url: pending.source.url, tabName },
              },
      });
      if (resp.availableTabs.length > 0) {
        // Server still wants a tab? Refresh the list.
        setPendingTabs({
          available: resp.availableTabs,
          source: pending.source,
        });
        return;
      }
      setPendingTabs(null);
      gotoPreview(resp, pending.source, tabName);
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load tab.");
    } finally {
      setBusy(false);
    }
  }

  function onCancel() {
    navigate("/delegation");
  }

  return (
    <main class="mx-auto max-w-3xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">
            Bulk import delegates
          </h1>
          <p class="mt-1 text-sm text-nu-purple-700">
            Upload a roster as CSV / XLSX, or paste a Google Sheets link. You'll
            review the parsed rows before anything is saved.
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

      <Show when={error()}>
        {(e) => (
          <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">{e()}</p>
        )}
      </Show>

      <Show when={pendingTabs()}>
        {(p) => (
          <section class="mt-6 rounded border border-nu-purple-200 bg-white p-4">
            <h2 class="text-lg font-semibold text-nu-purple-700">
              Choose a tab
            </h2>
            <p class="mt-1 text-sm text-nu-purple-700">
              The workbook has multiple tabs. Pick which one holds the roster.
            </p>
            <div class="mt-3">
              <DelegateImportTabPicker
                tabs={p().available}
                busy={busy()}
                onPick={onTabPicked}
              />
            </div>
          </section>
        )}
      </Show>

      <Show when={!pendingTabs()}>
        <form onSubmit={onSubmit} class="mt-6 space-y-6">
          <fieldset class="rounded border border-nu-purple-200 bg-white p-4">
            <legend class="px-2 text-sm font-semibold text-nu-purple-700">
              Source
            </legend>
            <div class="flex gap-4">
              <label class="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="mode"
                  value="file"
                  checked={mode() === "file"}
                  onChange={() => setMode("file")}
                />
                Upload CSV or XLSX
              </label>
              <label class="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="mode"
                  value="sheet"
                  checked={mode() === "sheet"}
                  onChange={() => setMode("sheet")}
                />
                Paste Google Sheets URL
              </label>
            </div>

            <Show when={mode() === "file"}>
              <div class="mt-4">
                <label class="block text-sm text-nu-purple-700">
                  File
                  <input
                    type="file"
                    accept=".csv,.xlsx"
                    onChange={(e) => {
                      const list = e.currentTarget.files;
                      setFile(list && list[0] ? list[0] : null);
                    }}
                    class="mt-1 block w-full text-sm"
                  />
                </label>
                <p class="mt-2 text-xs text-nu-purple-700">
                  Max 5 MB. Up to 2,000 rows.
                </p>
              </div>
            </Show>

            <Show when={mode() === "sheet"}>
              <div class="mt-4">
                <label class="block text-sm text-nu-purple-700">
                  Google Sheets URL
                  <input
                    type="url"
                    value={sheetUrl()}
                    onInput={(e) => setSheetUrl(e.currentTarget.value)}
                    placeholder="https://docs.google.com/spreadsheets/d/..."
                    class="mt-1 block w-full rounded border border-nu-purple-300 px-2 py-1 text-sm"
                  />
                </label>
                <p
                  class="mt-2 text-xs text-nu-purple-700"
                  title='Set sharing to "Anyone with the link can view" so we can read the sheet.'
                >
                  The sheet must be set to "Anyone with the link can view".
                </p>
              </div>
            </Show>
          </fieldset>

          <section class="rounded border border-nu-purple-200 bg-nu-purple-50 p-4 text-sm text-nu-purple-700">
            <div class="font-semibold">Download a starter template</div>
            <ul class="mt-2 list-disc pl-5">
              <li>
                <a
                  href="/templates/delegate-import-template.csv"
                  class="underline"
                >
                  delegate-import-template.csv
                </a>
              </li>
              <li>
                <a
                  href="/templates/delegate-import-template.xlsx"
                  class="underline"
                >
                  delegate-import-template.xlsx
                </a>
              </li>
            </ul>
          </section>

          <div class="flex justify-end gap-2">
            <button
              type="button"
              onClick={onCancel}
              class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={busy()}
              class="rounded bg-nu-purple px-3 py-1 text-sm text-white disabled:opacity-50"
            >
              {busy() ? "Working…" : "Preview import"}
            </button>
          </div>
        </form>
      </Show>
    </main>
  );
}

function connectMessage(err: unknown): string | null {
  if (err instanceof ConnectError) return err.message;
  if (err instanceof Error) return err.message;
  return null;
}
