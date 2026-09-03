import type { EditConflictTombstone } from "@/api/generated/schemas";
import { ApiFailure } from "./client";

// errors.ts is the one place a server-issued error code becomes the Russian
// copy DESIGN §14 asks for. A screen calls describeApiFailure instead of ever
// handing err.message — meant for mocker's own logs, not for the person using
// the mock — straight into a Russian Alert.
//
// The literals mirror the `code` enumeration in api/openapi.json (which in
// turn mirrors internal/httpx's Code* constants). A code added there needs a
// translation added here before it can reach a screen; the default branch is
// what keeps an unknown one from falling through to English.
export function describeErrorCode(code: string): string {
  switch (code) {
    case "bad_request":
      return "Некорректные данные";
    case "unauthorized":
      return "Сессия истекла. Войдите снова";
    case "forbidden":
      return "Нет доступа";
    case "not_found":
      return "Не найдено";
    case "conflict":
      return "Такое значение уже используется";
    case "too_large":
      return "Запрос слишком большой";
    case "rate_limited":
      return "Слишком много попыток. Подождите минуту";
    case "unsupported_media_type":
      return "Сервер не принял формат запроса";
    case "service_unavailable":
      return "Эта возможность сейчас недоступна";
    case "not_implemented_yet":
      return "Эта возможность ещё не реализована";
    case "internal":
      return "Ошибка на сервере. Попробуйте ещё раз";
    // A3/D6: another write landed between this caller's read and this write.
    // `details` (ApiFailure.details, client.ts) carries what the caller
    // needs to retry — the per-screen affordance is built from that, not
    // from this string; this case only keeps the summary line off the
    // generic "Что-то пошло не так" default (property 7).
    case "edit_conflict":
      return "Кто-то другой изменил это, пока вы редактировали";
    // The five codes below are POST /api/workspaces/{id}/preview's own
    // taxonomy (D6 rows 1-5, P2f) — admin-local strings that live at the
    // handler's own call site rather than in internal/httpx (N9), exactly
    // like unsupported_media_type/rate_limited/service_unavailable already
    // do above.
    case "invalid_draft":
      return "Черновик не проходит те же проверки, что и сохранение";
    case "no_spec":
      return "У воркспейса нет привязанной спеки";
    case "operation_not_found":
      return "Такой операции нет в спеке";
    case "custom_endpoint_wins":
      return "Этот путь уже перекрыт кастомным endpoint'ом";
    case "missing_path_param":
      return "Не заполнен обязательный параметр пути";
    default:
      return "Что-то пошло не так";
  }
}

// describePreviewRefusalReason translates D5's four `refused.reason`
// strings — the preview panel's own taxonomy, riding inside a 200 rather
// than as an error code (N9 is explicit that these are NOT httpx error
// codes and describeErrorCode must not grow cases for them). Kept as its
// own function, not folded into describeErrorCode, so a reviewer reading
// either one sees a closed, four-case switch rather than nine codes from
// two unrelated vocabularies mixed into one.
export function describePreviewRefusalReason(reason: string): string {
  switch (reason) {
    case "browser_executable_media_type":
      return "Тип содержимого исполняется браузером — такой ответ нельзя показать";
    case "pinned_body_too_large":
      return "Закреплённое тело больше текущего MOCKER_MAX_RESPONSE";
    case "pinned_body_undecodable":
      return "Закреплённое тело не удалось декодировать";
    case "generation_failed":
      return "Генератор не смог построить тело";
    default:
      return "Тело не построено по неизвестной причине";
  }
}

// clientUnknownErrorCode is client.ts's own synthesized code for a response it
// could not parse as { error: { code, message } }. Exported from here rather
// than from client.ts so the three screens that branch on it stop each keeping
// their own copy of the literal.
export const clientUnknownErrorCode = "client_unknown_error";

/**
 * describeApiFailure is what every screen shows for a failed call. Shared
 * rather than re-derived per screen: a real server rejection (a slug
 * collision, a workspace already gone from a lost race with another tab)
 * must read the same way wherever it surfaces.
 */
export function describeApiFailure(err: unknown): string {
  if (!(err instanceof ApiFailure)) {
    // fetch() itself rejected — offline, DNS, CORS — so there is no status
    // and no server message to report.
    return "Сервер не ответил";
  }
  if (err.code === clientUnknownErrorCode) {
    return `Сервер не ответил (${err.status})`;
  }
  return describeErrorCode(err.code);
}

// isGoneTombstone tells D6/D12's two per-route 409-details shapes apart:
// every single-object route's own ConflictDetails type (OverrideConflictDetails,
// WorkspaceConflictDetails, EndpointConflictDetails, ScenarioConflictDetails)
// never carries `gone`, while EditConflictTombstone always does and always as
// `true` (api/openapi.json declares the union untagged, with no discriminator
// field of its own) — one shared predicate rather than four per-screen copies,
// generic so each screen's own union narrows through it without a cast.
export function isGoneTombstone<T>(
  details: T | EditConflictTombstone,
): details is EditConflictTombstone {
  return (
    typeof details === "object" &&
    details !== null &&
    "gone" in details &&
    (details as { gone?: unknown }).gone === true
  );
}

/**
 * describeApiFailureDetailed exists alongside describeApiFailure, not instead
 * of it, because the two answer different questions. describeApiFailure
 * deliberately never shows err.message: that text is written for mocker's own
 * logs, and a bare "workspace slug already in use" reads fine but a stack
 * trace fragment does not. Several screens in THIS phase genuinely need the
 * server's own sentence, though, because it IS the actionable content rather
 * than incidental detail — spec import says which format is unsupported
 * (§3.2), the override PUT names the offending status and pattern (§3.3). A
 * per-screen re-implementation would fork this decision four times, so it is
 * one shared helper instead: the same Russian summary describeApiFailure
 * already produces, followed by the server's own message when there is one
 * to show.
 */
export function describeApiFailureDetailed(err: unknown): string {
  const summary = describeApiFailure(err);
  if (err instanceof ApiFailure && err.code !== clientUnknownErrorCode && err.message !== "") {
    return `${summary}: ${err.message}`;
  }
  return summary;
}
