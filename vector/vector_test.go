package vector_test

import (
	"context"
	"math"
	"testing"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/vector"
)

func TestCosine(t *testing.T) {
	for _, tt := range []struct {
		name string
		a, b []float32
		want float64
	}{
		{"same", []float32{1, 2, 3}, []float32{2, 4, 6}, 1},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"zero vector", []float32{0, 0}, []float32{1, 1}, 0},
		{"length mismatch", []float32{1, 0}, []float32{1, 0, 0}, 0},
		{"empty", nil, nil, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := vector.Cosine(tt.a, tt.b); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("Cosine = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeIndex struct{}

func (fakeIndex) Upsert(context.Context, string, string, string, []float32) error { return nil }
func (fakeIndex) Delete(context.Context, string, string) error                    { return nil }
func (fakeIndex) Search(context.Context, string, []string, []float32, int) ([]memory.Hit, error) {
	return nil, nil
}

func TestRegistry(t *testing.T) {
	vector.Register("fake-test", func(vector.Loader) (vector.Index, error) { return fakeIndex{}, nil })
	if _, err := vector.Open("fake-test", nil); err != nil {
		t.Fatal(err)
	}
	_, err := vector.Open("nope", nil)
	if memory.CodeOf(err) != memory.CodeInvalidArgument {
		t.Fatalf("unknown index = %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering a name twice did not panic")
		}
	}()
	vector.Register("fake-test", func(vector.Loader) (vector.Index, error) { return fakeIndex{}, nil })
}
