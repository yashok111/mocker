import { Tooltip } from "@mantine/core";
import styles from "./SequenceGraph.module.css";

export interface TooltipBounds {
  left: number;
  top: number;
  width: number;
  height: number;
}

export function ObjectDescriptionTooltip({
  bounds,
  description,
}: {
  bounds: TooltipBounds;
  description: string;
}) {
  if (!description.trim()) return null;
  return (
    <Tooltip
      opened
      label={description.trim()}
      events={{ hover: false, focus: false, touch: false }}
      multiline
      maw={320}
      withArrow
      withinPortal
      position="bottom"
      floatingStrategy="fixed"
      middlewares={{ flip: true, shift: { padding: 8 } }}
      transitionProps={{ duration: 0 }}
      classNames={{ tooltip: styles.descriptionTooltip }}
    >
      <span aria-hidden="true" className={styles.tooltipAnchor} style={bounds} />
    </Tooltip>
  );
}
