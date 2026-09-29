type FitGraph = {
  getNodes(): readonly unknown[];
  zoomToFit(options: { padding: number; maxScale: number }): unknown;
};

export function createInitialFit(
  graph: FitGraph,
  host: Pick<HTMLElement, "clientWidth" | "clientHeight">,
  padding = 32,
) {
  let currentIdentity: string | undefined;
  let fitted = false;
  return (identity = "default") => {
    if (currentIdentity !== identity) {
      currentIdentity = identity;
      fitted = false;
    }
    // Hidden or empty graphs must retain their opportunity to fit once visible.
    if (fitted || !graph.getNodes().length || host.clientWidth <= 0 || host.clientHeight <= 0)
      return false;
    graph.zoomToFit({ padding, maxScale: 1 });
    fitted = true;
    return true;
  };
}
