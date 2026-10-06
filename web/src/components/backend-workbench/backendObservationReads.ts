export function observationPinKey(pin: { setId: string; version: number; contentHash: string }) {
  if (!Number.isSafeInteger(pin.version) || pin.version < 1)
    throw new Error("Версия вне точного диапазона браузера; используйте MCP.");
  return `${pin.setId}/${pin.version}/${pin.contentHash}`;
}
export const observationDisplay = (value: unknown) =>
  value == null ? "Неизвестно" : String(value);
export function observationRequestGate() {
  let generation = 0;
  return { begin: () => ++generation, current: (token: number) => token === generation };
}
