// Package testutil holds deterministic fakes for the model-backed plugin
// points, so tests run without a network.
package testutil

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"unicode"

	"github.com/raven-clown/logbook/memory"
)

// BagOfWords is an embedder that hashes lower-cased words into Dims buckets
// and normalises the result, so texts that share words are similar.
type BagOfWords struct {
	Dims int
	// Synonyms maps a word to the word whose bucket it shares, so a query can
	// match by meaning where full-text search finds nothing.
	Synonyms map[string]string
	// OnEmbed runs inside every successful call, before it returns.
	OnEmbed func(texts []string)
	mu      sync.Mutex
	err      error
	block    bool
	calls    int
}

// SetErr makes every call fail with err, or work again with nil.
func (b *BagOfWords) SetErr(err error) { b.mu.Lock(); b.err = err; b.mu.Unlock() }

// SetBlock makes every call wait until its context ends.
func (b *BagOfWords) SetBlock(block bool) { b.mu.Lock(); b.block = block; b.mu.Unlock() }

// Calls returns how many times Embed ran.
func (b *BagOfWords) Calls() int { b.mu.Lock(); defer b.mu.Unlock(); return b.calls }

// Model names the fake's vectors.
func (b *BagOfWords) Model() string { return "bag-of-words" }

// Embed returns one vector per text.
func (b *BagOfWords) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	b.mu.Lock()
	b.calls++
	err, block := b.err, b.block
	b.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = b.vector(t)
	}
	if b.OnEmbed != nil {
		b.OnEmbed(texts)
	}
	return out, nil
}

func (b *BagOfWords) vector(text string) []float32 {
	if len(b.Synonyms) == 0 {
		return Vector(text, b.dims())
	}
	words := strings.Fields(strings.ToLower(text))
	for i, w := range words {
		if s, ok := b.Synonyms[strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })]; ok {
			words[i] = s
		}
	}
	return Vector(strings.Join(words, " "), b.dims())
}

func (b *BagOfWords) dims() int {
	if b.Dims <= 0 {
		return 256
	}
	return b.Dims
}

// Vector is the bag-of-words vector of text.
func Vector(text string, dims int) []float32 {
	v := make([]float32, dims)
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%uint32(dims)]++ // #nosec G115 -- dims is a small positive test value
	}
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n > 0 {
		for i := range v {
			v[i] = float32(float64(v[i]) / math.Sqrt(n))
		}
	}
	return v
}

// FakeExtractor returns fixed facts and answers contradiction checks with Contradict.
type FakeExtractor struct {
	Facts      []memory.Candidate
	Contradict func(a, b string) bool
	Err        error
	// Block makes Contradicts wait until its context ends.
	Block bool
	mu    sync.Mutex
	asked [][2]string
}

// Extract returns Facts, or Err.
func (f *FakeExtractor) Extract(_ context.Context, _ string) ([]memory.Candidate, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Facts, nil
}

// Contradicts reports Contradict(a, b), false when it is nil.
func (f *FakeExtractor) Contradicts(ctx context.Context, a, b string) (bool, error) {
	f.mu.Lock()
	f.asked = append(f.asked, [2]string{a, b})
	f.mu.Unlock()
	if f.Block {
		<-ctx.Done()
		return false, ctx.Err()
	}
	if f.Err != nil {
		return false, f.Err
	}
	return f.Contradict != nil && f.Contradict(a, b), nil
}

// Asked returns the pairs Contradicts was called with.
func (f *FakeExtractor) Asked() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.asked...)
}
