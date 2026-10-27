import type { EventType } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo } from "react";

import { DailyChart, DayRangeSelect } from "@/components/daily-chart";
import { Gaps, VolumeTable } from "@/components/event-volume";
import { QueryError } from "@/components/query-error";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { dayRangeOf, daysSearchOf, rangeOf, validateDaysSearch } from "@/lib/daily-counts";
import { eventTypeName, gapDays, volumeSeries } from "@/lib/event-volume";
import { eventVolumeQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/event-volume")({
  validateSearch: validateDaysSearch,
  staticData: { crumb: msg`Event volume` },
  component: EventVolume,
});

/**
 * What the project's event store received per UTC day and type of event,
 * and what retention dropped before the rollups read it: a gap, whose
 * events are missing from every count read from them.
 */
function EventVolume() {
  const { i18n, t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const days = dayRangeOf(search);
  // The range ends on the UTC day the page opened on.
  const range = useMemo(() => rangeOf(new Date(), days), [days]);
  const volume = useQuery(eventVolumeQuery(project, range));
  const series = useMemo(
    () => volumeSeries(volume.data?.days ?? [], range.days),
    [volume.data, range],
  );
  const touched = useMemo(() => gapDays(volume.data?.gaps ?? [], range.days), [volume.data, range]);
  const typeName = (key: string) => eventTypeName(i18n, key as EventType);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Event volume`}
        description={
          <Trans>
            The events SDKs posted to the project per UTC day they were received, as the rollups
            counted them. The counts stay after retention drops the events themselves.
          </Trans>
        }
        actions={
          <DayRangeSelect
            value={days}
            onChange={(next) =>
              void navigate({ search: { days: daysSearchOf(next) }, replace: true })
            }
          />
        }
      />
      {volume.isError ? (
        <QueryError error={volume.error} onRetry={() => void volume.refetch()} />
      ) : volume.data === undefined ? (
        <div className="flex flex-col gap-3" aria-busy>
          <Skeleton className="h-6 w-48" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        <>
          <DailyChart
            label={t`Events per day`}
            stacked={series}
            nameOf={typeName}
            marked={{ days: touched, name: t`Days with a gap` }}
            empty={<Trans>The project received no event in these days.</Trans>}
          />
          <details className="text-sm">
            <summary className="cursor-pointer text-muted-foreground">
              <Trans>Show as table</Trans>
            </summary>
            <VolumeTable days={series.days} touched={touched} />
          </details>
          <Gaps gaps={volume.data.gaps} />
        </>
      )}
    </div>
  );
}
