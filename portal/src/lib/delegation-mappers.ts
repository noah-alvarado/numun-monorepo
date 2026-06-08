// Conversions between domain proto messages and the DelegationForm value shape.

import { create } from "@bufbuild/protobuf";
import {
  AddressSchema,
  type Address,
} from "@/gen/numun/v1/common_pb";
import {
  CommitteePreferencesSchema,
  type Delegation,
  EstimatedDelegatesSchema,
  type CommitteePreferences,
  type EstimatedDelegates,
} from "@/gen/numun/v1/delegations_pb";
import {
  trinaryFromProto,
  trinaryToProto,
  type DelegationFormValues,
  emptyDelegationForm,
} from "./delegation-schema";

export function delegationToForm(d: Delegation): DelegationFormValues {
  const base = emptyDelegationForm();
  return {
    school: d.school,
    address: {
      street: d.address?.street ?? base.address.street,
      city: d.address?.city ?? base.address.city,
      state: d.address?.state ?? base.address.state,
      postalCode: d.address?.postalCode ?? base.address.postalCode,
      country: d.address?.country ?? base.address.country,
    },
    estimated: {
      total: d.estimatedDelegates?.total ?? 0,
      financiallyQualifying: d.estimatedDelegates?.financiallyQualifying ?? 0,
    },
    prefs: {
      typeCrisis: trinaryFromProto(d.committeePreferences?.type?.crisis ?? 0),
      typeNonCrisis: trinaryFromProto(
        d.committeePreferences?.type?.nonCrisis ?? 0,
      ),
      sizeSmall: trinaryFromProto(d.committeePreferences?.size?.small ?? 0),
      sizeMedium: trinaryFromProto(d.committeePreferences?.size?.medium ?? 0),
      sizeLarge: trinaryFromProto(d.committeePreferences?.size?.large ?? 0),
    },
  };
}

export function formToAddress(v: DelegationFormValues): Address {
  return create(AddressSchema, {
    street: v.address.street,
    city: v.address.city,
    state: v.address.state,
    postalCode: v.address.postalCode,
    country: v.address.country,
  });
}

export function formToEstimated(v: DelegationFormValues): EstimatedDelegates {
  return create(EstimatedDelegatesSchema, {
    total: v.estimated.total,
    financiallyQualifying: v.estimated.financiallyQualifying,
  });
}

export function formToPrefs(v: DelegationFormValues): CommitteePreferences {
  return create(CommitteePreferencesSchema, {
    type: {
      crisis: trinaryToProto(v.prefs.typeCrisis),
      nonCrisis: trinaryToProto(v.prefs.typeNonCrisis),
    },
    size: {
      small: trinaryToProto(v.prefs.sizeSmall),
      medium: trinaryToProto(v.prefs.sizeMedium),
      large: trinaryToProto(v.prefs.sizeLarge),
    },
  });
}
