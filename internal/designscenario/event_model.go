package designscenario

// EventModel contains shared Kafka definitions. A contract selects operations
// from these definitions for one participant's AsyncAPI document.
type EventModel struct {
	Servers   []EventServer   `json:"servers"`
	Channels  []EventChannel  `json:"channels"`
	Messages  []EventMessage  `json:"messages"`
	Schemas   []EventSchema   `json:"schemas"`
	Contracts []EventContract `json:"contracts"`
}

type EventServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Host        string `json:"host"`
	Protocol    string `json:"protocol"`
	Auth        string `json:"auth"`
}

type EventChannel struct {
	ID                    string             `json:"id"`
	Name                  string             `json:"name"`
	Description           string             `json:"description"`
	Address               string             `json:"address"`
	ServerIDs             []string           `json:"serverIds"`
	MessageIDs            []string           `json:"messageIds"`
	DiscriminatorProperty string             `json:"discriminatorProperty,omitempty"`
	Kafka                 *EventChannelKafka `json:"kafka,omitempty"`
}

type EventChannelKafka struct {
	Partitions *int32 `json:"partitions,omitempty"`
	Replicas   *int32 `json:"replicas,omitempty"`
}

type EventMessage struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	PayloadSchemaID string         `json:"payloadSchemaId,omitempty"`
	HeadersSchemaID string         `json:"headersSchemaId,omitempty"`
	KeySchemaID     string         `json:"keySchemaId,omitempty"`
	Examples        []EventExample `json:"examples"`
}

type EventExample struct {
	Name        string `json:"name"`
	PayloadJSON string `json:"payloadJSON"`
	HeadersJSON string `json:"headersJSON,omitempty"`
}

type EventSchema struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SchemaJSON  string `json:"schemaJSON"`
}

type EventContract struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Description   string           `json:"description"`
	ParticipantID string           `json:"participantId"`
	Version       string           `json:"version"`
	Operations    []EventOperation `json:"operations"`
}

type EventOperation struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Action      string               `json:"action"`
	ChannelID   string               `json:"channelId"`
	MessageID   string               `json:"messageId"`
	Kafka       *EventOperationKafka `json:"kafka,omitempty"`
}

type EventOperationKafka struct {
	GroupID  string `json:"groupId,omitempty"`
	ClientID string `json:"clientId,omitempty"`
}

type EventBinding struct {
	ContractID  string `json:"contractId"`
	OperationID string `json:"operationId"`
}
