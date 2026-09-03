import { Alert, Button, Container, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";

// RouteErrorFallback is what the router renders when a route's own load or
// render threw something no screen caught. It deliberately shows the raw
// message: by the time a fallback this generic is reached, the failure is a
// bug in this app rather than a server answer any Russian copy could describe
// honestly.
export function RouteErrorFallback({ error }: { error: Error }) {
  return (
    <Container size="sm" py="xl">
      <Stack gap="md">
        <Alert
          color="red"
          icon={<IconAlertTriangle size={18} />}
          role="alert"
          title="Что-то сломалось"
        >
          <Text size="sm">{error.message}</Text>
        </Alert>
        <Button component={Link} to="/" w="fit-content" variant="default">
          На главную
        </Button>
      </Stack>
    </Container>
  );
}

// NotFoundFallback covers a URL this app has no route for. The SPA fallback in
// internal/webui means the SERVER answers 200 with this very app for any
// unknown GET, so this component is the only thing that ever tells a person
// their path was wrong.
export function NotFoundFallback() {
  return (
    <Container size="sm" py="xl">
      <Stack gap="md">
        <Title order={1}>Такой страницы нет</Title>
        <Button component={Link} to="/" w="fit-content" variant="default">
          На главную
        </Button>
      </Stack>
    </Container>
  );
}
