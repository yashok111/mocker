import { lazy, Suspense, type ComponentProps } from "react";
import { Loader } from "@mantine/core";
const Canvas = lazy(() =>
  import("./ExploreCanvas").then((module) => ({ default: module.ExploreCanvas })),
);
export function ExploreCanvas(
  props: ComponentProps<typeof import("./ExploreCanvas").ExploreCanvas>,
) {
  return (
    <Suspense fallback={<Loader size="sm" aria-label="Загружаем диаграмму" />}>
      <Canvas {...props} />
    </Suspense>
  );
}
