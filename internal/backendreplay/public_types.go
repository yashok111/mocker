package backendreplay

import (
	"context"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"time"
)

type TargetInfo struct {
	ID          string `json:"id"`
	Version     int64  `json:"version"`
	IsolationID string `json:"isolationId"`
}
type Target struct {
	TargetInfo
	Transport         p.Transport
	ConfigFingerprint string
}
type ConnectInput struct {
	ConfiguredTargetID   string `json:"configuredTargetId"`
	ExpectedIdentityHash string `json:"expectedIdentityHash"`
	AllowReset           bool   `json:"allowReset"`
	IdempotencyKey       string `json:"idempotencyKey"`
}
type RevokeInput struct {
	Profile        Pin    `json:"profile"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type SavePackageInput struct {
	ID              string     `json:"id"`
	ExpectedVersion int64      `json:"expectedVersion"`
	Package         Package    `json:"package"`
	Provenance      Provenance `json:"provenance"`
	IdempotencyKey  string     `json:"idempotencyKey"`
}
type SavedPackage struct {
	Pin        Pin        `json:"pin"`
	Package    Package    `json:"package"`
	Provenance Provenance `json:"provenance"`
}
type Run struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"projectId"`
	Status     string     `json:"status"`
	Input      RunInput   `json:"input"`
	Provenance Provenance `json:"provenance"`
	Report     *Report    `json:"report,omitzero"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// ActorAllowed is evaluated at admission and before every dispatch. Startup
// supplies the same account policy as the existing admin and MCP planes. An
// error means the verdict could not be reached and is returned as it is,
// never read as a refusal (review 2026-10-06, F177).
type ActorAllowed func(context.Context, string) (bool, error)
