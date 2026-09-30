import { Button, Group, NativeSelect, Stack, Text, Textarea, TextInput } from "@mantine/core";
import type { ResponseRuleEntityFixture } from "@/api/generated/schemas";

export default function EntityFixtureEditor({
  value,
  onChange,
}: {
  value: ResponseRuleEntityFixture[];
  onChange: (entities: ResponseRuleEntityFixture[]) => void;
}) {
  function edit(index: number, entity: ResponseRuleEntityFixture) {
    onChange(value.map((item, i) => (i === index ? entity : item)));
  }
  return (
    <Stack gap="sm" aria-label="Сущности для симуляции">
      <Text fw={600}>Сущности для симуляции</Text>
      <Text size="xs" c="dimmed">
        Каждый запуск начинает с этих записей и меняет только пример. Данные рабочего пространства
        остаются вне симуляции.
      </Text>
      {value.map((entity, index) => (
        <Stack gap="xs" key={index}>
          <TextInput
            label={`Семейство примера ${index + 1}`}
            placeholder="/orders"
            value={entity.family}
            onChange={(event) => edit(index, { ...entity, family: event.currentTarget.value })}
          />
          <TextInput
            label={`Поле ID: семейство ${index + 1}`}
            value={entity.idField}
            onChange={(event) => edit(index, { ...entity, idField: event.currentTarget.value })}
          />
          <NativeSelect
            label={`Тип ID: семейство ${index + 1}`}
            value={entity.idType}
            data={[
              { value: "integer", label: "Целое число" },
              { value: "string", label: "Строка" },
            ]}
            onChange={(event) =>
              edit(index, {
                ...entity,
                idType: event.currentTarget.value as ResponseRuleEntityFixture["idType"],
              })
            }
          />
          {entity.rows.map((row, rowIndex) => {
            const label = `${index + 1}.${rowIndex + 1}`;
            function change(next: typeof row) {
              edit(index, {
                ...entity,
                rows: entity.rows.map((item, i) => (i === rowIndex ? next : item)),
              });
            }
            return (
              <Stack key={rowIndex} gap="xs">
                <TextInput
                  label={`Ключ записи ${label}`}
                  value={row.key}
                  onChange={(event) => change({ ...row, key: event.currentTarget.value })}
                />
                <Text size="xs" c="dimmed">
                  Область записи · {row.scope.length ? "значения родителей по порядку" : "корневая"}
                </Text>
                {row.scope.map((parent, parentIndex) => (
                  <TextInput
                    key={parentIndex}
                    label={`Родитель ${parentIndex + 1}: запись ${label}`}
                    value={parent}
                    onChange={(event) =>
                      change({
                        ...row,
                        scope: row.scope.map((item, i) =>
                          i === parentIndex ? event.currentTarget.value : item,
                        ),
                      })
                    }
                  />
                ))}
                <Group gap="xs">
                  <Button
                    size="xs"
                    variant="default"
                    disabled={row.scope.length >= 3}
                    onClick={() => change({ ...row, scope: [...row.scope, ""] })}
                  >
                    Добавить родителя: запись {label}
                  </Button>
                  {row.scope.length > 0 && (
                    <Button
                      size="xs"
                      variant="subtle"
                      onClick={() => change({ ...row, scope: row.scope.slice(0, -1) })}
                    >
                      Убрать родителя: запись {label}
                    </Button>
                  )}
                </Group>
                <Textarea
                  label={`JSON записи ${label}`}
                  autosize
                  minRows={3}
                  maxRows={8}
                  value={row.dataJSON}
                  onChange={(event) => change({ ...row, dataJSON: event.currentTarget.value })}
                />
                <Group>
                  <Button
                    size="xs"
                    color="red"
                    variant="subtle"
                    onClick={() =>
                      edit(index, { ...entity, rows: entity.rows.filter((_, i) => i !== rowIndex) })
                    }
                  >
                    Удалить запись {label}
                  </Button>
                </Group>
              </Stack>
            );
          })}
          <Group gap="xs">
            <Button
              size="xs"
              variant="default"
              disabled={entity.rows.length >= 100}
              onClick={() =>
                edit(index, {
                  ...entity,
                  rows: [...entity.rows, { key: "", scope: [], dataJSON: "{}" }],
                })
              }
            >
              Добавить запись: семейство {index + 1}
            </Button>
            <Button
              size="xs"
              color="red"
              variant="subtle"
              onClick={() => onChange(value.filter((_, i) => i !== index))}
            >
              Удалить семейство {index + 1}
            </Button>
          </Group>
        </Stack>
      ))}
      <Group>
        <Button
          size="xs"
          variant="default"
          disabled={value.length >= 100}
          onClick={() =>
            onChange([...value, { family: "", idField: "id", idType: "integer", rows: [] }])
          }
        >
          Добавить семейство примера
        </Button>
      </Group>
    </Stack>
  );
}
