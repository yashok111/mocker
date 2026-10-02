import { useContext } from "react";
import { BackendAPIArtifactsContext } from "./BackendAPIArtifacts";

// Nested synchronous close/select callbacks share one accepted departure.
const accepted = new WeakSet<object>();
export function useBackendAPIDeparture() {
  const host = useContext(BackendAPIArtifactsContext);
  return (action: () => void, check?: () => boolean): boolean => {
    if (host && accepted.has(host)) {
      action();
      return true;
    }
    const allowed = check ? check() : !host?.guard || host.guard();
    if (!allowed) return false;
    if (!host) {
      action();
      return true;
    }
    accepted.add(host);
    try {
      action();
      return true;
    } finally {
      accepted.delete(host);
    }
  };
}
