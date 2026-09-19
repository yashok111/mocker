import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Button,
  Code,
  Group,
  NativeSelect,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { IconPlayerPlay } from "@tabler/icons-react";
import { requestMock } from "@/api/client";
import type { MockResponse } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";

type MockTarget = "draft" | "published";

export function MockTester({
  draftUrl,
  publishedUrl,
  published,
  initialMethod = "GET",
  initialPath = "/",
}: {
  draftUrl: string;
  publishedUrl: string;
  published: boolean;
  initialMethod?: string;
  initialPath?: string;
}): ReactElement {
  const [target, setTarget] = useState<MockTarget>("draft");
  const [method, setMethod] = useState(initialMethod.toUpperCase());
  const [path, setPath] = useState(initialPath);
  const [query, setQuery] = useState("");
  const [headers, setHeaders] = useState("");
  const [body, setBody] = useState("");
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<MockResponse | null>(null);
  const [error, setError] = useState<unknown>(null);

  const unavailable = target === "published" && !published;

  async function invoke(): Promise<void> {
    if (unavailable) return;
    setPending(true);
    setError(null);
    setResult(null);
    try {
      const base = target === "draft" ? draftUrl : publishedUrl;
      const normalizedPath = path.startsWith("/") ? path : `/${path}`;
      const suffix = query.trim() === "" ? "" : `?${query.replace(/^\?/, "")}`;
      const requestHeaders = new Headers();
      for (const line of headers.split("\n")) {
        const colon = line.indexOf(":");
        if (colon > 0)
          requestHeaders.set(line.slice(0, colon).trim(), line.slice(colon + 1).trim());
      }
      const response = await requestMock(`${base.replace(/\/$/, "")}${normalizedPath}${suffix}`, {
        method,
        headers: requestHeaders,
        body: method === "GET" || method === "HEAD" || body === "" ? undefined : body,
      });
      setResult(response);
    } catch (cause) {
      setError(cause);
    } finally {
      setPending(false);
    }
  }

  return (
    <Stack gap="sm">
      <SegmentedControl
        fullWidth
        value={target}
        onChange={(value) => setTarget(value as MockTarget)}
        data={[
          { value: "draft", label: "Черновик" },
          { value: "published", label: "Публикация" },
        ]}
        aria-label="Мок для вызова"
      />
      {unavailable ? (
        <Alert color="yellow">Опубликованный мок появится после первого выпуска.</Alert>
      ) : null}
      <Group grow align="flex-end" wrap="nowrap">
        <NativeSelect
          label="Метод"
          value={method}
          onChange={(event) => setMethod(event.currentTarget.value)}
          data={["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]}
          w={100}
        />
        <TextInput
          label="Путь"
          value={path}
          onChange={(event) => setPath(event.currentTarget.value)}
        />
      </Group>
      <TextInput
        label="Query-параметры"
        placeholder="status=active&limit=10"
        value={query}
        onChange={(event) => setQuery(event.currentTarget.value)}
      />
      <Textarea
        label="Заголовки"
        description="По одному Name: value на строку"
        minRows={2}
        value={headers}
        onChange={(event) => setHeaders(event.currentTarget.value)}
      />
      {method !== "GET" && method !== "HEAD" ? (
        <Textarea
          label="Тело запроса"
          minRows={4}
          value={body}
          onChange={(event) => setBody(event.currentTarget.value)}
        />
      ) : null}
      <Button
        variant="light"
        leftSection={<IconPlayerPlay size={16} />}
        disabled={unavailable || path.trim() === ""}
        loading={pending}
        onClick={() => void invoke()}
      >
        Вызвать мок
      </Button>
      {error !== null ? (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(error)}
        </Alert>
      ) : null}
      {result !== null ? (
        <Stack gap={5} aria-live="polite">
          <Text size="xs" fw={650}>
            Ответ · HTTP {result.status}
          </Text>
          <Code block>{formatMockBody(result)}</Code>
        </Stack>
      ) : null}
    </Stack>
  );
}

function formatMockBody(result: MockResponse): string {
  if (result.body === "") return "(пустое тело)";
  if (!result.headers.get("Content-Type")?.includes("json")) return result.body;
  try {
    return JSON.stringify(JSON.parse(result.body), null, 2);
  } catch {
    return result.body;
  }
}
