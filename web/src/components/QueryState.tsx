import type { ReactElement, ReactNode } from "react";
import { Alert, Button, Group, Loader, Stack, Text } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { describeApiFailure } from "@/api/errors";

// QueryState is the four-state ladder every screen in this tree draws around
// a TanStack query: a loader, then the failure with its «Повторить» button,
// then whatever the screen itself renders. It was copy-pasted into fifteen
// screens — identical markup down to the `size={18}` on the icon and the
// `component="output"` on the loading sentence — which meant a fix to the
// shape (the live region, a colour, the retry wording) had to be applied
// fifteen times or not at all, and twice it was not.
//
// What it deliberately does NOT own:
//   - the screen's root data-testid, which by contract sits OUTSIDE every
//     state switch (docs/agent/contract-frontend.md) so routes.test.tsx can
//     find the screen without stubbing its requests. This component always
//     renders inside that marker, never around it;
//   - the empty state, the "unexpected status" alert and any per-screen
//     affordance. Those are the screen's own and stay in `children`, where a
//     reader looking for them still finds them next to the data.
//
// The ids stay each screen's: `{testIdPrefix}-error` on the failure block and
// `{testIdPrefix}-retry` on the button are exactly what the page tests and
// routes.test.tsx already assert, so the migration changed no test.

/** The slice of a TanStack query result this component reads. Structural
 * rather than `UseQueryResult`, so a screen can pass results with different
 * data types in one array — every screen with more than one query (the
 * operations screen has three) needs exactly that. */
export type QueryStateQuery = {
  isPending: boolean;
  isError: boolean;
  error: unknown;
  refetch: () => unknown;
};

export function QueryState({
  queries,
  testIdPrefix,
  onRetry,
  children,
}: {
  /** Pending wins over failed, and the FIRST failure is the one described:
   * a screen whose three queries all fail shows one alert, not three. */
  queries: QueryStateQuery[];
  testIdPrefix: string;
  /** Defaults to refetching every query passed in. A screen overrides it
   * where retrying means more than that. */
  onRetry?: () => void;
  children: ReactNode;
}): ReactElement {
  if (queries.some((q) => q.isPending)) {
    return (
      // role on the Text, not the Group: the live region should be the
      // sentence a screen reader announces, not the flex box around it.
      <Group gap="xs">
        <Loader size="sm" />
        <Text size="sm" component="output">
          Загрузка…
        </Text>
      </Group>
    );
  }
  const failed = queries.find((q) => q.isError);
  if (failed !== undefined) {
    return (
      <Stack gap="sm" data-testid={`${testIdPrefix}-error`}>
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailure(failed.error)}
        </Alert>
        <Button
          variant="default"
          w="fit-content"
          onClick={() => {
            if (onRetry) {
              onRetry();
              return;
            }
            for (const q of queries) {
              void q.refetch();
            }
          }}
          data-testid={`${testIdPrefix}-retry`}
        >
          Повторить
        </Button>
      </Stack>
    );
  }
  return <>{children}</>;
}
