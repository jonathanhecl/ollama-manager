package rag

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type Match struct {
	ID      int64   `json:"id"`
	Term    string  `json:"term"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

func vectorNorm(v []float64) float64 {
	norm := 0.0
	for _, f := range v {
		norm = math.Hypot(norm, f)
	}
	return norm
}

func decodeVector(b []byte, dims int) ([]float64, error) {
	if err := checkVectorBlob(b, dims); err != nil {
		return nil, err
	}
	v := make([]float64, dims)
	for i := 0; i < dims; i++ {
		v[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
	}
	return v, nil
}

func metaMismatch(expected, got Meta) error {
	if expected.ID != "" && got.ID != expected.ID {
		return errors.New("base identity changed since it was validated")
	}
	if got.EmbeddingModel != expected.EmbeddingModel {
		return fmt.Errorf("base embedding model changed to %q", got.EmbeddingModel)
	}
	if got.EmbeddingDigest != expected.EmbeddingDigest {
		return errors.New("base embedding digest changed")
	}
	if got.Dimensions != expected.Dimensions {
		return fmt.Errorf("base dimensions changed to %d", got.Dimensions)
	}
	return nil
}

func Search(ctx context.Context, dir, filename string, expected Meta, query []float64, limit int, minScore float64) ([]Match, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid limit %d", limit)
	}
	if math.IsNaN(minScore) || math.IsInf(minScore, 0) {
		return nil, errors.New("invalid minimum score")
	}
	if expected.Dimensions <= 0 || expected.Dimensions > maxDimensions {
		return nil, fmt.Errorf("invalid dimensions %d", expected.Dimensions)
	}
	if len(query) != expected.Dimensions {
		return nil, fmt.Errorf("query has %d dimensions, base expects %d", len(query), expected.Dimensions)
	}
	for _, f := range query {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errors.New("non-finite value in query embedding")
		}
	}
	qnorm := vectorNorm(query)
	if math.IsNaN(qnorm) || math.IsInf(qnorm, 0) || qnorm == 0 {
		return nil, errors.New("query embedding has no usable norm")
	}
	q := make([]float64, len(query))
	for i, f := range query {
		q[i] = f / qnorm
	}

	path, err := Path(dir, filename)
	if err != nil {
		return nil, err
	}
	db, err := openRO(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, version, err := readMetaContext(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("not a RAG base (%w)", err)
	}
	if err := validateContext(ctx, db, m, version); err != nil {
		return nil, err
	}
	if err := metaMismatch(expected, m); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, term, content, embedding FROM entries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matches := []Match{}
	for rows.Next() {
		var id int64
		var term, content string
		var blob []byte
		if err := rows.Scan(&id, &term, &content, &blob); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := decodeVector(blob, m.Dimensions)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", id, err)
		}
		if strings.TrimSpace(term) == "" && strings.TrimSpace(content) == "" {
			continue
		}
		norm := vectorNorm(v)
		if math.IsNaN(norm) || math.IsInf(norm, 0) {
			return nil, fmt.Errorf("entry %d: embedding has no usable norm", id)
		}
		if norm == 0 {
			continue
		}
		dot := 0.0
		for i, f := range v {
			dot += q[i] * (f / norm)
		}
		if math.IsNaN(dot) {
			return nil, fmt.Errorf("entry %d: non-finite similarity", id)
		}
		if dot > 1 {
			dot = 1
		} else if dot < -1 {
			dot = -1
		}
		if dot >= minScore {
			matches = append(matches, Match{ID: id, Term: term, Content: content, Score: dot})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].ID < matches[j].ID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}
