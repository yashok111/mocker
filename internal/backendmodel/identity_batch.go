package backendmodel

import (
	"context"
	"slices"
	"strings"
)

// batchIdentities is what reserveIdentity reads across one import batch, read
// or built once per batch instead of once per command.
//
// Review 2026-10-06, F84/F194: reserveIdentity re-read and JSON-decoded every
// staged decision of the session for each of up to 500 commands, and resolved
// base identities by a linear scan of the base graph per command (in composed
// mode it also rebuilt the selected partition per decision command), all
// inside the single writer. A session holding thousands of decisions made a
// batch cost decisions x commands decodes while every other write waited.
// The staged list is now loaded once and kept in step with the batch's own
// decision writes and removals, and base identities come from an index.
type batchIdentities struct {
	base      *RevisionState
	index     map[string][2]string
	decisions []ImportCommand
	loaded    bool
}

func newBatchIdentities(base *RevisionState) *batchIdentities {
	return &batchIdentities{base: base}
}

// identity is stateIdentity over the batch's base, by index: the first record
// of the base with that type and external key, as stateIdentity returns it.
func (b *batchIdentities) identity(typ, key string) (string, string) {
	if b.base == nil {
		return "", ""
	}
	if b.index == nil {
		b.index = make(map[string][2]string, len(b.base.Nodes)+len(b.base.Edges)+len(b.base.Evidence))
		put := func(address, id, kind string) {
			if _, ok := b.index[address]; !ok {
				b.index[address] = [2]string{id, kind}
			}
		}
		for _, x := range b.base.Nodes {
			put("node\x00"+x.ExternalKey, x.ID, x.Kind)
		}
		for _, x := range b.base.Edges {
			put("edge\x00"+x.ExternalKey, x.ID, x.Kind)
		}
		for _, x := range b.base.Evidence {
			put("evidence\x00"+x.ExternalKey, x.ID, "")
		}
	}
	v := b.index[typ+"\x00"+key]
	return v[0], v[1]
}

// staged returns the session's staged identity and deletion decisions in
// loadDecisions order (record_type, external_key).
func (b *batchIdentities) staged(ctx context.Context, q importReader, sid string) ([]ImportCommand, error) {
	if !b.loaded {
		decisions, err := loadDecisions(ctx, q, sid)
		if err != nil {
			return nil, err
		}
		b.decisions, b.loaded = decisions, true
	}
	return b.decisions, nil
}

func decisionOrder(typ, key string) func(ImportCommand, struct{}) int {
	return func(d ImportCommand, _ struct{}) int {
		dt, dk, _ := commandAddress(d)
		if c := strings.Compare(dt, typ); c != 0 {
			return c
		}
		return strings.Compare(dk, key)
	}
}

// stage mirrors the decision upsert the batch just wrote for (typ, key).
func (b *batchIdentities) stage(c ImportCommand, typ, key string) {
	if !b.loaded {
		return
	}
	i, found := slices.BinarySearchFunc(b.decisions, struct{}{}, decisionOrder(typ, key))
	if found {
		b.decisions[i] = c
		return
	}
	b.decisions = slices.Insert(b.decisions, i, c)
}

// unstage mirrors the decision delete the batch just ran for (typ, key).
func (b *batchIdentities) unstage(typ, key string) {
	if !b.loaded {
		return
	}
	if i, found := slices.BinarySearchFunc(b.decisions, struct{}{}, decisionOrder(typ, key)); found {
		b.decisions = slices.Delete(b.decisions, i, i+1)
	}
}
