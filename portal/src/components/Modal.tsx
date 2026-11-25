import { type JSX } from "solid-js";

// Modal is the shared dialog shell: a dimmed overlay that closes on an
// outside click, with a titled panel.
export default function Modal(props: {
  title: string;
  onClose: () => void;
  children: JSX.Element;
}) {
  const titleId = `modal-${Math.random().toString(36).slice(2)}`;
  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4"
      onClick={() => props.onClose()}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        class="w-full max-w-md rounded border border-nu-purple-200 bg-white p-4 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id={titleId} class="text-base font-semibold text-nu-purple">
          {props.title}
        </h2>
        {props.children}
      </div>
    </div>
  );
}
