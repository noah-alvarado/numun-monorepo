import { createSignal } from "solid-js";

import Modal from "@/components/Modal";

// Confirms rejecting a delegation and collects an optional reason, which is
// emailed to the advisors (EMAIL.md T2) and kept in the audit log.
export default function RejectDelegationDialog(props: {
  school: string;
  busy: boolean;
  onCancel: () => void;
  onReject: (reason: string) => void;
}) {
  const [reason, setReason] = createSignal("");
  return (
    <Modal title={`Reject ${props.school}?`} onClose={props.onCancel}>
      <p class="mt-2 text-sm text-nu-purple-700">
        The advisors are emailed that their registration wasn't approved. This
        can't be undone.
      </p>
      <label class="mt-3 block text-sm">
        <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
          Reason (optional, included in the email to the advisors)
        </span>
        <textarea
          value={reason()}
          onInput={(e) => setReason(e.currentTarget.value)}
          rows={3}
          class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
        />
      </label>
      <div class="mt-4 flex justify-end gap-2 text-sm">
        <button
          type="button"
          onClick={() => props.onCancel()}
          class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={props.busy}
          onClick={() => props.onReject(reason().trim())}
          class="rounded bg-red-700 px-3 py-1 text-white disabled:opacity-50"
        >
          {props.busy ? "Rejecting…" : "Reject"}
        </button>
      </div>
    </Modal>
  );
}
