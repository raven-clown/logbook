package testutil_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/raven-clown/logbook/internal/testutil"
	"github.com/raven-clown/logbook/vector"
)

func TestBagOfWords(t *testing.T) {
	b := &testutil.BagOfWords{Dims: 64}
	vs, err := b.Embed(context.Background(), []string{"CI uses Go 1.26", "ci uses go 1.26!", "the cat sat"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(vs[0], vs[1]) {
		t.Fatal("same words gave different vectors")
	}
	if same, other := vector.Cosine(vs[0], vs[1]), vector.Cosine(vs[0], vs[2]); same < 0.999 || other >= same {
		t.Fatalf("similarity same %v, other %v", same, other)
	}
	b.SetErr(errors.New("down"))
	if _, err := b.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("SetErr did nothing")
	}
	b.SetErr(nil)
	b.SetBlock(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Embed(ctx, []string{"x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked call = %v", err)
	}
	if b.Calls() != 3 || b.Model() != "bag-of-words" {
		t.Fatalf("calls %d, model %q", b.Calls(), b.Model())
	}
}
