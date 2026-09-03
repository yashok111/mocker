package recipes_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/recipes"
)

// A6 (A7): the asset_url kind — shape validation, the value it writes, and
// the two ways it declines.

func TestAssetURL_Validate(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{`"avatar.jpg"`, `["a.png","b.png"]`, `"_"`} {
		r := recipes.Recipe{Kind: recipes.KindAssetURL, Data: jsonx.RawMessage(ok)}
		if err := r.Validate(); err != nil {
			t.Errorf("Validate(%s) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []struct{ data, why string }{
		{``, "no value"},
		{`[]`, "empty list"},
		{`"a/b"`, "a slash"},
		{`".."`, "dot-segment"},
		{`["ok.png",""]`, "an empty element"},
		{`42`, "not a string"},
		{`{"name":"a.png"}`, "an object"},
	} {
		r := recipes.Recipe{Kind: recipes.KindAssetURL, Data: jsonx.RawMessage(bad.data)}
		if err := r.Validate(); err == nil {
			t.Errorf("Validate(%s) accepted (%s)", bad.data, bad.why)
		}
	}
	// Policy words and the other kinds' fields are refused by name.
	r := recipes.Recipe{Kind: recipes.KindAssetURL, Data: jsonx.RawMessage(`"a.png"`), Field: "generate"}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "only a value") {
		t.Errorf("a field on asset_url: %v", err)
	}
}

func TestAssetURL_Value(t *testing.T) {
	t.Parallel()
	env := recipes.Env{Seed: 7, Type: "string", AssetBase: "https://alex.mock.local:8443/__mocker/assets/"}

	r := recipes.Recipe{Kind: recipes.KindAssetURL, Data: jsonx.RawMessage(`"pic one.jpg"`)}
	if _, _, err := r.Value(env, "avatar", nil, nil); err == nil {
		t.Fatal("a name with a space validated at Value time")
	}

	r = recipes.Recipe{Kind: recipes.KindAssetURL, Data: jsonx.RawMessage(`"avatar-1.jpg"`)}
	v, ok, err := r.Value(env, "avatar", nil, nil)
	if err != nil || !ok || v != "https://alex.mock.local:8443/__mocker/assets/avatar-1.jpg" {
		t.Fatalf("Value = %v %v %v", v, ok, err)
	}

	// A list: one of them, the same one for the same seed, escaped.
	r = recipes.Recipe{Kind: recipes.KindAssetURL, Data: jsonx.RawMessage(`["a.png","b.png","c.png"]`)}
	first, ok, err := r.Value(env, "avatar", nil, nil)
	if err != nil || !ok {
		t.Fatalf("list Value: %v %v", ok, err)
	}
	again, _, _ := r.Value(env, "avatar", nil, nil)
	if first != again {
		t.Fatalf("same seed, different pick: %v then %v", first, again)
	}
	seen := map[any]bool{}
	for seed := uint64(0); seed < 30; seed++ {
		e := env
		e.Seed = seed
		v, _, _ := r.Value(e, "avatar", nil, nil)
		seen[v] = true
	}
	if len(seen) != 3 {
		t.Fatalf("30 seeds picked %d distinct names, want all 3", len(seen))
	}

	// An empty base DECLINES — never a relative URL.
	env.AssetBase = ""
	if v, ok, err := r.Value(env, "avatar", nil, nil); ok || err != nil {
		t.Fatalf("empty AssetBase: %v %v %v, want a decline", v, ok, err)
	}
}
