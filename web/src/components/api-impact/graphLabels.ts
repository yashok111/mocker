export function impactGraphLabel(label: string): string {
  if (!label.startsWith("/")) return label;
  const parts = label
    .slice(1)
    .split("/")
    .map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"));
  const [root, group, name, ...tail] = parts;

  if (root === "components" && group && name) {
    if (group === "schemas") {
      const fields: string[] = [];
      for (let index = 0; index < tail.length; index++) {
        if (tail[index] === "properties" && tail[index + 1]) {
          fields.push(tail[++index]!);
        } else if (tail[index] === "items" && fields.length > 0) {
          fields[fields.length - 1] += "[]";
        }
      }
      if (fields.length > 0) return `${name}\nПоле ${fields.join(".")}`;
      if (tail[0] === "required") return `${name}\nОбязательные поля`;
      return name;
    }
    const componentKinds: Record<string, string> = {
      responses: "Ответ",
      requestBodies: "Тело запроса",
      parameters: "Параметр",
      headers: "Заголовок",
      pathItems: "Общий путь",
      callbacks: "Callback",
      links: "Связь",
      securitySchemes: "Авторизация",
      examples: "Пример",
    };
    if (Object.hasOwn(componentKinds, group)) return `${name}\n${componentKinds[group]}`;
  }

  if (
    root === "paths" &&
    group &&
    name &&
    /^(get|put|post|delete|options|head|patch|trace)$/.test(name)
  ) {
    const operation = `${name.toUpperCase()} ${group}`;
    if (tail[0] === "responses" && tail[1]) return `${operation}\nОтвет ${tail[1]}`;
    if (tail[0] === "requestBody") return `${operation}\nТело запроса`;
    if (tail[0] === "parameters" && /^\d+$/.test(tail[1] ?? "")) {
      return `${operation}\nПараметр ${Number(tail[1]) + 1}`;
    }
    return operation;
  }

  if (root === "messages" && /^\d+$/.test(group ?? "") && name === "operation") {
    return `Шаг ${Number(group) + 1}\nПривязка операции`;
  }
  return label;
}
