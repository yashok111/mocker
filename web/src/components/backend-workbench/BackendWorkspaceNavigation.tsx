import { Button, Group } from "@mantine/core";
export function focusWorkspaceRegion(selector: string) {
  requestAnimationFrame(() => {
    const target = document.querySelector<HTMLElement>(selector);
    let parent: HTMLElement | null = target ?? null;
    while (parent) { if (parent instanceof HTMLDetailsElement) parent.open = true; parent = parent.parentElement; }
    target?.scrollIntoView?.({ block: "start" });
    target?.focus();
  });
}
export function BackendWorkspaceNavigation() {
  return (
    <Group component="nav" aria-label="Разделы Backend Workbench" wrap="wrap">
<Button variant="default" onClick={() => focusWorkspaceRegion('[data-testid="backend-observations"] h2')}>Наблюдения</Button>
<Button variant="default" onClick={() => focusWorkspaceRegion('[data-testid="backend-replay"] h2')}>Orders replay</Button>
<Button variant="default" onClick={() => focusWorkspaceRegion('[data-testid="backend-portable"] h2')}>Перенос проекта</Button>
      <Button
        variant="default"
        onClick={() => focusWorkspaceRegion('[data-testid="backend-materialization"] h2')}
      >
        Материализация
      </Button>
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
        onClick={() => focusWorkspaceRegion('[data-testid="backend-lifecycle"] h3')}
      >
        Жизненный цикл
      </Button>
      <Button
        variant="default"
        onClick={() => focusWorkspaceRegion('[data-testid="backend-business-map"] h3')}
      >
        Бизнес-события
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
