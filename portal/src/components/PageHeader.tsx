import { Show, type JSX } from "solid-js";
import { useNavigate } from "@solidjs/router";

// PageHeader is the title row shared by the admin and account pages: a title,
// an optional subtitle and Back (to the dashboard) plus any extra actions.
export default function PageHeader(props: {
  title: string;
  subtitle?: string;
  actions?: JSX.Element;
}) {
  const navigate = useNavigate();
  return (
    <header class="flex items-center justify-between gap-4">
      <div>
        <h1 class="text-2xl font-bold text-nu-purple">{props.title}</h1>
        <Show when={props.subtitle}>
          <p class="mt-1 text-sm text-nu-purple-700">{props.subtitle}</p>
        </Show>
      </div>
      <div class="flex gap-2 text-sm">
        {props.actions}
        <button
          type="button"
          onClick={() => navigate("/dashboard")}
          class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
        >
          Back
        </button>
      </div>
    </header>
  );
}
