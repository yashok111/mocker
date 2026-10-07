import { useEffect, useLayoutEffect, useRef } from "react";

// Opening the inspector immediately resizes/clips the canvas. Wait for the
// second pointer click so right-side cards remain under the pointer throughout.
export function createCardClicks(select: (id: string) => void, enter: (id: string) => void) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const cancel = () => {
    clearTimeout(timer);
    timer = undefined;
  };
  return {
    click(id: string, detail: number) {
      cancel();
      if (detail > 1) return;
      if (detail === 0) select(id);
      else
        timer = setTimeout(() => {
          timer = undefined;
          select(id);
        }, 350);
    },
    doubleClick(id: string) {
      cancel();
      enter(id);
    },
    cancel,
  };
}

export function useCardClicks(
  select: (id: string) => void,
  enter: (id: string) => void,
  scope: unknown,
) {
  const callbacks = useRef({ select, enter });
  useEffect(() => {
    callbacks.current = { select, enter };
  }, [select, enter]);
  const gestures = useRef<ReturnType<typeof createCardClicks> | undefined>(undefined);
  useLayoutEffect(() => {
    const clicks = createCardClicks(
      (id) => callbacks.current.select(id),
      (id) => callbacks.current.enter(id),
    );
    gestures.current = clicks;
    return () => {
      clicks.cancel();
      gestures.current = undefined;
    };
  }, [scope]);
  return {
    click: (id: string, detail: number) => gestures.current?.click(id, detail),
    doubleClick: (id: string) => gestures.current?.doubleClick(id),
    cancel: () => gestures.current?.cancel(),
  };
}
