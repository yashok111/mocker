import { Button, Group } from "@mantine/core";
export function focusWorkspaceRegion(selector: string) {
  requestAnimationFrame(() => {
    const target = document.querySelector<HTMLElement>(selector);
    target?.scrollIntoView?.({ block: "start" });
    target?.focus();
  });
}
export function BackendWorkspaceNavigation() {
  return (
    <Group component="nav" aria-label="Разделы Backend Workbench" wrap="wrap">
      <Button
        variant="default"
        onClick={() => focusWorkspaceRegion('[data-testid="backend-architecture"] h2')}
      >
        Архитектура C4
      </Button>
      <Button
        variant="default"
        onClick={() => focusWorkspaceRegion('[data-testid="backend-interactions"] h3')}
      >
        Взаимодействия
      </Button>
      <Button
        variant="default"
        onClick={() => focusWorkspaceRegion('[data-testid="backend-project-page"]')}
      >
        Инвентарь и отчёты
      </Button>
    </Group>
  );
}
