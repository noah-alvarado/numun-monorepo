// Valibot schema for delegation forms. Mirrors the protovalidate annotations
// on delegations.proto so client-side errors line up with server-side ones.
// Manual sync for v1 per IMPLEMENTATION_PLAN.md M4 (codegen path is an open
// item).

import * as v from "valibot";

export const TrinaryValues = ["", "positive", "negative", "neutral"] as const;
export type TrinaryValue = (typeof TrinaryValues)[number];

export const AddressSchema = v.object({
  street: v.pipe(v.string(), v.trim()),
  city: v.pipe(v.string(), v.trim()),
  state: v.pipe(v.string(), v.trim()),
  postalCode: v.pipe(v.string(), v.trim()),
  country: v.pipe(v.string(), v.trim()),
});

export const EstimatedSchema = v.object({
  total: v.pipe(
    v.number(),
    v.integer("Whole numbers only."),
    v.minValue(0, "Must be zero or more."),
  ),
  financiallyQualifying: v.pipe(
    v.number(),
    v.integer("Whole numbers only."),
    v.minValue(0, "Must be zero or more."),
  ),
});

export const PrefsSchema = v.object({
  typeCrisis: v.picklist(TrinaryValues),
  typeNonCrisis: v.picklist(TrinaryValues),
  sizeSmall: v.picklist(TrinaryValues),
  sizeMedium: v.picklist(TrinaryValues),
  sizeLarge: v.picklist(TrinaryValues),
});

// DelegationFormValues is the shape both Create and Edit forms operate on.
// Edit also carries `expectedVersion` so the submit-handler can pass it
// through to UpdateDelegation; Create ignores it.
export const DelegationFormSchema = v.object({
  school: v.pipe(
    v.string(),
    v.trim(),
    v.minLength(1, "School name is required."),
    v.maxLength(200, "Up to 200 characters."),
  ),
  address: AddressSchema,
  estimated: EstimatedSchema,
  prefs: PrefsSchema,
});

export type DelegationFormValues = v.InferOutput<typeof DelegationFormSchema>;

export function emptyDelegationForm(): DelegationFormValues {
  return {
    school: "",
    address: { street: "", city: "", state: "", postalCode: "", country: "" },
    estimated: { total: 0, financiallyQualifying: 0 },
    prefs: {
      typeCrisis: "",
      typeNonCrisis: "",
      sizeSmall: "",
      sizeMedium: "",
      sizeLarge: "",
    },
  };
}

// Map our string "positive"/"negative"/"neutral" to the generated Trinary enum.
import { Trinary } from "@/gen/numun/v1/delegations_pb";

export function trinaryToProto(t: TrinaryValue): Trinary {
  switch (t) {
    case "positive":
      return Trinary.POSITIVE;
    case "negative":
      return Trinary.NEGATIVE;
    case "neutral":
      return Trinary.NEUTRAL;
  }
  return Trinary.UNSPECIFIED;
}

export function trinaryFromProto(t: Trinary): TrinaryValue {
  switch (t) {
    case Trinary.POSITIVE:
      return "positive";
    case Trinary.NEGATIVE:
      return "negative";
    case Trinary.NEUTRAL:
      return "neutral";
  }
  return "";
}
