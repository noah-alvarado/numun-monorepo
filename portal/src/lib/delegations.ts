import { delegationClient } from "@/lib/api";
import {
  Delegation_Status,
  type Delegation,
} from "@/gen/numun/v1/delegations_pb";

// listScopedDelegations pages through DelegationService.ListDelegations (the
// caller's in-scope delegations) so callers never stop at the first page.
export async function listScopedDelegations(
  conferenceId: string,
  status: Delegation_Status = Delegation_Status.UNSPECIFIED,
): Promise<Delegation[]> {
  const all: Delegation[] = [];
  let cursor = "";
  do {
    const resp = await delegationClient.listDelegations({
      conferenceId,
      status,
      page: { pageSize: 500, cursor },
    });
    all.push(...resp.items);
    cursor = resp.page?.nextCursor ?? "";
  } while (cursor);
  return all;
}
