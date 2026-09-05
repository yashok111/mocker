import {
  Alert,
  Box,
  Button,
  Card,
  PasswordInput,
  Stack,
  TextInput,
  Title,
  Text,
} from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import { useLogin } from "@/api/generated/auth/auth.ts";
import { ApiFailure, setCsrfToken } from "@/api/client";
import { clientUnknownErrorCode, describeErrorCode } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import { userName } from "@/validation/name";

// LoginPage is DESIGN §14 screen 1: password + name, вход = get-or-create by
// name. It owns only the form and the copy shown for a failed attempt — the
// route guard decides when this screen is even mounted.

const loginForm = type({
  name: userName,
  password: "string",
});

type LoginForm = typeof loginForm.infer;

// describeFailure turns a thrown login error into the exact copy the design
// specifies. The server answers the SAME 401 for a wrong password and an
// unknown name on purpose (a different message would leak which one was
// right), so this never tries to tell them apart either.
function describeFailure(err: unknown): string {
  if (!(err instanceof ApiFailure)) {
    // fetch() itself rejected — offline, DNS, CORS — so there is no status
    // to report.
    return "Сервер не ответил";
  }
  if (err.status === 401) {
    return "Неверный пароль или имя";
  }
  if (err.status === 429) {
    return "Слишком много попыток. Подождите минуту";
  }
  if (err.code === clientUnknownErrorCode) {
    return `Сервер не ответил (${err.status})`;
  }
  return describeErrorCode(err.code);
}

export function LoginPage({ onSuccess }: { onSuccess: () => void }) {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<LoginForm>({
    resolver: arktypeResolver(loginForm),
    defaultValues: { name: "", password: "" },
  });

  const login = useLogin({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        // Armed here as well as in ensureSession: the redirect below reads
        // the session from cache, and a mutation that had to wait for the
        // next guarded navigation to arm the token would leave the first
        // state-changing call after login without a header.
        setCsrfToken(res.data.csrfToken);
        onSuccess();
      },
    },
  });

  const failureMessage = login.isError ? describeFailure(login.error) : null;

  return (
    <Box
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "var(--mantine-spacing-md)",
      }}
    >
      <Card
        component="form"
        withBorder
        shadow="sm"
        p="lg"
        w="100%"
        maw={380}
        data-testid="login-form"
        onSubmit={handleSubmit((values) =>
          login.mutate({ data: { name: values.name.trim(), password: values.password } }),
        )}
      >
        <Stack gap="md">
          <Title order={1}>Вход</Title>
          {failureMessage !== null ? (
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {failureMessage}
            </Alert>
          ) : null}
          <TextInput
            label="Имя"
            autoComplete="username"
            data-testid="login-name"
            error={errors.name?.message}
            {...register("name")}
          />
          <PasswordInput
            label="Пароль"
            autoComplete="current-password"
            data-testid="login-password"
            error={errors.password?.message}
            {...register("password")}
          />
          <Text size="xs" c="dimmed" data-testid="login-hint">
            Имя — любое: оно подписывает ваши правки в истории и создаётся при первом входе. Пароль
            один на всю установку; его выдаёт тот, кто её поднял.
          </Text>
          <Button type="submit" fullWidth loading={login.isPending} data-testid="login-submit">
            {login.isPending ? "Входим…" : "Войти"}
          </Button>
        </Stack>
      </Card>
    </Box>
  );
}
