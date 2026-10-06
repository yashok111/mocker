// Package ordersprotocol owns the closed orders-replay-v1 wire contract.
// It performs no I/O and depends on neither the replay engine nor the service.
package ordersprotocol

import "context"

const (
	Version        = "orders-replay-v1"
	FixtureVersion = "payment-write-retry-v1"
	FailurePoint   = "orders.persist.after_payment"
	ManifestPolicy = "orders-source-tree-v1"
	BodyLimit      = 64 << 10
	JournalLimit   = 1 << 20
	ReportLimit    = 4 << 20
)

type Endpoint string

const (
	IdentityEndpoint Endpoint = "identity"
	ResetEndpoint    Endpoint = "reset"
	FailureEndpoint  Endpoint = "failure"
	OrderEndpoint    Endpoint = "order"
	JournalEndpoint  Endpoint = "journal"
)

type Identity struct {
	Protocol       string `json:"protocol"`
	Service        string `json:"service"`
	ServiceVersion string `json:"serviceVersion"`
	TestOnly       bool   `json:"testOnly"`
	IsolationID    string `json:"isolationId"`
	InstanceID     string `json:"instanceId"`
	Variant        string `json:"variant"`
	BuildHash      string `json:"buildHash"`
	SourceTreeHash string `json:"sourceTreeHash"`
	SourcePolicy   string `json:"sourcePolicy"`
	FixtureHash    string `json:"fixtureHash"`
}

// Epoch is outside IdentityHash: reset advances it without changing identity.
type IdentityResponse struct {
	Identity     Identity `json:"identity"`
	IdentityHash string   `json:"identityHash"`
	Epoch        int64    `json:"epoch"`
}
type SourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type BuildDescriptor struct {
	ServiceVersion string   `json:"serviceVersion"`
	Variant        string   `json:"variant"`
	SourceTreeHash string   `json:"sourceTreeHash"`
	Toolchain      string   `json:"toolchain"`
	GOOS           string   `json:"goos"`
	GOARCH         string   `json:"goarch"`
	BuildFlags     []string `json:"buildFlags"`
}
type Order struct {
	SKU         string `json:"sku"`
	Quantity    int    `json:"quantity"`
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}
type FixtureSpec struct {
	Version          string `json:"version"`
	FailurePoint     string `json:"failurePoint"`
	Order            Order  `json:"order"`
	PaymentScope     string `json:"paymentScope"`
	PersistenceScope string `json:"persistenceScope"`
}

func Fixture() FixtureSpec {
	return FixtureSpec{FixtureVersion, FailurePoint, Order{"widget", 1, 1000, "USD"}, "mocked", "actual_fixture"}
}

// Every mutation is fenced, including reset (which carries the preceding epoch).
type Fence struct {
	RunID        string `json:"runId"`
	StepID       string `json:"stepId"`
	RequestKey   string `json:"requestKey"`
	Epoch        int64  `json:"epoch"`
	IdentityHash string `json:"identityHash"`
}
type ResetAuthorization struct {
	ID            string `json:"id"`
	Version       int64  `json:"version"`
	TargetID      string `json:"targetId"`
	ConfigVersion int64  `json:"configVersion"`
	IdentityHash  string `json:"identityHash"`
	IsolationID   string `json:"isolationId"`
	AllowReset    bool   `json:"allowReset"`
}
type ResetRequest struct {
	Fence
	Authorization ResetAuthorization `json:"authorization"`
	FixtureHash   string             `json:"fixtureHash"`
}
type FailureRequest struct {
	Fence
	Point string `json:"point"`
	Count int    `json:"count"`
}
type OrderRequest struct {
	Fence
	BusinessKey string `json:"businessKey"`
	Attempt     int    `json:"attempt"`
	Order       Order  `json:"order"`
}
type Counters struct {
	Orders   int `json:"orders"`
	Charges  int `json:"charges"`
	Attempts int `json:"attempts"`
	Triggers int `json:"triggers"`
}

// Receipt is stored atomically with a completed operation. It is never rewritten.
// HTTPStatus is the original delivery status, including the intended injected 503.
type Receipt struct {
	Fence
	Endpoint    Endpoint `json:"endpoint"`
	RequestHash string   `json:"requestHash"`
	HTTPStatus  int      `json:"httpStatus"`
	Outcome     string   `json:"outcome"`
	ResultEpoch int64    `json:"resultEpoch"`
	BusinessKey string   `json:"businessKey,omitempty"`
	Attempt     int      `json:"attempt,omitzero"`
	OrderID     string   `json:"orderId,omitempty"`
	ChargeID    string   `json:"chargeId,omitempty"`
	Counters    Counters `json:"counters"`
	Sequence    int64    `json:"sequence"`
}
type ErrorResponse struct {
	Protocol string `json:"protocol"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Journal events carry facts, never a trusted passed boolean.
type Event struct {
	Sequence    int64  `json:"sequence"`
	Kind        string `json:"kind"`
	StepID      string `json:"stepId"`
	RequestKey  string `json:"requestKey"`
	BusinessKey string `json:"businessKey,omitempty"`
	ObjectID    string `json:"objectId,omitempty"`
	AmountMinor int64  `json:"amountMinor,omitzero"`
}
type OrderRecord struct {
	ID          string `json:"id"`
	BusinessKey string `json:"businessKey"`
	Order       Order  `json:"order"`
}
type ChargeRecord struct {
	ID          string `json:"id"`
	BusinessKey string `json:"businessKey"`
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
	Scope       string `json:"scope"`
}
type Journal struct {
	Identity     Identity       `json:"identity"`
	IdentityHash string         `json:"identityHash"`
	RunID        string         `json:"runId"`
	Epoch        int64          `json:"epoch"`
	CurrentEpoch int64          `json:"currentEpoch"`
	FixtureHash  string         `json:"fixtureHash"`
	Complete     bool           `json:"complete"`
	HighWater    int64          `json:"highWater"`
	PendingKeys  []string       `json:"pendingKeys"`
	Receipts     []Receipt      `json:"receipts"`
	Events       []Event        `json:"events"`
	Orders       []OrderRecord  `json:"orders"`
	Charges      []ChargeRecord `json:"charges"`
}

// Transport is implemented only by internal/probe. The client is already bound
// to operator config and credentials; callers cannot pass URLs or headers.
// Response preserves bounded wire evidence. Complete=false means Body may only
// be a prefix and cannot be used as a receipt. Body contains no request headers.
// Exactly one of Payload/ProtocolError is set for a completely decoded response.
// Transport/decode failures return a non-nil Go error, retaining available wire
// evidence. A valid HTTP error envelope is ProtocolError with nil Go error.
// The injected 503 is a valid Receipt Payload with nil Go error.
type Response[T any] struct {
	HTTPStatus    int
	Body          []byte
	Complete      bool
	Payload       *T
	ProtocolError *ErrorResponse
}
type Transport interface {
	Identity(context.Context) (Response[IdentityResponse], error)
	Reset(context.Context, ResetRequest) (Response[Receipt], error)
	Arm(context.Context, FailureRequest) (Response[Receipt], error)
	Order(context.Context, OrderRequest) (Response[Receipt], error)
	Journal(context.Context, string) (Response[Journal], error)
}
