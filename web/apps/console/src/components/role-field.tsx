import type { Role } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import type { Ref } from "react";

import { Field } from "@/components/ui/field";
import { Select, SelectItem } from "@/components/ui/select";
import { roleName } from "@/lib/access-names";
import { roleSummary } from "@/lib/project-members";

export type RoleFieldProps = {
  /** Every role, in the server's order. */
  roles: readonly Role[];
  /** The name of the role chosen; empty while none is. */
  value: string;
  onChange: (role: string) => void;
  onBlur?: () => void;
  ref?: Ref<HTMLButtonElement>;
  /** An error the form found itself, such as no role chosen. */
  error?: string;
  /** The refusal of the last submission, whose violation at `/role` is shown here. */
  refusal?: unknown;
};

/**
 * The choice of a role held in a project, by a grant or a project token,
 * saying under it what the chosen role lets its holder do. Nothing is
 * chosen first, so a role is always picked on purpose.
 */
export function RoleField({ roles, value, onChange, onBlur, ref, error, refusal }: RoleFieldProps) {
  const { i18n, t } = useLingui();
  const role = roles.find((candidate) => candidate.name === value);
  return (
    <Field
      label={<Trans>Role</Trans>}
      hint={role ? roleSummary(i18n, role) : undefined}
      error={error}
      refusal={refusal}
      pointer="/role"
    >
      <Select
        ref={ref}
        onBlur={onBlur}
        value={value}
        onValueChange={onChange}
        placeholder={t`Choose a role`}
      >
        {roles.map((candidate) => (
          <SelectItem key={candidate.name} value={candidate.name}>
            {roleName(i18n, candidate.name)}
          </SelectItem>
        ))}
      </Select>
    </Field>
  );
}
