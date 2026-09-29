import { describe, expect, it } from "vitest";
import { impactGraphLabel } from "./graphLabels";

describe("impactGraphLabel", () => {
  it.each([
    ["/components/schemas/Order/properties/status", "Order\nПоле status"],
    ["/components/schemas/CreateOrder/required", "CreateOrder\nОбязательные поля"],
    ["/components/schemas/Order", "Order"],
    [
      "/components/schemas/Order/properties/customer/properties/status",
      "Order\nПоле customer.status",
    ],
    [
      "/components/schemas/Order/properties/items/items/properties/status",
      "Order\nПоле items[].status",
    ],
    ["/components/schemas/Order~1Item/properties/a~01b", "Order/Item\nПоле a~1b"],
    [
      "/paths/~1orders/post/responses/201/content/application~1json/schema/$ref",
      "POST /orders\nОтвет 201",
    ],
    [
      "/paths/~1orders/post/requestBody/content/application~1json/schema/$ref",
      "POST /orders\nТело запроса",
    ],
    ["/paths/~1orders~1{id}/get", "GET /orders/{id}"],
    ["/paths/~1orders/get/parameters/0/schema", "GET /orders\nПараметр 1"],
    ["/components/responses/NotFound/content/application~1json/schema", "NotFound\nОтвет"],
    ["/components/requestBodies/NewOrder", "NewOrder\nТело запроса"],
    ["/messages/0/operation", "Шаг 1\nПривязка операции"],
    ["/messages/2/operation/operationKey", "Шаг 3\nПривязка операции"],
  ])("summarizes %s without displaying structural JSON segments", (pointer, expected) => {
    expect(impactGraphLabel(pointer)).toBe(expected);
  });

  it.each([
    "Order",
    "GET /orders",
    "/orders",
    "Заказ покупателя",
    "",
    "/custom/a~1b/detail",
    "/components/unknown/Order",
    "/components/constructor/Order",
  ])("preserves names and unrecognized pointers: %s", (label) => {
    expect(impactGraphLabel(label)).toBe(label);
  });
});
