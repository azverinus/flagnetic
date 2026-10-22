import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";

import { type AttributeOption, type AttributeTypes, attributeOptions } from "./conditions";
import { attributesQuery, unitTypesQuery } from "./queries";

/**
 * What an attribute condition can name in a project, its declared
 * attributes and its unit types, once both are read: the choices and
 * the value type of each. `failed` is the read that failed, to retry.
 */
export function useConditionAttributes(
  project: string,
):
  | { state: "ready"; options: AttributeOption[]; types: AttributeTypes }
  | { state: "loading" }
  | { state: "failed"; error: unknown; retry: () => void } {
  const attributes = useQuery(attributesQuery(project));
  const unitTypes = useQuery(unitTypesQuery(project));
  const ready = useMemo(() => {
    if (!attributes.data || !unitTypes.data) return undefined;
    return {
      options: attributeOptions(attributes.data, unitTypes.data),
      types: new Map([
        ...unitTypes.data.map((type) => [type.key, "string"] as const),
        ...attributes.data.map((attribute) => [attribute.key, attribute.type] as const),
      ]) as AttributeTypes,
    };
  }, [attributes.data, unitTypes.data]);
  const failed = [attributes, unitTypes].find((read) => read.isError);
  if (failed) return { state: "failed", error: failed.error, retry: () => void failed.refetch() };
  if (!ready) return { state: "loading" };
  return { state: "ready", ...ready };
}
