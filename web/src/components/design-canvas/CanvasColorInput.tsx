import { useRef, useState, type ReactElement } from "react";
import { ActionIcon, ColorInput, Group, Tooltip } from "@mantine/core";
import { IconRestore } from "@tabler/icons-react";
import { isCanvasColor } from "./canvasColors";

const swatches = [
  "#ffffff",
  "#e9ecef",
  "#d3f9d8",
  "#c3fae8",
  "#d0ebff",
  "#e5dbff",
  "#ffdeeb",
  "#fff3bf",
  "#ffe8cc",
  "#25332f",
  "#087f70",
  "#1971c2",
  "#7048e8",
  "#c92a2a",
];

interface Props {
  label: string;
  value: string | undefined;
  onChange: (color: string | undefined) => void;
}

export function CanvasColorInput({ label, value, onChange }: Props): ReactElement {
  const [input, setInput] = useState({ source: value, draft: value ?? "" });
  const latestDraft = useRef(input.draft);
  // External edits (including undo/redo) replace the buffer; partial typing stays local.
  if (input.source !== value) {
    setInput({ source: value, draft: value ?? "" });
  }
  const resetLabel = `Сбросить ${label.toLocaleLowerCase("ru")}`;
  const changeDraft = (draft: string) => {
    latestDraft.current = draft;
    setInput({ source: value, draft });
  };

  return (
    <Group gap="xs" align="end" wrap="nowrap">
      <ColorInput
        label={label}
        value={input.draft}
        placeholder="По умолчанию"
        format="hex"
        fixOnBlur={false}
        withEyeDropper={false}
        swatches={swatches}
        closeOnColorSwatchClick
        popoverProps={{ withinPortal: true }}
        style={{ flex: 1 }}
        onChange={changeDraft}
        onChangeEnd={(color) => {
          // Mantine also expands partial #abc typing; wait for a complete hex instead.
          if (isCanvasColor(latestDraft.current) && isCanvasColor(color)) {
            onChange(color);
          }
        }}
        onBlur={() => {
          if (input.draft === "") onChange(undefined);
          changeDraft(value ?? "");
        }}
      />
      <Tooltip label={resetLabel}>
        <ActionIcon
          variant="subtle"
          color="gray"
          size="input-sm"
          aria-label={resetLabel}
          disabled={value === undefined && input.draft === ""}
          onClick={() => {
            changeDraft("");
            onChange(undefined);
          }}
        >
          <IconRestore size={16} />
        </ActionIcon>
      </Tooltip>
    </Group>
  );
}
