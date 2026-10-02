import { createFileRoute } from "@tanstack/react-router";
import { type } from "arktype";
import { ApiDesignerWorkbench } from "@/components/api-designer/ApiDesignerWorkbench";
import { NotFoundFallback } from "@/components/ErrorFallback";

const designSearch = type({
  "reviewId?": "string | number",
  "pinnedRevisionId?": "string",
  "pinnedHash?": "string",
  "pinnedObjectKey?": "string",
  "pinnedPointer?": "string",
  "pinnedSelectorPointer?": "string",
  "returnProjectId?": "string",
  "returnRevisionId?": "string",
  "returnSourceNodeId?": "string",
});
export const validateDesignSearch = designSearch.assert;

export const Route = createFileRoute("/_authed/designs/$id")({
  parseParams: ({ id }) => ({ id: Number(id) }),
  validateSearch: validateDesignSearch,
  component: DesignRoute,
});

function DesignRoute() {
  const { id } = Route.useParams();
  const { reviewId, ...pinnedAPI } = Route.useSearch();
  if (!Number.isSafeInteger(id) || id <= 0) return <NotFoundFallback />;
  const parsedReview = reviewId === undefined ? undefined : Number(reviewId);
  return (
    <ApiDesignerWorkbench
      id={id}
      pinnedAPI={pinnedAPI.pinnedRevisionId ? pinnedAPI : undefined}
      reviewId={
        parsedReview !== undefined && Number.isSafeInteger(parsedReview) && parsedReview > 0
          ? parsedReview
          : undefined
      }
    />
  );
}
