package assets_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/recipes"
)

// TestValidName_matchesRecipes holds the two copies of the name rule
// together: internal/recipes is a leaf that may not import this package
// (P3c D4), so its asset_url validator carries its own regexp — and this
// corpus, fed to both, is what keeps them one rule (A6 D2). A name this
// package accepts must validate as an asset_url value, and one it refuses
// must be refused there too.
func TestValidName_matchesRecipes(t *testing.T) {
	t.Parallel()
	corpus := []string{
		"avatar-1.jpg", "a.b.c", "_", "A", strings.Repeat("x", 128),
		"", ".", "..", "a/b", "a b", "a%20b", strings.Repeat("x", 129), "ü.png", "a\n", "asset:a.png",
	}
	for _, name := range corpus {
		quoted, err := jsonx.Marshal(name)
		if err != nil {
			t.Fatal(err)
		}
		r := recipes.Recipe{Kind: recipes.KindAssetURL, Data: quoted}
		recipeOK := r.Validate() == nil
		if got := assets.ValidName(name); got != recipeOK {
			t.Errorf("%q: assets.ValidName=%v, recipes asset_url Validate ok=%v — the two copies of the rule disagree", name, got, recipeOK)
		}
	}
}
