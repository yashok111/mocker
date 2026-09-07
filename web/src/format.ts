import dayjs from "dayjs";

// format.ts holds the three display formatters the screens kept re-deriving.
// It renders Russian operator-facing copy and nothing else: no API types, no
// React, so a screen importing it pulls in nothing but dayjs.

/**
 * formatBytes is the ONE byte formatter. There were two, and they disagreed:
 * AssetsPage's always wrote one decimal ("1.0 МБ") and StreamEditor's dropped
 * it on an exact multiple ("1 МБ"), so the same 1 048 576 read differently on
 * two tabs of the same workspace. StreamEditor's rule won on test coverage —
 * two assertions in StreamEditor.test.tsx against one in AssetsPage.test.tsx —
 * and it is also the better rule for the numbers these screens actually show:
 * the caps (MOCKER_MAX_ASSET_BYTES, the frame cap) are configured as whole
 * mebibytes, and "8.0 МБ" spells a precision the setting does not have.
 */
export function formatBytes(n: number): string {
  if (n >= 1024 * 1024) {
    return `${(n / (1024 * 1024)).toFixed(n % (1024 * 1024) === 0 ? 0 : 1)} МБ`;
  }
  if (n >= 1024) {
    return `${(n / 1024).toFixed(n % 1024 === 0 ? 0 : 1)} КБ`;
  }
  return `${n} Б`;
}

/**
 * formatBytesPerSec is the rate half of the same vocabulary: §30.12's
 * amplifier, the number StreamCapsStrip shows before a loop is saved. It keeps
 * its own body rather than appending "/с" to formatBytes because a rate is
 * measured, not configured — an exactly-1-MiB/s reading is a coincidence, and
 * showing "1 МБ/с" where the next sample reads "1.1 МБ/с" makes the strip
 * look like it is switching precision.
 */
export function formatBytesPerSec(n: number): string {
  if (n >= 1024 * 1024) {
    return `${(n / (1024 * 1024)).toFixed(1)} МБ/с`;
  }
  if (n >= 1024) {
    return `${(n / 1024).toFixed(1)} КБ/с`;
  }
  return `${n} Б/с`;
}

/**
 * formatTimestamp renders the Unix SECONDS every list view on the admin API
 * carries (spec_handlers.go's sp.CreatedAt.Unix(), endpoint_handlers.go's
 * row.CreatedAt/UpdatedAt, the scenario and checkpoint summary views, the
 * asset row) — dayjs needs telling which unit, or it reads 1970 for every row.
 *
 * A21 REVERSES an earlier decision recorded in HistoryPage.tsx: a fourth
 * three-line copy was judged cheaper than a shared util three screens would
 * need retrofitting to. A fifth copy then appeared inline in AssetsPage.tsx
 * without a comment at all, which is exactly the threshold that argument was
 * reasoning about — five undocumented copies of one format string is how the
 * format silently forks. One exported function, five call sites.
 */
export function formatTimestamp(unixSeconds: number): string {
  return dayjs.unix(unixSeconds).format("DD.MM.YYYY HH:mm");
}
