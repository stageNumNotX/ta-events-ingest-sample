// Package traffichash implements traffic.hash.v1.
// This is not cs.hash.v1 / catalog hash.
//
// Length prefix is uint16BE of UTF-8 byte length per part. The earlier
// uint32BE reading of Q-HASH is superseded (owner 2026-09-10).
package traffichash

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

const (
	Algorithm = "traffic.hash.v1"
	nul       = 0
	maxPart   = 0xffff
	// MaxAssignmentBytes matches producer HashV1Body (compiler-service#65).
	// SUBJECT_ID 257..65535 bytes must not hash here.
	MaxAssignmentBytes = 256
)

// Parts is the exact ordered payload. Do not reorder.
type Parts struct {
	AssignmentKey   string `json:"assignmentKey"`
	AssignmentValue string `json:"assignmentValue"`
	PolicyID        string `json:"policyID"`
	DecisionScopeID string `json:"decisionScopeID"`
	DistributionKey string `json:"distributionKey"`
	Salt            string `json:"salt"`
}

func (p Parts) ordered() []string {
	return []string{
		p.AssignmentKey,
		p.AssignmentValue,
		p.PolicyID,
		p.DecisionScopeID,
		p.DistributionKey,
		p.Salt,
	}
}

// Body is the exact pre-hash byte string:
// algorithm || NUL || for each of 6 parts: uint16BE(len UTF-8) || bytes.
// Producer hash vectors export it as bodyHex; Digest is SHA256(Body).
func Body(p Parts) ([]byte, error) {
	if n := len([]byte(p.AssignmentValue)); n > MaxAssignmentBytes {
		return nil, fmt.Errorf("%s: assignmentValue exceeds %d bytes", Algorithm, MaxAssignmentBytes)
	}
	body := make([]byte, 0, len(Algorithm)+1+6*2)
	body = append(body, Algorithm...)
	body = append(body, nul)
	var lenbuf [2]byte
	for _, part := range p.ordered() {
		if !utf8.ValidString(part) {
			return nil, fmt.Errorf("%s: part is not valid UTF-8", Algorithm)
		}
		b := []byte(part)
		if len(b) > maxPart {
			return nil, fmt.Errorf("%s: part too long", Algorithm)
		}
		binary.BigEndian.PutUint16(lenbuf[:], uint16(len(b)))
		body = append(body, lenbuf[:]...)
		body = append(body, b...)
	}
	return body, nil
}

// DecodeBody is the strict inverse of Body. It rejects a wrong algorithm
// prefix, a non-6-part payload, and trailing bytes; it does not guess.
func DecodeBody(body []byte) (Parts, error) {
	var p Parts
	prefix := append([]byte(Algorithm), nul)
	if len(body) < len(prefix) || string(body[:len(prefix)]) != string(prefix) {
		return p, fmt.Errorf("%s: body does not start with algorithm||NUL", Algorithm)
	}
	rest := body[len(prefix):]
	parts := make([]string, 0, 6)
	for len(parts) < 6 {
		if len(rest) < 2 {
			return p, fmt.Errorf("%s: truncated length prefix at part %d", Algorithm, len(parts))
		}
		n := int(binary.BigEndian.Uint16(rest[:2]))
		rest = rest[2:]
		if len(rest) < n {
			return p, fmt.Errorf("%s: truncated part %d (want %d bytes, have %d)", Algorithm, len(parts), n, len(rest))
		}
		s := string(rest[:n])
		if !utf8.ValidString(s) {
			return p, fmt.Errorf("%s: part %d is not valid UTF-8", Algorithm, len(parts))
		}
		parts = append(parts, s)
		rest = rest[n:]
	}
	if len(rest) != 0 {
		return p, fmt.Errorf("%s: %d trailing bytes after 6 parts", Algorithm, len(rest))
	}
	p = Parts{
		AssignmentKey:   parts[0],
		AssignmentValue: parts[1],
		PolicyID:        parts[2],
		DecisionScopeID: parts[3],
		DistributionKey: parts[4],
		Salt:            parts[5],
	}
	if len([]byte(p.AssignmentValue)) > MaxAssignmentBytes {
		return p, fmt.Errorf("%s: assignmentValue exceeds %d bytes", Algorithm, MaxAssignmentBytes)
	}
	return p, nil
}

// Digest is SHA256(Body).
func Digest(p Parts) ([32]byte, error) {
	var out [32]byte
	body, err := Body(p)
	if err != nil {
		return out, err
	}
	return sha256.Sum256(body), nil
}

// Point maps digest[0:8] as big-endian uint64 modulo totalShare.
func Point(p Parts, totalShare uint64) (uint64, error) {
	if totalShare == 0 {
		return 0, fmt.Errorf("%s: totalShare must be positive", Algorithm)
	}
	d, err := Digest(p)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(d[0:8]) % totalShare, nil
}

// Lookup picks the first choice whose exclusive cumulative end is > point.
// Zero-share buckets are allowed: a 0 end or a repeated end never matches.
func Lookup(point uint64, ends []int, choiceIDs []string) (string, error) {
	if len(ends) == 0 || len(ends) != len(choiceIDs) {
		return "", fmt.Errorf("%s: cumulativeEnds/choiceIds length mismatch", Algorithm)
	}
	for i, end := range ends {
		if end < 0 {
			return "", fmt.Errorf("%s: negative cumulative end", Algorithm)
		}
		if point < uint64(end) {
			return choiceIDs[i], nil
		}
	}
	return "", fmt.Errorf("%s: point %d outside cumulativeEnds", Algorithm, point)
}
