import { describe, expect, it } from "vitest";
import { ApiFailure } from "./client";
import {
  clientUnknownErrorCode,
  describeApiFailure,
  describeApiFailureDetailed,
  describeErrorCode,
  isGoneTombstone,
} from "./errors";

describe("describeErrorCode", () => {
  it.each([
    ["bad_request", "Некорректные данные"],
    ["unauthorized", "Сессия истекла. Войдите снова"],
    ["forbidden", "Нет доступа"],
    ["not_found", "Не найдено"],
    ["conflict", "Такое значение уже используется"],
    ["too_large", "Запрос слишком большой"],
    ["rate_limited", "Слишком много попыток. Подождите минуту"],
    ["service_unavailable", "Эта возможность сейчас недоступна"],
    ["not_implemented_yet", "Эта возможность ещё не реализована"],
    ["internal", "Ошибка на сервере. Попробуйте ещё раз"],
    ["edit_conflict", "Кто-то другой изменил это, пока вы редактировали"],
  ])("translates %s", (code, expected) => {
    expect(describeErrorCode(code)).toBe(expected);
  });

  it("falls back to Russian for a code it has never seen", () => {
    // The point of the default branch: an unknown code must not fall through
    // to the server's own English log sentence.
    expect(describeErrorCode("some_future_code")).toBe("Что-то пошло не так");
  });
});

describe("describeApiFailure", () => {
  it("reports a rejected fetch as no answer at all", () => {
    expect(describeApiFailure(new TypeError("Failed to fetch"))).toBe("Сервер не ответил");
  });

  it("names the status when the body could not be read as an error envelope", () => {
    expect(describeApiFailure(new ApiFailure("x", 502, clientUnknownErrorCode))).toBe(
      "Сервер не ответил (502)",
    );
  });

  it("never surfaces the server's own message", () => {
    const failure = new ApiFailure("workspace slug already in use", 409, "conflict");
    expect(describeApiFailure(failure)).toBe("Такое значение уже используется");
  });
});

describe("describeApiFailureDetailed", () => {
  it("appends the server's own message after the Russian summary", () => {
    const failure = new ApiFailure(
      "unsupported format: Swagger 2.0 and YAML input land in a later phase",
      400,
      "bad_request",
    );
    expect(describeApiFailureDetailed(failure)).toBe(
      "Некорректные данные: unsupported format: Swagger 2.0 and YAML input land in a later phase",
    );
  });

  it("falls back to the plain summary for a rejected fetch, with no message to append", () => {
    expect(describeApiFailureDetailed(new TypeError("Failed to fetch"))).toBe("Сервер не ответил");
  });

  it("does not double the status for an unparseable error body", () => {
    const failure = new ApiFailure("request failed with status 502", 502, clientUnknownErrorCode);
    expect(describeApiFailureDetailed(failure)).toBe("Сервер не ответил (502)");
  });
});

describe("isGoneTombstone", () => {
  it("recognizes D6's gone-tombstone shape", () => {
    expect(isGoneTombstone({ gone: true, editVersion: null })).toBe(true);
  });

  it("rejects a real ConflictDetails document, even one that happens to carry other fields", () => {
    // The discriminant is presence-of-`gone`, not shape similarity — a
    // document conflict (WorkspaceConflictDetails-shaped here) must never be
    // mistaken for "the row is gone", or a screen would show "nothing to
    // retry" for a conflict it could actually resolve.
    expect(isGoneTombstone({ name: "x", settings: {}, specId: null, editVersion: 3 })).toBe(false);
  });

  it("rejects a non-object value without throwing", () => {
    expect(isGoneTombstone(null as unknown as { gone: true; editVersion: null })).toBe(false);
  });
});
