export function downloadScenarioBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.style.display = "none";
  document.body.append(anchor);
  try {
    anchor.click();
  } finally {
    anchor.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }
}

export function downloadScenarioArtifact(artifact: {
  content: string;
  filename: string;
  mediaType: string;
}): void {
  downloadScenarioBlob(
    new Blob([artifact.content], { type: artifact.mediaType }),
    artifact.filename,
  );
}
