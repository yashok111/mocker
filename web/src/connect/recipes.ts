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

// buildRecipes takes ws.url as the server sent it (never rebuilt from
// window.location — see the phase brief: the admin host is forbidden from
// sitting under the base domain, and the port comes from the request) and
// config.reservedPrefix, which is a runtime setting, never the literal
// "/__mocker" (the acceptance stack deliberately runs with a non-default
// value).
export function buildRecipes(ws: WorkspaceView, config: ServerConfigView): Recipe[] {
  return [
    {
      id: "env",
      title: "Переменная окружения",
      snippet: `API_BASE_URL=${ws.url}`,
      note: "Работает всегда — если фронтенд читает адрес API из переменной окружения.",
    },
    {
      id: "apiBase",
      title: "Параметр ?apiBase=",
      snippet: `?apiBase=${ws.url}`,
      note: "Сработает, только если фронтенд сам умеет читать apiBase из адресной строки — это разовая доработка на его стороне.",
    },
    {
      id: "devtools",
      title: "Подмена в devtools",
      snippet: ws.url,
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
