package designscenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

// Captured from actual B24 owner revisions in E0 (87c654f), before E1 edits.
// These are synthetic local B31 fixtures, independent of this encoder.
const b24ScenarioEnvelopeGoldens = `[{"scenarioId":"1","revisionId":"1","version":"1","contentHash":"ebb1dfaace1d6c37b9f4c13e97c8bf2ae839e9ab77094e43bc1e4f2883a64f1b","documentHash":"f895698bc3f094cef7cf9820d46bfbca692cbda4ad619e20802347f06daaf738","envelope":"{\"document\":{\"formatVersion\":3,\"title\":\"B31 linked and divergent copy frozen scenario\",\"participants\":[{\"id\":\"client\",\"name\":\"Client\",\"kind\":\"client\",\"description\":\"\"},{\"id\":\"orders\",\"name\":\"Orders\",\"kind\":\"service\",\"description\":\"\"},{\"id\":\"queue\",\"name\":\"Orders queue\",\"kind\":\"queue\",\"description\":\"\"}],\"messages\":[{\"id\":\"request-linked\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via linked contract\",\"description\":\"\",\"operation\":{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"request-copy\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via divergent copy\",\"description\":\"\",\"operation\":{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"event-created\",\"fromId\":\"orders\",\"toId\":\"queue\",\"kind\":\"event\",\"label\":\"Order created\",\"description\":\"\",\"eventBindings\":[{\"contractId\":\"orders-events\",\"operationId\":\"emit-created\"}]}],\"fragments\":[{\"id\":\"orders-option\",\"kind\":\"opt\",\"label\":\"Optional authored requests\",\"fromMessageId\":\"request-linked\",\"toMessageId\":\"request-copy\"}],\"contracts\":[{\"id\":\"linked-orders\",\"name\":\"Exact linked orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"integer\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Orders exact API links\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"linked\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}},{\"id\":\"copy-orders\",\"name\":\"Divergent copy orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"string\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Divergent authoritative embedded copy\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"copy\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}}],\"eventModel\":{\"servers\":[{\"id\":\"kafka\",\"name\":\"Declared Kafka\",\"description\":\"\",\"host\":\"inert.invalid:9092\",\"protocol\":\"kafka\",\"auth\":\"none\"}],\"channels\":[{\"id\":\"created\",\"name\":\"Order created\",\"description\":\"\",\"address\":\"orders.created\",\"serverIds\":[\"kafka\"],\"messageIds\":[\"created-message\"]}],\"messages\":[{\"id\":\"created-message\",\"name\":\"Created message\",\"description\":\"\",\"payloadSchemaId\":\"created-schema\",\"examples\":[{\"name\":\"Example\",\"payloadJSON\":\"{\\\"orderId\\\":\\\"example\\\"}\"}]}],\"schemas\":[{\"id\":\"created-schema\",\"name\":\"Created payload\",\"description\":\"\",\"schemaJSON\":\"{\\\"type\\\":\\\"object\\\",\\\"properties\\\":{\\\"orderId\\\":{\\\"type\\\":\\\"string\\\"}}}\"}],\"contracts\":[{\"id\":\"orders-events\",\"name\":\"Orders events\",\"description\":\"\",\"participantId\":\"orders\",\"version\":\"1\",\"operations\":[{\"id\":\"emit-created\",\"name\":\"Emit created\",\"description\":\"\",\"action\":\"send\",\"channelId\":\"created\",\"messageId\":\"created-message\",\"apiLinks\":[{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"},{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}],\"stateLinks\":[{\"contractId\":\"linked-orders\",\"diagramId\":\"orders-state\",\"transitionId\":\"create-order\"}]}]}]}},\"formDrafts\":{\"copy-orders\":\"{\\\"unfinished\\\":\\\"copy draft\\\"}\",\"linked-orders\":\"{\\\"unfinished\\\":\\\"linked draft\\\"}\"}}","documentJSON":"{\"formatVersion\":3,\"title\":\"B31 linked and divergent copy frozen scenario\",\"participants\":[{\"id\":\"client\",\"name\":\"Client\",\"kind\":\"client\",\"description\":\"\"},{\"id\":\"orders\",\"name\":\"Orders\",\"kind\":\"service\",\"description\":\"\"},{\"id\":\"queue\",\"name\":\"Orders queue\",\"kind\":\"queue\",\"description\":\"\"}],\"messages\":[{\"id\":\"request-linked\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via linked contract\",\"description\":\"\",\"operation\":{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"request-copy\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via divergent copy\",\"description\":\"\",\"operation\":{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"event-created\",\"fromId\":\"orders\",\"toId\":\"queue\",\"kind\":\"event\",\"label\":\"Order created\",\"description\":\"\",\"eventBindings\":[{\"contractId\":\"orders-events\",\"operationId\":\"emit-created\"}]}],\"fragments\":[{\"id\":\"orders-option\",\"kind\":\"opt\",\"label\":\"Optional authored requests\",\"fromMessageId\":\"request-linked\",\"toMessageId\":\"request-copy\"}],\"contracts\":[{\"id\":\"linked-orders\",\"name\":\"Exact linked orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"integer\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Orders exact API links\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"linked\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}},{\"id\":\"copy-orders\",\"name\":\"Divergent copy orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"string\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Divergent authoritative embedded copy\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"copy\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}}],\"eventModel\":{\"servers\":[{\"id\":\"kafka\",\"name\":\"Declared Kafka\",\"description\":\"\",\"host\":\"inert.invalid:9092\",\"protocol\":\"kafka\",\"auth\":\"none\"}],\"channels\":[{\"id\":\"created\",\"name\":\"Order created\",\"description\":\"\",\"address\":\"orders.created\",\"serverIds\":[\"kafka\"],\"messageIds\":[\"created-message\"]}],\"messages\":[{\"id\":\"created-message\",\"name\":\"Created message\",\"description\":\"\",\"payloadSchemaId\":\"created-schema\",\"examples\":[{\"name\":\"Example\",\"payloadJSON\":\"{\\\"orderId\\\":\\\"example\\\"}\"}]}],\"schemas\":[{\"id\":\"created-schema\",\"name\":\"Created payload\",\"description\":\"\",\"schemaJSON\":\"{\\\"type\\\":\\\"object\\\",\\\"properties\\\":{\\\"orderId\\\":{\\\"type\\\":\\\"string\\\"}}}\"}],\"contracts\":[{\"id\":\"orders-events\",\"name\":\"Orders events\",\"description\":\"\",\"participantId\":\"orders\",\"version\":\"1\",\"operations\":[{\"id\":\"emit-created\",\"name\":\"Emit created\",\"description\":\"\",\"action\":\"send\",\"channelId\":\"created\",\"messageId\":\"created-message\",\"apiLinks\":[{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"},{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}],\"stateLinks\":[{\"contractId\":\"linked-orders\",\"diagramId\":\"orders-state\",\"transitionId\":\"create-order\"}]}]}]}}","formDraftsJSON":"{\"copy-orders\":\"{\\\"unfinished\\\":\\\"copy draft\\\"}\",\"linked-orders\":\"{\\\"unfinished\\\":\\\"linked draft\\\"}\"}"},{"scenarioId":"1","revisionId":"2","version":"2","contentHash":"df31cab0460a365b5fdc9e7d9485d92df982b107119d255db89284f2e63c2f43","documentHash":"8c5adedb2424c62e5253976bc6a9e01b3660dbd010a8088d56acd4b9206cf23e","envelope":"{\"document\":{\"formatVersion\":3,\"title\":\"B31 linked and divergent copy frozen scenario — newer head\",\"participants\":[{\"id\":\"client\",\"name\":\"Client\",\"kind\":\"client\",\"description\":\"\"},{\"id\":\"orders\",\"name\":\"Orders\",\"kind\":\"service\",\"description\":\"\"},{\"id\":\"queue\",\"name\":\"Orders queue\",\"kind\":\"queue\",\"description\":\"\"}],\"messages\":[{\"id\":\"request-linked\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via linked contract\",\"description\":\"\",\"operation\":{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"request-copy\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via divergent copy\",\"description\":\"\",\"operation\":{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"event-created\",\"fromId\":\"orders\",\"toId\":\"queue\",\"kind\":\"event\",\"label\":\"Order created\",\"description\":\"\",\"eventBindings\":[{\"contractId\":\"orders-events\",\"operationId\":\"emit-created\"}]}],\"fragments\":[{\"id\":\"orders-option\",\"kind\":\"opt\",\"label\":\"Optional authored requests\",\"fromMessageId\":\"request-linked\",\"toMessageId\":\"request-copy\"}],\"contracts\":[{\"id\":\"linked-orders\",\"name\":\"Exact linked orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"integer\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Orders exact API links\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"linked\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}},{\"id\":\"copy-orders\",\"name\":\"Divergent copy orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"string\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Divergent authoritative embedded copy\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"copy\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}}],\"eventModel\":{\"servers\":[{\"id\":\"kafka\",\"name\":\"Declared Kafka\",\"description\":\"\",\"host\":\"inert.invalid:9092\",\"protocol\":\"kafka\",\"auth\":\"none\"}],\"channels\":[{\"id\":\"created\",\"name\":\"Order created\",\"description\":\"\",\"address\":\"orders.created\",\"serverIds\":[\"kafka\"],\"messageIds\":[\"created-message\"]}],\"messages\":[{\"id\":\"created-message\",\"name\":\"Created message\",\"description\":\"\",\"payloadSchemaId\":\"created-schema\",\"examples\":[{\"name\":\"Example\",\"payloadJSON\":\"{\\\"orderId\\\":\\\"example\\\"}\"}]}],\"schemas\":[{\"id\":\"created-schema\",\"name\":\"Created payload\",\"description\":\"\",\"schemaJSON\":\"{\\\"type\\\":\\\"object\\\",\\\"properties\\\":{\\\"orderId\\\":{\\\"type\\\":\\\"string\\\"}}}\"}],\"contracts\":[{\"id\":\"orders-events\",\"name\":\"Orders events\",\"description\":\"\",\"participantId\":\"orders\",\"version\":\"1\",\"operations\":[{\"id\":\"emit-created\",\"name\":\"Emit created\",\"description\":\"\",\"action\":\"send\",\"channelId\":\"created\",\"messageId\":\"created-message\",\"apiLinks\":[{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"},{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}],\"stateLinks\":[{\"contractId\":\"linked-orders\",\"diagramId\":\"orders-state\",\"transitionId\":\"create-order\"}]}]}]}},\"formDrafts\":{\"copy-orders\":\"{\\\"unfinished\\\":\\\"changed newer head\\\"}\",\"linked-orders\":\"{\\\"unfinished\\\":\\\"linked draft\\\"}\"}}","documentJSON":"{\"formatVersion\":3,\"title\":\"B31 linked and divergent copy frozen scenario — newer head\",\"participants\":[{\"id\":\"client\",\"name\":\"Client\",\"kind\":\"client\",\"description\":\"\"},{\"id\":\"orders\",\"name\":\"Orders\",\"kind\":\"service\",\"description\":\"\"},{\"id\":\"queue\",\"name\":\"Orders queue\",\"kind\":\"queue\",\"description\":\"\"}],\"messages\":[{\"id\":\"request-linked\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via linked contract\",\"description\":\"\",\"operation\":{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"request-copy\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via divergent copy\",\"description\":\"\",\"operation\":{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"event-created\",\"fromId\":\"orders\",\"toId\":\"queue\",\"kind\":\"event\",\"label\":\"Order created\",\"description\":\"\",\"eventBindings\":[{\"contractId\":\"orders-events\",\"operationId\":\"emit-created\"}]}],\"fragments\":[{\"id\":\"orders-option\",\"kind\":\"opt\",\"label\":\"Optional authored requests\",\"fromMessageId\":\"request-linked\",\"toMessageId\":\"request-copy\"}],\"contracts\":[{\"id\":\"linked-orders\",\"name\":\"Exact linked orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"integer\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Orders exact API links\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"linked\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}},{\"id\":\"copy-orders\",\"name\":\"Divergent copy orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"string\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Divergent authoritative embedded copy\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"copy\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}}],\"eventModel\":{\"servers\":[{\"id\":\"kafka\",\"name\":\"Declared Kafka\",\"description\":\"\",\"host\":\"inert.invalid:9092\",\"protocol\":\"kafka\",\"auth\":\"none\"}],\"channels\":[{\"id\":\"created\",\"name\":\"Order created\",\"description\":\"\",\"address\":\"orders.created\",\"serverIds\":[\"kafka\"],\"messageIds\":[\"created-message\"]}],\"messages\":[{\"id\":\"created-message\",\"name\":\"Created message\",\"description\":\"\",\"payloadSchemaId\":\"created-schema\",\"examples\":[{\"name\":\"Example\",\"payloadJSON\":\"{\\\"orderId\\\":\\\"example\\\"}\"}]}],\"schemas\":[{\"id\":\"created-schema\",\"name\":\"Created payload\",\"description\":\"\",\"schemaJSON\":\"{\\\"type\\\":\\\"object\\\",\\\"properties\\\":{\\\"orderId\\\":{\\\"type\\\":\\\"string\\\"}}}\"}],\"contracts\":[{\"id\":\"orders-events\",\"name\":\"Orders events\",\"description\":\"\",\"participantId\":\"orders\",\"version\":\"1\",\"operations\":[{\"id\":\"emit-created\",\"name\":\"Emit created\",\"description\":\"\",\"action\":\"send\",\"channelId\":\"created\",\"messageId\":\"created-message\",\"apiLinks\":[{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"},{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}],\"stateLinks\":[{\"contractId\":\"linked-orders\",\"diagramId\":\"orders-state\",\"transitionId\":\"create-order\"}]}]}]}}","formDraftsJSON":"{\"copy-orders\":\"{\\\"unfinished\\\":\\\"changed newer head\\\"}\",\"linked-orders\":\"{\\\"unfinished\\\":\\\"linked draft\\\"}\"}"},{"scenarioId":"2","revisionId":"3","version":"1","contentHash":"6ad032c14e940ea560806ca5e987b085d49f091520acf4a4088edd29ec1ba6e3","documentHash":"94d8e63e0610f8b1bfd2869a5c743207d8546ab5dd14e466d1df18326458e152","envelope":"{\"document\":{\"formatVersion\":3,\"title\":\"B31 second owner with distinct same-key copy\",\"participants\":[{\"id\":\"client\",\"name\":\"Client\",\"kind\":\"client\",\"description\":\"\"},{\"id\":\"orders\",\"name\":\"Orders\",\"kind\":\"service\",\"description\":\"\"},{\"id\":\"queue\",\"name\":\"Orders queue\",\"kind\":\"queue\",\"description\":\"\"}],\"messages\":[{\"id\":\"request-linked\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via linked contract\",\"description\":\"\",\"operation\":{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"request-copy\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via divergent copy\",\"description\":\"\",\"operation\":{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"event-created\",\"fromId\":\"orders\",\"toId\":\"queue\",\"kind\":\"event\",\"label\":\"Order created\",\"description\":\"\",\"eventBindings\":[{\"contractId\":\"orders-events\",\"operationId\":\"emit-created\"}]}],\"fragments\":[{\"id\":\"orders-option\",\"kind\":\"opt\",\"label\":\"Optional authored requests\",\"fromMessageId\":\"request-linked\",\"toMessageId\":\"request-copy\"}],\"contracts\":[{\"id\":\"linked-orders\",\"name\":\"Exact linked orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"integer\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Orders exact API links\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"linked\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}},{\"id\":\"copy-orders\",\"name\":\"Divergent copy orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"string\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Second independent copy\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"copy\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}}],\"eventModel\":{\"servers\":[{\"id\":\"kafka\",\"name\":\"Declared Kafka\",\"description\":\"\",\"host\":\"inert.invalid:9092\",\"protocol\":\"kafka\",\"auth\":\"none\"}],\"channels\":[{\"id\":\"created\",\"name\":\"Order created\",\"description\":\"\",\"address\":\"orders.created\",\"serverIds\":[\"kafka\"],\"messageIds\":[\"created-message\"]}],\"messages\":[{\"id\":\"created-message\",\"name\":\"Created message\",\"description\":\"\",\"payloadSchemaId\":\"created-schema\",\"examples\":[{\"name\":\"Example\",\"payloadJSON\":\"{\\\"orderId\\\":\\\"example\\\"}\"}]}],\"schemas\":[{\"id\":\"created-schema\",\"name\":\"Created payload\",\"description\":\"\",\"schemaJSON\":\"{\\\"type\\\":\\\"object\\\",\\\"properties\\\":{\\\"orderId\\\":{\\\"type\\\":\\\"string\\\"}}}\"}],\"contracts\":[{\"id\":\"orders-events\",\"name\":\"Orders events\",\"description\":\"\",\"participantId\":\"orders\",\"version\":\"1\",\"operations\":[{\"id\":\"emit-created\",\"name\":\"Emit created\",\"description\":\"\",\"action\":\"send\",\"channelId\":\"created\",\"messageId\":\"created-message\",\"apiLinks\":[{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"},{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}],\"stateLinks\":[{\"contractId\":\"linked-orders\",\"diagramId\":\"orders-state\",\"transitionId\":\"create-order\"}]}]}]}},\"formDrafts\":{\"copy-orders\":\"unfinished second owner buffer\"}}","documentJSON":"{\"formatVersion\":3,\"title\":\"B31 second owner with distinct same-key copy\",\"participants\":[{\"id\":\"client\",\"name\":\"Client\",\"kind\":\"client\",\"description\":\"\"},{\"id\":\"orders\",\"name\":\"Orders\",\"kind\":\"service\",\"description\":\"\"},{\"id\":\"queue\",\"name\":\"Orders queue\",\"kind\":\"queue\",\"description\":\"\"}],\"messages\":[{\"id\":\"request-linked\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via linked contract\",\"description\":\"\",\"operation\":{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"request-copy\",\"fromId\":\"client\",\"toId\":\"orders\",\"kind\":\"request\",\"label\":\"Create via divergent copy\",\"description\":\"\",\"operation\":{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}},{\"id\":\"event-created\",\"fromId\":\"orders\",\"toId\":\"queue\",\"kind\":\"event\",\"label\":\"Order created\",\"description\":\"\",\"eventBindings\":[{\"contractId\":\"orders-events\",\"operationId\":\"emit-created\"}]}],\"fragments\":[{\"id\":\"orders-option\",\"kind\":\"opt\",\"label\":\"Optional authored requests\",\"fromMessageId\":\"request-linked\",\"toMessageId\":\"request-copy\"}],\"contracts\":[{\"id\":\"linked-orders\",\"name\":\"Exact linked orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"integer\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Orders exact API links\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"linked\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}},{\"id\":\"copy-orders\",\"name\":\"Divergent copy orders\",\"document\":{\"components\":{\"schemas\":{\"BooleanField\":true,\"OrderRequest\":{\"properties\":{\"userId\":{\"type\":\"integer\"}},\"type\":\"object\"},\"OrderResponse\":{\"properties\":{\"kind\":{\"type\":\"string\"},\"token\":{\"type\":\"string\"},\"total\":{\"type\":\"string\"}},\"type\":\"object\"}}},\"info\":{\"title\":\"Second independent copy\",\"version\":\"1\"},\"openapi\":\"3.1.0\",\"paths\":{\"/orders\":{\"post\":{\"operationId\":\"createOrder\",\"requestBody\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderRequest\"}}}},\"responses\":{\"200\":{\"content\":{\"application/json\":{\"schema\":{\"$ref\":\"#/components/schemas/OrderResponse\"}}},\"description\":\"Order response\"}},\"summary\":\"Создать заказ\",\"x-mocker-canvas-operation-id\":\"orders-create\"}}},\"x-mocker-response-rules\":{\"formatVersion\":1,\"rules\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"edges\":[{\"from\":\"start\",\"id\":\"start-response\",\"port\":\"next\",\"to\":\"response\"}],\"id\":\"orders-rule\",\"name\":\"Orders authored response\",\"nodes\":[{\"id\":\"start\",\"name\":\"Start\",\"type\":\"start\",\"x\":0,\"y\":0},{\"id\":\"response\",\"name\":\"Respond\",\"response\":{\"bodyJSON\":\"{\\\"total\\\":1}\",\"headers\":[],\"mediaType\":\"application/json\",\"status\":200},\"type\":\"response\",\"x\":100,\"y\":0}]}]},\"x-mocker-state-diagrams\":{\"diagrams\":[{\"id\":\"orders-state\",\"initialStateId\":\"new\",\"name\":\"Orders authored lifecycle\",\"states\":[{\"id\":\"new\",\"name\":\"New\",\"terminal\":false,\"x\":0,\"y\":0},{\"id\":\"done\",\"name\":\"Done\",\"terminal\":true,\"x\":100,\"y\":0}],\"transitions\":[{\"binding\":{\"method\":\"post\",\"path\":\"/orders\"},\"from\":\"new\",\"id\":\"create-order\",\"name\":\"Create order\",\"patchJSON\":\"{}\",\"responseStatus\":200,\"to\":\"done\"}]}],\"formatVersion\":1}},\"mode\":\"copy\",\"source\":{\"designId\":2,\"revisionId\":3,\"version\":2}}],\"eventModel\":{\"servers\":[{\"id\":\"kafka\",\"name\":\"Declared Kafka\",\"description\":\"\",\"host\":\"inert.invalid:9092\",\"protocol\":\"kafka\",\"auth\":\"none\"}],\"channels\":[{\"id\":\"created\",\"name\":\"Order created\",\"description\":\"\",\"address\":\"orders.created\",\"serverIds\":[\"kafka\"],\"messageIds\":[\"created-message\"]}],\"messages\":[{\"id\":\"created-message\",\"name\":\"Created message\",\"description\":\"\",\"payloadSchemaId\":\"created-schema\",\"examples\":[{\"name\":\"Example\",\"payloadJSON\":\"{\\\"orderId\\\":\\\"example\\\"}\"}]}],\"schemas\":[{\"id\":\"created-schema\",\"name\":\"Created payload\",\"description\":\"\",\"schemaJSON\":\"{\\\"type\\\":\\\"object\\\",\\\"properties\\\":{\\\"orderId\\\":{\\\"type\\\":\\\"string\\\"}}}\"}],\"contracts\":[{\"id\":\"orders-events\",\"name\":\"Orders events\",\"description\":\"\",\"participantId\":\"orders\",\"version\":\"1\",\"operations\":[{\"id\":\"emit-created\",\"name\":\"Emit created\",\"description\":\"\",\"action\":\"send\",\"channelId\":\"created\",\"messageId\":\"created-message\",\"apiLinks\":[{\"contractId\":\"linked-orders\",\"operationKey\":\"orders-create\"},{\"contractId\":\"copy-orders\",\"operationKey\":\"orders-create\"}],\"stateLinks\":[{\"contractId\":\"linked-orders\",\"diagramId\":\"orders-state\",\"transitionId\":\"create-order\"}]}]}]}}","formDraftsJSON":"{\"copy-orders\":\"unfinished second owner buffer\"}"}]`

type b24ScenarioGolden struct {
	ScenarioID     string `json:"scenarioId"`
	RevisionID     string `json:"revisionId"`
	Version        string `json:"version"`
	ContentHash    string `json:"contentHash"`
	DocumentHash   string `json:"documentHash"`
	Envelope       string `json:"envelope"`
	DocumentJSON   string `json:"documentJSON"`
	FormDraftsJSON string `json:"formDraftsJSON"`
}

func scenarioGoldens(t *testing.T) []b24ScenarioGolden {
	t.Helper()
	var rows []b24ScenarioGolden
	if err := jsonx.Unmarshal([]byte(b24ScenarioEnvelopeGoldens), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPrepareB24ScenarioEnvelopeGoldens(t *testing.T) {
	r := newTestRepo(t)
	for _, row := range scenarioGoldens(t) {
		var document Document
		var drafts map[string]string
		if err := jsonx.Unmarshal([]byte(row.DocumentJSON), &document); err != nil {
			t.Fatal(err)
		}
		if err := jsonx.Unmarshal([]byte(row.FormDraftsJSON), &drafts); err != nil {
			t.Fatal(err)
		}
		prepared, _, err := r.prepare(t.Context(), document, drafts)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.document != row.DocumentJSON || prepared.formDrafts != row.FormDraftsJSON || prepared.hash != row.ContentHash {
			t.Fatalf("B24 writer changed revision %s", row.RevisionID)
		}
	}
}

func TestArtifactSnapshotHistoricalDraftOnlyAndReadOnly(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.designs.Create(t.Context(), apidesign.CreateInput{Name: "Independent API owner", Document: `{"openapi":"3.1.0","info":{"title":"Owner","version":"1"},"paths":{}}`, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	document := validDocument("Снимок")
	document.Contracts = []Contract{{ID: "copy", Name: "Raw", Document: jsonx.RawMessage(`{"paths":{},"x-retained":{"n":9007199254740993,"html":"<>&","nested":[{"z":null,"a":false}]}}`)}}
	created, err := r.Create(t.Context(), CreateInput{Document: document, FormDrafts: map[string]string{"panel": "first buffer"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := r.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: document, FormDrafts: map[string]string{"panel": "second buffer"}, Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	before := artifactOwnerRows(t, r)
	old, err := r.ArtifactSnapshot(t.Context(), created.Scenario.ID, created.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := r.ArtifactSnapshot(t.Context(), created.Scenario.ID, saved.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.ScenarioID != created.Scenario.ID || old.RevisionID != created.Draft.ID || old.Version != 1 || latest.Version != 2 || old.ContentHash != created.Draft.Hash || latest.ContentHash != saved.Draft.Hash {
		t.Fatalf("wrong immutable identity: old=%+v latest=%+v", old, latest)
	}
	if old.ContentHash == latest.ContentHash || old.DocumentHash != latest.DocumentHash || old.DocumentJSON != latest.DocumentJSON || old.FormDraftsJSON == latest.FormDraftsJSON {
		t.Fatal("draft-only hash policy")
	}
	if old.DocumentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(old.DocumentJSON))) || !strings.Contains(old.DocumentJSON, "9007199254740993") || !reflect.DeepEqual(old.Document, created.Draft.Document) {
		t.Fatal("raw/decoded document changed")
	}
	for _, snapshot := range []*ArtifactSnapshot{old, latest} {
		var rawDocument, rawDrafts string
		if err := r.db.R.QueryRowContext(t.Context(), `SELECT document,form_drafts FROM design_scenario_revisions WHERE id=?`, snapshot.RevisionID).Scan(&rawDocument, &rawDrafts); err != nil {
			t.Fatal(err)
		}
		if snapshot.DocumentJSON != rawDocument || snapshot.FormDraftsJSON != rawDrafts {
			t.Fatal("stored raw pair lost")
		}
	}
	tx, err := r.db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := r.ArtifactDigestTx(t.Context(), tx, created.Scenario.ID, created.Draft.ID)
	if err != nil || digest != old.ContentHash {
		t.Fatalf("digest=%s err=%v", digest, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := artifactOwnerRows(t, r); after != before {
		t.Fatal("read changed API/scenario owner rows")
	}
}

// Compare every persisted field in both owners, including immutable revisions.
func artifactOwnerRows(t *testing.T, r *Repo) string {
	t.Helper()
	all := map[string][][]any{}
	for _, table := range []string{"api_designs", "api_design_revisions", "design_scenarios", "design_scenario_revisions"} {
		func() {
			rows, err := r.db.R.QueryContext(t.Context(), `SELECT * FROM `+table+` ORDER BY id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				values := make([]any, len(columns))
				dest := make([]any, len(columns))
				for i := range dest {
					dest[i] = &values[i]
				}
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				all[table] = append(all[table], values)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	raw, err := jsonx.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestArtifactSnapshotB24FrozenRawGoldens(t *testing.T) {
	r := newTestRepo(t)
	goldens := scenarioGoldens(t)
	err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		for _, id := range []int64{1, 2} {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenarios(id,name,created_at,updated_at) VALUES (?,'fixture',1,1)`, id); err != nil {
				return err
			}
		}
		for _, row := range goldens {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenario_revisions(id,scenario_id,version,hash,document,form_drafts,source,summary,created_at) VALUES (?,?,?,?,?,?,'mcp','B24 E0',1)`, row.RevisionID, row.ScenarioID, row.Version, row.ContentHash, row.DocumentJSON, row.FormDraftsJSON); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenarios SET version=2,draft_revision_id=2 WHERE id=1`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	before := artifactOwnerRows(t, r)
	for _, row := range goldens {
		id, _ := strconv.ParseInt(row.ScenarioID, 10, 64)
		rid, _ := strconv.ParseInt(row.RevisionID, 10, 64)
		version, _ := strconv.ParseInt(row.Version, 10, 64)
		got, err := r.ArtifactSnapshot(t.Context(), id, rid)
		if err != nil {
			t.Fatal(err)
		}
		if got.ScenarioID != id || got.RevisionID != rid || got.Version != version || got.DocumentJSON != row.DocumentJSON || got.FormDraftsJSON != row.FormDraftsJSON || got.ContentHash != row.ContentHash || got.DocumentHash != row.DocumentHash {
			t.Fatalf("B24 snapshot changed revision %s", row.RevisionID)
		}
		var drafts map[string]string
		if err := jsonx.Unmarshal([]byte(row.FormDraftsJSON), &drafts); err != nil {
			t.Fatal(err)
		}
		envelope, hash, err := encodeScenarioEnvelope(got.Document, drafts)
		if err != nil || string(envelope) != row.Envelope || hash != row.ContentHash {
			t.Fatalf("B24 envelope changed revision %s: %v", row.RevisionID, err)
		}
	}
	if artifactOwnerRows(t, r) != before {
		t.Fatal("B24 snapshot mutated owners")
	}
}

func TestArtifactSnapshotOwnershipAndCancellation(t *testing.T) {
	r := newTestRepo(t)
	a, err := r.Create(t.Context(), CreateInput{Document: validDocument("a"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Create(t.Context(), CreateInput{Document: validDocument("b"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := r.db.R.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, ids := range [][2]int64{{a.Scenario.ID, b.Draft.ID}, {a.Scenario.ID, 99999}, {99999, a.Draft.ID}, {0, a.Draft.ID}, {a.Scenario.ID, 0}, {-1, a.Draft.ID}} {
		if _, err := r.ArtifactSnapshot(t.Context(), ids[0], ids[1]); err == nil {
			t.Fatalf("snapshot accepted %v", ids)
		}
		if _, err := r.ArtifactDigestTx(t.Context(), tx, ids[0], ids[1]); err == nil {
			t.Fatalf("digest accepted %v", ids)
		}
	}
	if _, err := r.ArtifactSnapshot(t.Context(), a.Scenario.ID, b.Draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.ArtifactSnapshot(ctx, a.Scenario.ID, a.Draft.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot cancellation=%v", err)
	}
	if _, err := r.ArtifactDigestTx(ctx, tx, a.Scenario.ID, a.Draft.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("digest cancellation=%v", err)
	}
}

func dropScenarioRevisionFences(t *testing.T, r *Repo) {
	t.Helper()
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name='design_scenario_revisions'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, name := range names {
			if _, err := tx.ExecContext(t.Context(), `DROP TRIGGER "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactSnapshotCorruptionAndDigestOnly(t *testing.T) {
	for _, tc := range []struct {
		name, column, value string
		invalidDigest       bool
	}{
		{"document raw mismatch", "document", `{"formatVersion":1,"title":"tampered"}`, false},
		{"draft mismatch", "form_drafts", `{"panel":"tampered"}`, false},
		{"document malformed", "document", `{`, false},
		{"draft malformed", "form_drafts", `{`, false},
		{"hash mismatch", "hash", strings.Repeat("0", 64), false},
		{"hash malformed", "hash", "INVALID", true},
		{"hash oversized", "hash", strings.Repeat("a", 65), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRepo(t)
			d, err := r.Create(t.Context(), CreateInput{Document: validDocument("ok"), FormDrafts: map[string]string{"panel": "buffer"}, Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			dropScenarioRevisionFences(t, r)
			if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET `+tc.column+`=? WHERE id=?`, tc.value, d.Draft.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			before := artifactOwnerRows(t, r)
			if _, err := r.ArtifactSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID); err == nil {
				t.Fatal("corrupt snapshot accepted")
			}
			tx, err := r.db.R.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := r.ArtifactDigestTx(t.Context(), tx, d.Scenario.ID, d.Draft.ID)
			if tc.invalidDigest {
				if err == nil {
					t.Fatal("invalid stored digest accepted")
				}
			} else {
				want := d.Draft.Hash
				if tc.column == "hash" {
					want = tc.value
				}
				if err != nil || digest != want {
					t.Fatalf("digest decoded bodies or verified raw bytes: %q %v", digest, err)
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if artifactOwnerRows(t, r) != before {
				t.Fatal("corrupt reads mutated owners")
			}
		})
	}
}

func TestArtifactSnapshotAggregateUTF8Bound(t *testing.T) {
	r := newTestRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Document: validDocument(strings.Repeat("Я", 30)), FormDrafts: map[string]string{"panel": strings.Repeat("я", 100)}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	var document, drafts string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT document,form_drafts FROM design_scenario_revisions WHERE id=?`, d.Draft.ID).Scan(&document, &drafts); err != nil {
		t.Fatal(err)
	}
	aggregate := int64(len(document) + len(drafts))
	r.cfg.MaxBody = aggregate - 1
	if int64(len(document)) > r.cfg.MaxBody || int64(len(drafts)) > r.cfg.MaxBody {
		t.Fatal("test needs each body individually below bound")
	}
	// Malformed bodies must not reach parsing when the aggregate bound rejects them.
	dropScenarioRevisionFences(t, r)
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=? WHERE id=?`, "!"+document[1:], d.Draft.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ArtifactSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("aggregate predecode bound=%v", err)
	}
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=? WHERE id=?`, document, d.Draft.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tx, err := r.db.R.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if digest, err := r.ArtifactDigestTx(t.Context(), tx, d.Scenario.ID, d.Draft.ID); err != nil || digest != d.Draft.Hash {
		t.Fatalf("bounded digest %s %v", digest, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{aggregate, 0, -1} {
		r.cfg.MaxBody = limit
		if _, err := r.ArtifactSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID); err != nil {
			t.Fatalf("owner MaxBody=%d: %v", limit, err)
		}
	}
}

func TestArtifactSnapshotLegacyDraftsAndUnsafeIdentity(t *testing.T) {
	r := newTestRepo(t)
	document := validDocument("Legacy")
	prepared, _, err := r.prepare(t.Context(), document, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, _, err := r.prepare(t.Context(), document, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared != empty || prepared.formDrafts != "{}" {
		t.Fatal("writer nil/empty normalization changed")
	}
	const scenarioID int64 = 9007199254740993
	const revisionID int64 = 9007199254740995
	const version int64 = 9007199254740997
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenarios(id,name,version,created_at,updated_at) VALUES (?,'Legacy',?,1,1)`, scenarioID, version); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenario_revisions(id,scenario_id,version,hash,document,form_drafts,source,summary,created_at) VALUES (?,?,?,?,?,'null','ui','legacy nil',1)`, revisionID, scenarioID, version, prepared.hash, prepared.document)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.ArtifactSnapshot(t.Context(), scenarioID, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScenarioID != scenarioID || got.RevisionID != revisionID || got.Version != version || got.FormDraftsJSON != "null" || got.ContentHash != prepared.hash {
		t.Fatalf("legacy raw/identity=%+v", got)
	}
}

func TestPrepareOrdinaryRawMessageBytes(t *testing.T) {
	r := newTestRepo(t)
	document := validDocument("Golden")
	document.Contracts = []Contract{{ID: "copy", Name: "Raw", Document: jsonx.RawMessage(`{"paths":{},"x-retained":{"n":9007199254740993,"html":"<>&","nested":[{"z":null,"a":false}]}}`)}}
	prepared, _, err := r.prepare(t.Context(), document, map[string]string{"z": "last", "a": "<unsafe>"})
	if err != nil {
		t.Fatal(err)
	}
	const wantDocument = `{"formatVersion":1,"title":"Golden","participants":[],"messages":[],"fragments":[],"contracts":[{"id":"copy","name":"Raw","document":{"paths":{},"x-retained":{"n":9007199254740993,"html":"\u003c\u003e\u0026","nested":[{"z":null,"a":false}]}}}]}`
	const wantDrafts = `{"a":"\u003cunsafe\u003e","z":"last"}`
	const wantHash = "0abc33e233b9386a1a01915ce8dddd056de980879b06d810dbcd397e4abd3760"
	if prepared.document != wantDocument || prepared.formDrafts != wantDrafts || prepared.hash != wantHash {
		t.Fatalf("ordinary writer bytes changed: %+v", prepared)
	}
	encoded, hash, err := encodeScenarioEnvelope(document, map[string]string{"z": "last", "a": "<unsafe>"})
	if err != nil || string(encoded) != `{"document":`+wantDocument+`,"formDrafts":`+wantDrafts+`}` || hash != wantHash {
		t.Fatalf("pure encoder bytes %s hash %s err %v", encoded, hash, err)
	}
}

func TestArtifactSnapshotPreservesNonCanonicalRawPair(t *testing.T) {
	r := newTestRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Document: validDocument("Raw whitespace"), FormDrafts: map[string]string{"draft": "buffer"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := r.ArtifactSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	dropScenarioRevisionFences(t, r)
	rawDocument := " \n" + original.DocumentJSON + "\t"
	rawDrafts := "\n" + original.FormDraftsJSON + " "
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=?,form_drafts=? WHERE id=?`, rawDocument, rawDrafts, d.Draft.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ArtifactSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DocumentJSON != rawDocument || snapshot.FormDraftsJSON != rawDrafts || snapshot.ContentHash != original.ContentHash || snapshot.DocumentHash == original.DocumentHash {
		t.Fatal("raw document hash conflated with envelope hash")
	}
}

func TestArtifactSnapshotPreservesJSONParsingDepthLimit(t *testing.T) {
	r := newTestRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Document: validDocument("Depth"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	dropScenarioRevisionFences(t, r)
	// A small malformed fixture exercises jsonx's existing parser limit; there is
	// no new traversal limit or giant allocation experiment.
	raw := `{"contracts":[{"id":"c","document":` + strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001) + `}]}`
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=? WHERE id=?`, raw, d.Draft.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ArtifactSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("existing JSON depth limit not preserved: %v", err)
	}
}
