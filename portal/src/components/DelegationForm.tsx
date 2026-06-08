// DelegationForm — used by both Create-My-Delegation (first action) and
// Edit-My-Delegation. Initial values default to empty for create; consumers
// pass `initialValues` to populate from an existing Delegation row.
//
// The submit handler is the integration point: the parent decides whether to
// call DelegationService.CreateDelegation or UpdateDelegation. Errors thrown
// from the handler bubble to the form's response slot.

import { createForm, valiForm } from "@modular-forms/solid";
import { For, Show, type JSX } from "solid-js";

import {
  DelegationFormSchema,
  type DelegationFormValues,
  emptyDelegationForm,
  TrinaryValues,
  type TrinaryValue,
} from "@/lib/delegation-schema";

type Props = {
  initialValues?: DelegationFormValues;
  submitLabel?: string;
  onSubmit: (values: DelegationFormValues) => Promise<void>;
  // Optional banner for handler-level errors that aren't field-shaped
  // (e.g., optimistic-lock 409 prompting the user to re-fetch).
  banner?: JSX.Element;
};

export default function DelegationForm(props: Props) {
  const [form, { Form, Field }] = createForm<DelegationFormValues>({
    initialValues: props.initialValues ?? emptyDelegationForm(),
    validate: valiForm(DelegationFormSchema),
  });

  return (
    <Form
      onSubmit={async (values) => {
        await props.onSubmit(values);
      }}
      class="space-y-6"
    >
      <Show when={props.banner}>{props.banner}</Show>

      <section class="space-y-3">
        <h3 class="text-base font-semibold text-nu-purple-700">School</h3>
        <Field name="school">
          {(field, fieldProps) => (
            <label class="block">
              <span class="text-sm text-nu-purple-700">School name</span>
              <input
                {...fieldProps}
                type="text"
                value={field.value ?? ""}
                class="mt-1 block w-full rounded border border-nu-purple-300 px-3 py-2"
                placeholder="Northwestern University"
              />
              <Show when={field.error}>
                <p class="mt-1 text-sm text-red-600">{field.error}</p>
              </Show>
            </label>
          )}
        </Field>
      </section>

      <section class="space-y-3">
        <h3 class="text-base font-semibold text-nu-purple-700">Address</h3>
        <Field name="address.street">
          {(field, fieldProps) => (
            <Input label="Street" field={field} fieldProps={fieldProps} />
          )}
        </Field>
        <div class="grid grid-cols-2 gap-3">
          <Field name="address.city">
            {(field, fieldProps) => (
              <Input label="City" field={field} fieldProps={fieldProps} />
            )}
          </Field>
          <Field name="address.state">
            {(field, fieldProps) => (
              <Input label="State" field={field} fieldProps={fieldProps} />
            )}
          </Field>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <Field name="address.postalCode">
            {(field, fieldProps) => (
              <Input
                label="Postal code"
                field={field}
                fieldProps={fieldProps}
              />
            )}
          </Field>
          <Field name="address.country">
            {(field, fieldProps) => (
              <Input label="Country" field={field} fieldProps={fieldProps} />
            )}
          </Field>
        </div>
      </section>

      <section class="space-y-3">
        <h3 class="text-base font-semibold text-nu-purple-700">
          Estimated delegates
        </h3>
        <div class="grid grid-cols-2 gap-3">
          <Field name="estimated.total" type="number">
            {(field, fieldProps) => (
              <NumberInput
                label="Total"
                field={field}
                fieldProps={fieldProps}
              />
            )}
          </Field>
          <Field name="estimated.financiallyQualifying" type="number">
            {(field, fieldProps) => (
              <NumberInput
                label="Financially qualifying"
                field={field}
                fieldProps={fieldProps}
              />
            )}
          </Field>
        </div>
      </section>

      <section class="space-y-3">
        <h3 class="text-base font-semibold text-nu-purple-700">
          Committee preferences
        </h3>
        <p class="text-sm text-nu-purple-700">
          Indicate per-axis comfort. Used by the assignment algorithm.
        </p>
        <PrefsRow name="prefs.typeCrisis" label="Crisis committees" Field={Field} />
        <PrefsRow
          name="prefs.typeNonCrisis"
          label="Non-crisis committees"
          Field={Field}
        />
        <PrefsRow name="prefs.sizeSmall" label="Small committees" Field={Field} />
        <PrefsRow name="prefs.sizeMedium" label="Medium committees" Field={Field} />
        <PrefsRow name="prefs.sizeLarge" label="Large committees" Field={Field} />
      </section>

      <div class="flex items-center justify-end gap-3">
        <button
          type="submit"
          disabled={form.submitting}
          class="rounded bg-nu-purple px-4 py-2 text-white disabled:opacity-50"
        >
          {form.submitting ? "Saving…" : (props.submitLabel ?? "Save")}
        </button>
      </div>
    </Form>
  );
}

function Input(props: {
  label: string;
  field: { value?: string | undefined; error: string };
  fieldProps: JSX.HTMLAttributes<HTMLInputElement> & { name: string };
}) {
  return (
    <label class="block">
      <span class="text-sm text-nu-purple-700">{props.label}</span>
      <input
        {...props.fieldProps}
        type="text"
        value={props.field.value ?? ""}
        class="mt-1 block w-full rounded border border-nu-purple-300 px-3 py-2"
      />
      <Show when={props.field.error}>
        <p class="mt-1 text-sm text-red-600">{props.field.error}</p>
      </Show>
    </label>
  );
}

function NumberInput(props: {
  label: string;
  field: { value?: number | undefined; error: string };
  fieldProps: JSX.HTMLAttributes<HTMLInputElement> & { name: string };
}) {
  return (
    <label class="block">
      <span class="text-sm text-nu-purple-700">{props.label}</span>
      <input
        {...props.fieldProps}
        type="number"
        min="0"
        value={props.field.value ?? 0}
        class="mt-1 block w-full rounded border border-nu-purple-300 px-3 py-2"
      />
      <Show when={props.field.error}>
        <p class="mt-1 text-sm text-red-600">{props.field.error}</p>
      </Show>
    </label>
  );
}

// PrefsRow renders a 4-button trinary picker for a single axis.
function PrefsRow(props: {
  name:
    | "prefs.typeCrisis"
    | "prefs.typeNonCrisis"
    | "prefs.sizeSmall"
    | "prefs.sizeMedium"
    | "prefs.sizeLarge";
  label: string;
  Field: (p: {
    name: typeof props.name;
    children: (
      field: { value?: TrinaryValue; error: string },
      props: JSX.HTMLAttributes<HTMLInputElement> & { name: string },
    ) => JSX.Element;
  }) => JSX.Element;
}) {
  const labels: Record<TrinaryValue, string> = {
    "": "—",
    positive: "Yes",
    neutral: "Neutral",
    negative: "No",
  };
  return (
    <props.Field name={props.name}>
      {(field, fieldProps) => (
        <div class="grid grid-cols-[1fr_auto] items-center gap-3">
          <span class="text-sm text-nu-purple-700">{props.label}</span>
          <div class="flex gap-1">
            <For each={TrinaryValues}>
              {(v) => (
                <label class="cursor-pointer">
                  <input
                    type="radio"
                    name={fieldProps.name}
                    value={v}
                    checked={field.value === v}
                    onChange={fieldProps.onChange}
                    onBlur={fieldProps.onBlur}
                    class="peer sr-only"
                  />
                  <span class="rounded border border-nu-purple-300 px-2 py-1 text-xs peer-checked:border-nu-purple peer-checked:bg-nu-purple peer-checked:text-white">
                    {labels[v]}
                  </span>
                </label>
              )}
            </For>
          </div>
        </div>
      )}
    </props.Field>
  );
}
