import type { Graph } from "@antv/x6";
import type { RefObject } from "react";
import { Button, Group } from "@mantine/core";
import styles from "./DiagramViewport.module.css";

type Props = {
  hostRef: RefObject<HTMLElement | null>;
  graphRef: RefObject<Graph | null>;
  className?: string;
  ariaLabel?: string;
  ariaHidden?: boolean;
  zoomLabel: string;
  zoomStep?: number;
  fitPadding?: number;
  fitLabel?: string;
};

export default function DiagramViewport({
  hostRef,
  graphRef,
  className,
  ariaLabel,
  ariaHidden,
  zoomLabel,
  zoomStep = 0.15,
  fitPadding = 32,
  fitLabel = "Вместить",
}: Props) {
  return (
    <div className={styles.shell}>
      <figure
        ref={hostRef}
        className={[styles.host, className].filter(Boolean).join(" ")}
        aria-label={ariaLabel}
        aria-hidden={ariaHidden}
      />
      <Group className={styles.controls} gap={6} wrap="nowrap">
        <Button
          size="compact-sm"
          variant="default"
          aria-label={`Уменьшить ${zoomLabel}`}
          onClick={() => graphRef.current?.zoom(-zoomStep)}
        >
          −
        </Button>
        <Button
          size="compact-sm"
          variant="default"
          aria-label={`Увеличить ${zoomLabel}`}
          onClick={() => graphRef.current?.zoom(zoomStep)}
        >
          +
        </Button>
        <Button
          size="compact-sm"
          variant="default"
          onClick={() => graphRef.current?.zoomToFit({ padding: fitPadding, maxScale: 1 })}
        >
          {fitLabel}
        </Button>
      </Group>
    </div>
  );
}
