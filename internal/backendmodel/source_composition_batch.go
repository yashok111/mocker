package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"uuid"
)

func validateComposedCommand(c ImportCommand, s *ImportSession) error {
	if err := validateComposedWireMembers(c); err != nil {
		return err
	}
	count := 0
	for _, present := range []bool{
		c.Node != nil, c.Edge != nil, c.Evidence != nil, c.Remove != nil,
		c.Identity != nil, c.Deletion != nil, c.ClaimIdentity != nil, c.Resolution != nil,
	} {
		if present {
			count++
		}
	}
	if count != 1 {
		return semantic("commands", "Command requires exactly one tagged payload")
	}
	if c.ClaimIdentity != nil {
		return validateSourceClaimCommand(c.Op, *c.ClaimIdentity)
	}
	if c.Resolution != nil {
		return validateSourceResolutionCommand(c.Op, *c.Resolution)
	}
	typ, key, err := commandAddress(c)
	if err != nil {
		return err
	}
	if !externalKey(key) {
		return semantic("externalKey", "Invalid external key")
	}
	if c.Node != nil || c.Edge != nil {
		return validateComposedRecordCommand(c, s, typ)
	}
	legacy := *s
	legacy.Profile = EventsProfile
	legacy.Mode = "reconcile"
	return validateCommand(c, &legacy)
}

func validateSourceClaimCommand(op string, x SourceClaimIdentity) error {
	validDecision := op == "claim_identity" && ValidID(x.DecisionID)
	validSubject := externalKey(x.ExternalKey) && x.RecordType == x.Target.RecordType
	validReason := nonblank(x.Reason) && len(x.EvidenceKeys) > 0
	if !validDecision || !validSubject || !validReason {
		return semantic("claimIdentity", "Invalid identity decision")
	}
	if err := validateBaseAssertionRef(x.Target); err != nil {
		return err
	}
	return validateEvidenceKeys(x.EvidenceKeys)
}

func validateSourceResolutionCommand(op string, x SourceAssertionResolution) error {
	validDecision := op == "resolve_assertion" && ValidID(x.DecisionID) && nonblank(x.Reason)
	validSubject := ValidID(x.ID) && slices.Contains([]string{"node", "edge"}, x.RecordType)
	validSelection := ValidID(x.Select.RepositoryID) && externalKey(x.Select.ProviderNamespace) && validHash(x.Select.AssertionHash)
	if !validDecision || !validSubject || !validHash(x.ConflictHash) || !validSelection {
		return semantic("resolution", "Invalid assertion resolution")
	}
	return validateSourceSelector(x.Property)
}

func validateComposedRecordCommand(c ImportCommand, s *ImportSession, typ string) error {
	var kind string
	var keys []string
	if c.Node != nil {
		kind, keys = c.Node.Kind, c.Node.EvidenceKeys
		if !nonblank(c.Node.Name) {
			return semantic("name", "Node name is required")
		}
	} else {
		kind, keys = c.Edge.Kind, c.Edge.EvidenceKeys
	}
	kinds := SupportedNodeKindsForProfile(ComposedProfile)
	if typ == "edge" {
		kinds = SupportedEdgeKindsForProfile(ComposedProfile)
	}
	if !slices.Contains(kinds, kind) {
		return semantic("kind", "Unsupported source6 kind")
	}
	if err := validateEvidenceKeys(keys); err != nil {
		return err
	}
	provisional := map[string]string{}
	addressID := func(address string) string {
		if id := provisional[address]; id != "" {
			return id
		}
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", len(provisional)+1)
		provisional[address] = id
		return id
	}
	_, _, err := normalizeSourcePayload(
		c,
		s,
		func(site SourceReferenceSite) (BaseAssertionRef, error) {
			raw, err := canonicalJSON(site.Ref)
			if err != nil {
				return BaseAssertionRef{}, err
			}
			return BaseAssertionRef{RecordType: site.RecordType, ExpectedID: addressID(site.RecordType + "\x00" + string(raw))}, nil
		},
		func(key string) (string, error) { return addressID("evidence\x00" + key), nil },
	)
	return err
}

func loadSourceDecisions(ctx context.Context, q importReader, sid string) ([]SourceDecision, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,state,document FROM backend_import_source_decisions WHERE session_id=? ORDER BY sequence`, sid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SourceDecision{}
	for rows.Next() {
		var d SourceDecision
		var doc string
		if err := rows.Scan(&d.Sequence, &d.State, &doc); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(doc), &d.Command); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func loadSourceBatchCommitments(ctx context.Context, q importReader, sid string) ([]SourceBatchCommitment, error) {
	rows, err := q.QueryContext(ctx, `SELECT accepted_version,batch_id,payload_hash,request_hash FROM backend_import_batches WHERE session_id=? ORDER BY accepted_version,batch_id`, sid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SourceBatchCommitment{}
	for rows.Next() {
		var item SourceBatchCommitment
		if err := rows.Scan(&item.AcceptedVersion, &item.BatchID, &item.PayloadHash, &item.RequestHash); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func reserveComposedIdentity(ctx context.Context, tx *sql.Tx, s *ImportSession, base *SourceGraphSnapshot, typ, key string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM backend_import_identities WHERE session_id=? AND record_type=? AND external_key=?`, s.ID, typ, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = pinnedComposedIdentity(base, s, typ, key)
	// Registry reservations constrain reuse but never resolve an immutable base ref.
	b, err := binding(ctx, tx, s, typ, key)
	if err != nil {
		return "", err
	}
	if b != nil {
		if id != "" && b.ID != id {
			return "", identityConflict("Pinned key conflicts with durable binding")
		}
		if id == "" {
			if b.State != "reserved" {
				return "", identityConflict("Retired or deleted key cannot be silently reused")
			}
			id = b.ID
		}
	}
	if id == "" {
		id = uuid.NewV7().String()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_identities(session_id,record_type,external_key,id) VALUES(?,?,?,?)`, s.ID, typ, key, id)
	return id, err
}

func pinnedComposedIdentity(base *SourceGraphSnapshot, s *ImportSession, typ, key string) string {
	var id string
	for _, a := range base.Assertions {
		if a.RecordType == typ && a.ExternalKey == key && a.Owner.RepositoryID == s.RepositoryID && a.Owner.ProviderNamespace == s.Manifest.Provider.Namespace {
			id = a.RecordID
			break
		}
	}
	if typ == "evidence" {
		for _, e := range base.State.Evidence {
			if e.ExternalKey == key && e.Ownership != nil && e.Ownership.RepositoryID == s.RepositoryID && e.Ownership.ProviderNamespace == s.Manifest.Provider.Namespace {
				id = e.ID
				break
			}
		}
	}
	return id
}

func saveSourceDecision(ctx context.Context, tx *sql.Tx, s *ImportSession, c ImportCommand, base *SourceGraphSnapshot) (string, string, string, error) {
	var decisionID, key string
	kind := c.Op
	if x := c.ClaimIdentity; x != nil {
		decisionID, key = x.DecisionID, x.RecordType+"\x00"+x.ExternalKey
	} else {
		x := c.Resolution
		decisionID, key = x.DecisionID, x.RecordType+"\x00"+x.ID+"\x00"+sourcePropertyKey(x.Property)
	}
	hash, err := requestDigest(c)
	if err != nil {
		return "", "", "", err
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT input_hash FROM backend_import_source_decisions WHERE session_id=? AND decision_id=?`, s.ID, decisionID).Scan(&prior)
	if err == nil {
		if prior != hash {
			return "", "", "", identityConflict("Decision ID was reused with another body")
		}
		if x := c.ClaimIdentity; x != nil {
			return x.RecordType, x.ExternalKey, x.Target.ExpectedID, nil
		}
		return "", "", "", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", "", err
	}
	id, typ, external := "", "", ""
	if x := c.ClaimIdentity; x != nil {
		if err := reserveSourceClaimIdentity(ctx, tx, s, base, *x); err != nil {
			return "", "", "", err
		}
		id, typ, external = x.Target.ExpectedID, x.RecordType, x.ExternalKey
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE backend_import_source_decisions SET state='superseded' WHERE session_id=? AND decision_kind=? AND decision_key=? AND state='active'`, s.ID, kind, key); err != nil {
			return "", "", "", err
		}
	}
	doc, err := json.Marshal(c)
	if err != nil {
		return "", "", "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_source_decisions(session_id,decision_id,decision_kind,decision_key,state,sequence,document,input_hash) SELECT ?,?,?,?,'active',COALESCE(MAX(sequence),0)+1,?,? FROM backend_import_source_decisions WHERE session_id=?`, s.ID, decisionID, kind, key, string(doc), hash, s.ID)
	return typ, external, id, err
}

func reserveSourceClaimIdentity(
	ctx context.Context,
	tx *sql.Tx,
	s *ImportSession,
	base *SourceGraphSnapshot,
	x SourceClaimIdentity,
) error {
	if x.Target.RepositoryID != s.RepositoryID {
		return identityConflict("Shared identity must remain in its repository")
	}
	if _, err := exactSourceAssertion(base.Assertions, x.Target); err != nil {
		return err
	}
	var reserved string
	err := tx.QueryRowContext(ctx, `SELECT id FROM backend_import_identities WHERE session_id=? AND record_type=? AND external_key=?`, s.ID, x.RecordType, x.ExternalKey).Scan(&reserved)
	if err == nil {
		return identityConflict("Claim identity must precede any acknowledged allocation")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	b, err := binding(ctx, tx, s, x.RecordType, x.ExternalKey)
	if err != nil {
		return err
	}
	if b != nil {
		return identityConflict("Claim key already has a durable binding")
	}
	for _, a := range base.Assertions {
		if a.RecordType == x.RecordType && a.RecordID == x.Target.ExpectedID && a.Owner.RepositoryID == s.RepositoryID && a.Owner.ProviderNamespace == s.Manifest.Provider.Namespace {
			return identityConflict("Selected partition already claims this UUID")
		}
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_import_identities WHERE session_id=? AND record_type=? AND id=?`, s.ID, x.RecordType, x.Target.ExpectedID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return identityConflict("Two selected keys cannot claim one UUID")
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_identities(session_id,record_type,external_key,id) VALUES(?,?,?,?)`, s.ID, x.RecordType, x.ExternalKey, x.Target.ExpectedID)
	return err
}

type composedBatch struct {
	tx         *sql.Tx
	session    *ImportSession
	base       *SourceGraphSnapshot
	result     *BatchReceipt
	seen       map[string]bool
	identities map[string]bool
}

func putComposedCommands(
	ctx context.Context,
	tx *sql.Tx,
	s *ImportSession,
	commands []ImportCommand,
	result *BatchReceipt,
) error {
	base, err := loadComposedBase(ctx, tx, s.ProjectID, s.BaseRevisionID)
	if err != nil {
		return err
	}
	batch := composedBatch{
		tx: tx, session: s, base: base, result: result,
		seen: map[string]bool{}, identities: map[string]bool{},
	}
	result.Identities = []RecordIdentity{}
	result.DecisionIDs = []string{}
	for _, command := range commands {
		if err := batch.put(ctx, command); err != nil {
			return err
		}
	}
	return nil
}

func (b *composedBatch) put(ctx context.Context, c ImportCommand) error {
	if err := validateComposedCommand(c, b.session); err != nil {
		return err
	}
	if c.ClaimIdentity != nil || c.Resolution != nil {
		return b.putDecision(ctx, c)
	}
	return b.putRecord(ctx, c)
}

func (b *composedBatch) putDecision(ctx context.Context, c ImportCommand) error {
	typ, key, id, err := saveSourceDecision(ctx, b.tx, b.session, c, b.base)
	if err != nil {
		return err
	}
	var decisionID string
	if c.ClaimIdentity != nil {
		decisionID = c.ClaimIdentity.DecisionID
	} else {
		decisionID = c.Resolution.DecisionID
	}
	if !slices.Contains(b.result.DecisionIDs, decisionID) {
		b.result.DecisionIDs = append(b.result.DecisionIDs, decisionID)
	}
	if id != "" {
		b.recordIdentity(RecordIdentity{RecordType: typ, ExternalKey: key, ID: id})
	}
	return nil
}

func (b *composedBatch) recordIdentity(identity RecordIdentity) {
	address := identity.RecordType + "\x00" + identity.ExternalKey
	if b.identities[address] {
		return
	}
	b.result.Identities = append(b.result.Identities, identity)
	b.identities[address] = true
}

func (b *composedBatch) putRecord(ctx context.Context, c ImportCommand) error {
	typ, key, _ := commandAddress(c)
	address := typ + "\x00" + key
	if b.seen[address] {
		return semantic("commands", "Duplicate addressed record in batch")
	}
	b.seen[address] = true
	id, err := b.reserveIdentity(ctx, c, typ, key)
	if err != nil {
		return err
	}
	b.recordIdentity(RecordIdentity{RecordType: typ, ExternalKey: key, ID: id})
	if c.Op == "remove" {
		return b.removeRecord(ctx, typ, key)
	}
	table := "backend_import_records"
	if c.Identity != nil || c.Deletion != nil {
		table = "backend_import_decisions"
	}
	doc, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = b.tx.ExecContext(
		ctx,
		`INSERT INTO `+table+`(session_id,record_type,external_key,document) VALUES(?,?,?,?) ON CONFLICT(session_id,record_type,external_key) DO UPDATE SET document=excluded.document`,
		b.session.ID,
		typ,
		key,
		string(doc),
	)
	return err
}

func (b *composedBatch) reserveIdentity(ctx context.Context, c ImportCommand, typ, key string) (string, error) {
	if c.Identity == nil && c.Deletion == nil {
		return reserveComposedIdentity(ctx, b.tx, b.session, b.base, typ, key)
	}
	selected := selectedSourceIdentityState(b.base, b.session)
	copySession := *b.session
	copySession.Mode = "reconcile"
	return reserveIdentity(ctx, b.tx, &copySession, c, typ, key, &selected)
}

func (b *composedBatch) removeRecord(ctx context.Context, typ, key string) error {
	if _, err := b.tx.ExecContext(
		ctx,
		`DELETE FROM backend_import_records WHERE session_id=? AND record_type=? AND external_key=?`,
		b.session.ID,
		typ,
		key,
	); err != nil {
		return err
	}
	_, err := b.tx.ExecContext(
		ctx,
		`DELETE FROM backend_import_decisions WHERE session_id=? AND record_type=? AND external_key=?`,
		b.session.ID,
		typ,
		key,
	)
	return err
}

func selectedSourceIdentityState(base *SourceGraphSnapshot, s *ImportSession) RevisionState {
	selected := RevisionState{}
	for _, a := range base.Assertions {
		if a.Owner.RepositoryID != s.RepositoryID || a.Owner.ProviderNamespace != s.Manifest.Provider.Namespace {
			continue
		}
		if a.RecordType == "node" {
			selected.Nodes = append(selected.Nodes, Node{ID: a.RecordID, ExternalKey: a.ExternalKey, Kind: a.Payload.Kind})
		} else {
			selected.Edges = append(selected.Edges, Edge{ID: a.RecordID, ExternalKey: a.ExternalKey, Kind: a.Payload.Kind})
		}
	}
	for _, e := range base.State.Evidence {
		if e.Ownership != nil && e.Ownership.RepositoryID == s.RepositoryID && e.Ownership.ProviderNamespace == s.Manifest.Provider.Namespace {
			selected.Evidence = append(selected.Evidence, e)
		}
	}
	return selected
}
