// recipes.ts holds the four connection recipes DESIGN §14 screen 4 lists,
// as plain data: no clipboard, no DOM, no React import. Keeping the "what
// to copy and what to say about it" question separate from "how a copy
// button works" is what lets buildRecipes be tested with a plain object in
// and a plain array out.
import type { ServerConfigView, WorkspaceView } from "@/api/generated/schemas";

export type Recipe = {
  id: "env" | "apiBase" | "devtools" | "curl";
  title: string;
  snippet: string;
  note: string;
};

// apiAddress is what a frontend's API base must be set to: the origin the
// server sent PLUS the workspace's basePath. WorkspaceView.url carries "NO
// base path" by contract, and three of five readers of the 2026-09-05 UI
// review found the same thing — a workspace with basePath "/api/v1" handed
// the operator an address every request 404s on, and nothing on the panel
// said why. A basePath with a `{param}` stays as the template: the value
// is the frontend's to choose (settings.basePathValues declares which).
export function apiAddress(ws: WorkspaceView): string {
  return `${ws.url}${ws.settings.basePath}`;
}

// buildRecipes takes ws.url as the server sent it (never rebuilt from
// window.location — see the phase brief: the admin host is forbidden from
// sitting under the base domain, and the port comes from the request) and
// config.reservedPrefix, which is a runtime setting, never the literal
// "/__mocker" (the acceptance stack deliberately runs with a non-default
// value). The three API recipes carry the base path (apiAddress); the
// health curl does not — the control routes sit at the origin's root
// (internal/mockplane/plane.go, cutReservedPrefix before any base path).
export function buildRecipes(ws: WorkspaceView, config: ServerConfigView): Recipe[] {
  const api = apiAddress(ws);
  return [
    {
      id: "env",
      title: "Переменная окружения",
      snippet: `API_BASE_URL=${api}`,
      note: "Работает всегда — если фронтенд читает адрес API из переменной окружения.",
    },
    {
      id: "apiBase",
      title: "Параметр ?apiBase=",
      snippet: `?apiBase=${api}`,
      note: "Сработает, только если фронтенд сам умеет читать apiBase из адресной строки — это разовая доработка на его стороне.",
    },
    {
      id: "devtools",
      title: "Подмена в devtools",
      snippet: api,
      note: "Вставьте как local override или правило прокси в браузере — без изменений в коде фронтенда.",
    },
    {
      id: "curl",
      title: "curl",
      snippet: `curl ${ws.url}${config.reservedPrefix}/health`,
      note: "Можно вставить в терминал как есть, чтобы проверить мок из командной строки.",
    },
  ];
}
