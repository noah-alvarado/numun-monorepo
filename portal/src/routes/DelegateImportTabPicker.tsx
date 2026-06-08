// Tab picker — rendered when PreviewUpsertDelegatesBulk returns
// `available_tabs.length > 0`. Each click re-invokes Preview with the chosen
// `tab_name`. See BULK_IMPORT.md §2.4.

import { For } from "solid-js";

export default function DelegateImportTabPicker(props: {
  tabs: string[];
  busy: boolean;
  onPick: (tabName: string) => void;
}) {
  return (
    <ul class="space-y-2">
      <For each={props.tabs}>
        {(tab) => (
          <li>
            <button
              type="button"
              disabled={props.busy}
              onClick={() => props.onPick(tab)}
              class="w-full rounded border border-nu-purple-300 px-3 py-2 text-left text-sm text-nu-purple-700 hover:bg-nu-purple-50 disabled:opacity-50"
            >
              {tab}
            </button>
          </li>
        )}
      </For>
    </ul>
  );
}
