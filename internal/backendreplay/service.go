package backendreplay

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yashok111/mocker/internal/backendblob"

	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

type Service struct {
	repo                                 *Repo
	graphs                               *backendmodel.Repo
	targets                              map[string]Target
	mu                                   sync.Mutex
	recovered, started, closed, finished bool
	cancel                               context.CancelFunc
	done, ready                          chan struct{}
	active                               map[string]context.CancelFunc
	runErr                               error
	// Each terminal write attempt's window; tests shorten it.
	persistTimeout time.Duration
	// ArtifactReader supplies exact owner reads when admitting a saved package.
	ArtifactReader func(context.Context) *backendmodel.EditorArtifactRequest
	ActorAllowed   ActorAllowed
}

func NewService(repo *Repo, graphs *backendmodel.Repo, targets []Target) *Service {
	s := &Service{repo: repo, graphs: graphs, targets: map[string]Target{}, done: make(chan struct{}), ready: make(chan struct{}), active: map[string]context.CancelFunc{}, persistTimeout: 5 * time.Second}
	for _, t := range targets {
		s.targets[t.ID] = t
	}
	s.ActorAllowed = func(ctx context.Context, actor string) (bool, error) {
		id, ok := actorID(actor)
		if !ok {
			return false, nil
		}
		var n int
		if err := repo.db.R.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=?`, id).Scan(&n); err != nil {
			return false, err
		}
		return n == 1, nil
	}
	return s
}

// actorID parses an account id. A malformed actor is a verdict (no such
// account), never a failed lookup.
func actorID(actor string) (int64, bool) {
	id, err := strconv.ParseInt(actor, 10, 64)
	return id, err == nil && id >= 1
}
func (s *Service) Targets() []TargetInfo {
	out := make([]TargetInfo, 0, len(s.targets))
	for _, t := range s.targets {
		out = append(out, t.TargetInfo)
	}
	slices.SortFunc(out, func(a, b TargetInfo) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}
func (s *Service) actor(ctx context.Context, actor string) error {
	if s.ActorAllowed == nil {
		return replayFault(403, "forbidden", "Actor is no longer authorized")
	}
	// Review 2026-10-06, F177: a lookup that failed (a cancelled request, a
	// reader-pool or database error) is not an authorization verdict. It used
	// to collapse into the 403 below, so the real cause was never logged and
	// an agent chased a revoked consent that did not exist. The worker still
	// fails closed: any error here closes the run as unverified.
	allowed, err := s.ActorAllowed(ctx, actor)
	if err != nil {
		return err
	}
	if !allowed {
		return replayFault(403, "forbidden", "Actor is no longer authorized")
	}
	return nil
}
func (s *Service) project(ctx context.Context, pid string) error {
	var n int
	if !p.ValidID(pid) {
		return invalidReplay()
	}
	if err := s.repo.db.R.QueryRowContext(ctx, `SELECT count(*) FROM backend_projects WHERE id=?`, pid).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return missingReplay()
	}
	return nil
}
func (s *Service) target(profile Profile) (Target, error) {
	t, ok := s.targets[profile.TargetID]
	if !ok || t.Transport == nil || t.Version != profile.ConfigVersion || t.IsolationID != profile.Identity.IsolationID {
		return t, conflictReplay("Configured target no longer matches profile")
	}
	return t, nil
}
func (s *Service) Connect(ctx context.Context, pid, actor string, in ConnectInput) (*Profile, error) {
	if err := s.actor(ctx, actor); err != nil {
		return nil, err
	}
	if err := s.project(ctx, pid); err != nil {
		return nil, err
	}
	if (in.ConfiguredTargetID == "" || len(in.ConfiguredTargetID) > 200) || !p.ValidHash(in.ExpectedIdentityHash) || !in.AllowReset || !p.ValidID(in.IdempotencyKey) {
		return nil, invalidReplay()
	}
	hash, err := replayHash(struct {
		Actor string
		Input ConnectInput
	}{actor, in})
	if err != nil {
		return nil, err
	}
	var out Profile
	if found, err := receiptRead(ctx, s.repo.db.R, pid, "connect", in.IdempotencyKey, hash, &out); err != nil || found {
		return &out, err
	}
	target, live, err := s.consentedIdentity(ctx, in)
	if err != nil {
		return nil, err
	}
	out = Profile{Pin: Pin{ID: newReplayID(), Version: 1}, TargetID: target.ID, ConfigVersion: target.Version, Identity: live.Identity, IdentityHash: live.IdentityHash, Authorization: p.ResetAuthorization{ID: newReplayID(), Version: 1, TargetID: target.ID, ConfigVersion: target.Version, IdentityHash: live.IdentityHash, IsolationID: target.IsolationID, AllowReset: true}}
	out.Pin.ContentHash, err = p.Hash("backend-replay-profile-v1", out)
	if err != nil {
		return nil, err
	}
	if err = out.Validate(); err != nil {
		return nil, err
	}
	err = s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		if found, err := receiptRead(ctx, tx, pid, "connect", in.IdempotencyKey, hash, &out); err != nil || found {
			return err
		}
		raw, err := marshalReplay(out)
		if err != nil {
			return err
		}
		auth, err := marshalReplay(out.Authorization)
		if err != nil {
			return err
		}
		if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_replay_profiles(project_id,id,version,target_id,config_version,identity_hash,content_hash,document_json,authorization_json,author,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, pid, out.Pin.ID, out.Pin.Version, out.TargetID, out.ConfigVersion, out.IdentityHash, out.Pin.ContentHash, string(raw), string(auth), actor, nowReplay()); err != nil {
			return err
		}
		return receiptWrite(ctx, tx, pid, "connect", in.IdempotencyKey, hash, out)
	})
	return &out, err
}

// consentedIdentity reads the configured target's live identity and requires
// it to be exactly the one the caller consented to.
func (s *Service) consentedIdentity(ctx context.Context, in ConnectInput) (Target, *p.IdentityResponse, error) {
	target, ok := s.targets[in.ConfiguredTargetID]
	if !ok || target.Transport == nil {
		return Target{}, nil, missingReplay()
	}
	res, err := target.Transport.Identity(ctx)
	if err != nil || !res.Complete || res.Payload == nil || res.HTTPStatus != 200 {
		return Target{}, nil, conflictReplay("Configured target identity could not be verified")
	}
	live := res.Payload
	if live.Validate() != nil || live.IdentityHash != in.ExpectedIdentityHash || live.Identity.IsolationID != target.IsolationID {
		return Target{}, nil, conflictReplay("Identity differs from explicit consent")
	}
	return target, live, nil
}
func (s *Service) GetProfile(ctx context.Context, pid, id string, version int64) (*Profile, error) {
	return readProfile(ctx, s.repo.db.R, pid, id, version)
}
func (s *Service) GetPackage(ctx context.Context, pid, id string, version int64) (*SavedPackage, error) {
	return readPackage(ctx, s.repo.db.R, pid, id, version)
}

// Page sizes of the profile and package lists (vars only so tests can page
// small). A package document may be stored up to 4 MiB, so its page is the
// smaller one: 25 bounds a page's decoded documents near 100 MiB on the
// 7.8 GB box.
var (
	profilePageSize = 100
	packagePageSize = 25
)

// Profiles lists a project's profile versions oldest first, one page at a
// time. The list used to decode up to 1001 full documents in one request and
// then answer 409 for good past 1000 (review 2026-10-06, F124); it is now a
// keyset page. cursor is "<id>:<version>" of the last item of the previous
// page ("" for the first); a page shorter than profilePageSize (100) is the last.
func (s *Service) Profiles(ctx context.Context, pid, cursor string) ([]Profile, error) {
	return listVersioned[Profile](ctx, s, pid, cursor, profileList, profilePageSize)
}

// Packages is Profiles for saved package versions (packagePageSize, 25).
func (s *Service) Packages(ctx context.Context, pid, cursor string) ([]SavedPackage, error) {
	return listVersioned[SavedPackage](ctx, s, pid, cursor, packageList, packagePageSize)
}

// versionedList names one versioned list's two queries as constants, so no
// SQL is assembled at run time.
type versionedList struct{ position, page string }

var (
	profileList = versionedList{
		position: `SELECT created_at,id,version FROM backend_replay_profiles_documents WHERE project_id=? AND id=? AND version=?`,
		page:     `SELECT document_json FROM backend_replay_profiles_documents WHERE project_id=? AND (?='' OR created_at>? OR (created_at=? AND (id>? OR (id=? AND version>?)))) ORDER BY created_at,id,version LIMIT ?`,
	}
	packageList = versionedList{
		position: `SELECT created_at,id,version FROM backend_replay_packages_documents WHERE project_id=? AND id=? AND version=?`,
		page:     `SELECT document_json FROM backend_replay_packages_documents WHERE project_id=? AND (?='' OR created_at>? OR (created_at=? AND (id>? OR (id=? AND version>?)))) ORDER BY created_at,id,version LIMIT ?`,
	}
)

func listVersioned[T any](ctx context.Context, s *Service, pid, cursor string, list versionedList, pageSize int) ([]T, error) {
	out := []T{}
	if err := s.project(ctx, pid); err != nil {
		return nil, err
	}
	var afterCreated, afterID string
	var afterVersion int64
	if cursor != "" {
		id, version, ok := strings.Cut(cursor, ":")
		n, err := strconv.ParseInt(version, 10, 64)
		if !ok || err != nil || n < 1 || !p.ValidID(id) {
			return nil, invalidReplay()
		}
		err = s.repo.db.R.QueryRowContext(ctx, list.position, pid, id, n).Scan(&afterCreated, &afterID, &afterVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidReplay()
		}
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.repo.db.R.QueryContext(ctx, list.page, pid, afterID, afterCreated, afterCreated, afterID, afterID, afterVersion, pageSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		var v T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Revoke(ctx context.Context, pid, actor string, in RevokeInput) error {
	if err := s.actor(ctx, actor); err != nil {
		return err
	}
	if in.Profile.Validate() != nil || !p.ValidID(in.IdempotencyKey) {
		return invalidReplay()
	}
	hash, err := replayHash(struct {
		Actor string
		Input RevokeInput
	}{actor, in})
	if err != nil {
		return err
	}
	return s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		var result struct {
			Revoked bool `json:"revoked"`
		}
		if found, err := receiptRead(ctx, tx, pid, "revoke", in.IdempotencyKey, hash, &result); err != nil || found {
			return err
		}
		profile, err := readProfile(ctx, tx, pid, in.Profile.ID, in.Profile.Version)
		if err != nil {
			return err
		}
		if profile.Pin != in.Profile {
			return conflictReplay("Profile hash mismatch")
		}
		var owner string
		if err = tx.QueryRowContext(ctx, `SELECT author FROM backend_replay_profiles_documents WHERE project_id=? AND id=? AND version=?`, pid, in.Profile.ID, in.Profile.Version).Scan(&owner); err != nil {
			return err
		}
		if owner != actor {
			return replayFault(403, "forbidden", "Only consent author may revoke")
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO backend_replay_revocations VALUES(?,?,?,?,?)`, pid, in.Profile.ID, in.Profile.Version, actor, nowReplay()); err != nil {
			return err
		}
		result.Revoked = true
		return receiptWrite(ctx, tx, pid, "revoke", in.IdempotencyKey, hash, result)
	})
}
func (s *Service) SavePackage(ctx context.Context, pid, actor string, in SavePackageInput) (*SavedPackage, error) {
	if err := s.actor(ctx, actor); err != nil {
		return nil, err
	}
	if !p.ValidID(in.ID) || in.ExpectedVersion < 0 || in.ExpectedVersion == math.MaxInt64 || !p.ValidID(in.IdempotencyKey) || in.Package.Validate() != nil {
		return nil, invalidReplay()
	}
	hash, err := replayHash(struct {
		Actor string
		Input SavePackageInput
	}{actor, in})
	if err != nil {
		return nil, err
	}
	var out SavedPackage
	if found, err := receiptRead(ctx, s.repo.db.R, pid, "save", in.IdempotencyKey, hash, &out); err != nil || found {
		return &out, err
	}
	profile, err := s.admitPackage(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	contentHash, err := PackageHash(in.Package)
	if err != nil {
		return nil, err
	}
	out = SavedPackage{Pin: Pin{ID: in.ID, Version: in.ExpectedVersion + 1, ContentHash: contentHash}, Package: in.Package, Provenance: in.Provenance}
	raw, err := marshalReplay(out)
	if err != nil {
		return nil, err
	}
	if len(raw) > p.ReportLimit {
		return nil, invalidReplay()
	}
	err = s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		if found, err := receiptRead(ctx, tx, pid, "save", in.IdempotencyKey, hash, &out); err != nil || found {
			return err
		}
		if err := checkFindingTarget(ctx, tx, pid, in.Package); err != nil {
			return err
		}
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(version),0) FROM backend_replay_packages_documents WHERE project_id=? AND id=?`, pid, in.ID).Scan(&version); err != nil {
			return err
		}
		if version != in.ExpectedVersion {
			return conflictReplay("Package version changed")
		}
		if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_replay_packages(project_id,id,version,content_hash,document_json,target_hash,profile_id,profile_version,author,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, pid, in.ID, out.Pin.Version, contentHash, string(raw), in.Package.TargetHash, profile.Pin.ID, profile.Pin.Version, actor, nowReplay()); err != nil {
			return err
		}
		return receiptWrite(ctx, tx, pid, "save", in.IdempotencyKey, hash, out)
	})
	return &out, err
}

// admitPackage checks a package against its exact profile and provenance, the
// current target and the diagram scope it binds, and returns the profile.
func (s *Service) admitPackage(ctx context.Context, pid string, in SavePackageInput) (*Profile, error) {
	profile, err := s.GetProfile(ctx, pid, in.Package.Profile.ID, in.Package.Profile.Version)
	if err != nil {
		return nil, err
	}
	if profile.Pin != in.Package.Profile || in.Provenance.Validate(profile.Identity) != nil {
		return nil, conflictReplay("Exact profile and source/build provenance required")
	}
	if err = s.checkPackageTarget(ctx, pid, in.Package); err != nil {
		return nil, err
	}
	if err = ValidateDiagramBindings(ctx, s.graphs, pid, in.Package); err != nil {
		return nil, err
	}
	return profile, nil
}

// checkPackageTarget requires the package's target pin to be the graph's
// current effective target, and each artifact pin to be in that target and
// exactly what the artifact owner holds.
func (s *Service) checkPackageTarget(ctx context.Context, pid string, pkg Package) error {
	if s.graphs == nil {
		return conflictReplay("Graph owner unavailable")
	}
	graph, err := s.graphs.ResolveEffectiveGraph(ctx, pid, pkg.Target)
	if err != nil {
		return err
	}
	if graph.Pins.TargetHash != pkg.TargetHash || !reflect.DeepEqual(graph.Target, pkg.Target) {
		return conflictReplay("Target pin mismatch")
	}
	if len(pkg.ArtifactPins) == 0 {
		return nil
	}
	if s.ArtifactReader == nil {
		return conflictReplay("Artifact owner unavailable")
	}
	reader := s.ArtifactReader(ctx)
	for _, pin := range pkg.ArtifactPins {
		if !slices.Contains(graph.Pins.ArtifactPins, pin) {
			return conflictReplay("Artifact not in selected target")
		}
		actual, err := reader.SnapshotPin(backendmodel.ArtifactKey{Kind: pin.Kind, ID: pin.ID}, pin.RevisionID)
		if err != nil {
			return err
		}
		if actual != pin {
			return conflictReplay("Artifact owner hash mismatch")
		}
	}
	return nil
}

// checkFindingTarget requires a package saved for a finding to target a
// revision that finding occurred in.
func checkFindingTarget(ctx context.Context, tx *sql.Tx, pid string, pkg Package) error {
	if pkg.FindingFingerprint == "" {
		return nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_finding_occurrences_documents WHERE project_id=? AND fingerprint=? AND json_extract(document,'$.targetHash')=?`, pid, pkg.FindingFingerprint, pkg.TargetHash).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return conflictReplay("Finding does not belong to target")
	}
	return nil
}
