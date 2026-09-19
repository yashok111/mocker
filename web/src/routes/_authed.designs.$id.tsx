import { createFileRoute } from "@tanstack/react-router";
import { type } from "arktype";
import { ApiDesignerWorkbench } from "@/components/api-designer/ApiDesignerWorkbench";
import { NotFoundFallback } from "@/components/ErrorFallback";

const designSearch = type({
  "reviewId?": "string | number",
});

export const Route = createFileRoute("/_authed/designs/$id")({
  parseParams: ({ id }) => ({ id: Number(id) }),
  validateSearch: designSearch.assert,
  component: DesignRoute,
});

function DesignRoute() {
  const { id } = Route.useParams();
  const { reviewId } = Route.useSearch();
  if (!Number.isInteger(id) || id <= 0) return <NotFoundFallback />;
  const parsedReview = reviewId === undefined ? undefined : Number(reviewId);
  return (
    <ApiDesignerWorkbench
      id={id}
      reviewId={
        parsedReview !== undefined && Number.isInteger(parsedReview) && parsedReview > 0
          ? parsedReview
          : undefined
      }
    />
  );
}
