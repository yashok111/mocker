import { createContext, useContext, useState, type ReactNode } from "react";
import type {
  BackendDiagramPin,
  BackendDiagramRef,
  BackendDiagramTarget,
} from "@/api/generated/schemas";
export type DiagramChangeHandoff = {
  projectId: string;
  diagram: BackendDiagramPin;
  target: BackendDiagramTarget;
  refs: BackendDiagramRef[];
};
const Context = createContext<
  | { handoff?: DiagramChangeHandoff; setHandoff: (handoff: DiagramChangeHandoff) => void }
  | undefined
>(undefined);
export function BackendDiagramChangeProvider({ children }: { children: ReactNode }) {
  const [handoff, setHandoff] = useState<DiagramChangeHandoff>();
  return <Context.Provider value={{ handoff, setHandoff }}>{children}</Context.Provider>;
}
export const useDiagramChangeHandoff = () => useContext(Context);
