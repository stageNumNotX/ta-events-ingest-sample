// Package producerhandoff locates and checksums the CONTRACT_READY handoff
// package exported by compiler-service#65
// (docs/control-plane/evidence/traffic-country-handoff @ ManifestCommit).
//
// It is test support only. The package bytes are the T2 oracle; nothing here
// reshapes or flattens them. Files are read only at the exact nested paths
// declared in manifest.files, after the manifest blob itself is pinned.
package producerhandoff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ManifestCommit is the CONTRACT_READY commit that published the package.
const ManifestCommit = "297d8d0c1473fa1dd3608ab91ef42575b3a255be"

// ProducerSHA is an alias of ManifestCommit (PR #1 comment 5625013088).
const ProducerSHA = ManifestCommit

// ManifestPath is the producer git path of the outer manifest.
const ManifestPath = "docs/control-plane/evidence/traffic-country-handoff/manifest.json"

// ImplementationSHA is the producer implementing commit (manifest.producerSha).
const ImplementationSHA = "10b0da88732859b62ba6fa625a7467af28f657f9"

// KnownProducerSHAs are full commits a manifest.producerSha / commit field may name.
var KnownProducerSHAs = []string{ProducerSHA, ImplementationSHA}

// EnvDir overrides the vendored location (e.g. a read-only checkout path).
const EnvDir = "TRAFFIC_PRODUCER_HANDOFF_DIR"

// EnvManifestSHA pins the outer manifest.json blob (64 hex chars).
const EnvManifestSHA = "TRAFFIC_PRODUCER_MANIFEST_SHA256"

const (
	ManifestFile        = "manifest.json"
	ManifestSHAFile     = "manifest.json.sha256" // not a trust anchor; producer package has none
	HashVectorsFile     = "hash-vectors.json"
	DecisionVectorsFile = "decision-vectors.json"
)

// ExpectedManifestSHA256 is the independent sha256 of
// compiler-service@297d8d0c1473fa1dd3608ab91ef42575b3a255be
// docs/control-plane/evidence/traffic-country-handoff/manifest.json
// (4145 B; git blob e5ff68ff5f66b6053639cc995682a7263e161da8). Owner
// supplied this pin in PR #1 comment 5626995990. A sibling
// manifest.json.sha256 next to vendored bytes is not a trust anchor.
const ExpectedManifestSHA256 = "7e0adb92f208d399072d75b4ce2ef6ced74c966b991a64eccacd0626b8fb01eb"

// ExpectedManifestBytes is the independently stated size of that blob.
const ExpectedManifestBytes = 4145

var ErrAbsent = errors.New("producer handoff package absent")

// Package is a verified handoff directory.
type Package struct {
	Dir           string
	Manifest      []byte
	ManifestSHA   string
	Root          string
	ProducerField string
	files         map[string]string // nested path → sha256 hex
}

func packageDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Dir(file)
}

func CandidateDirs() []string {
	here := packageDir()
	return []string{
		filepath.Join(here, "testdata", ProducerSHA),
		filepath.Join(here, "..", "producercorpus", "testdata", "handoff"),
		filepath.Join("..", "producerhandoff", "testdata", ProducerSHA),
		filepath.Join("..", "producercorpus", "testdata", "handoff"),
	}
}

func DefaultDir() string {
	if d := os.Getenv(EnvDir); d != "" {
		return d
	}
	cands := CandidateDirs()
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return cands[0]
}

func Open(dir string) (*Package, error) {
	st, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrAbsent, dir)
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("handoff path %s is not a directory", dir)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("handoff %s: %s required: %w", dir, ManifestFile, err)
	}
	sum := sha256.Sum256(manifest)
	gotSHA := hex.EncodeToString(sum[:])
	wantSHA, err := pinnedManifestSHA()
	if err != nil {
		return nil, err
	}
	if wantSHA == "" {
		return nil, fmt.Errorf("handoff %s: manifest blob is not pinned (set %s or ExpectedManifestSHA256 from %s:%s) before trusting %s files; sibling %s is not a trust anchor", dir, EnvManifestSHA, ManifestCommit, ManifestPath, ManifestFile, ManifestSHAFile)
	}
	if !strings.EqualFold(gotSHA, wantSHA) {
		return nil, fmt.Errorf("handoff %s: %s sha256=%s want %s (pinned %s:%s)", dir, ManifestFile, gotSHA, wantSHA, ManifestCommit, ManifestPath)
	}
	if os.Getenv(EnvManifestSHA) == "" && ExpectedManifestBytes > 0 && len(manifest) != ExpectedManifestBytes {
		return nil, fmt.Errorf("handoff %s: %s size=%d want %d (pinned %s:%s)", dir, ManifestFile, len(manifest), ExpectedManifestBytes, ManifestCommit, ManifestPath)
	}
	root, producerField, files, err := parseManifest(manifest)
	if err != nil {
		return nil, fmt.Errorf("handoff %s: %s: %w", dir, ManifestFile, err)
	}
	if !knownProducerField(producerField) {
		return nil, fmt.Errorf("handoff %s: %s producerSha/commit %q is not %s or %s", dir, ManifestFile, producerField, ProducerSHA, ImplementationSHA)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("handoff %s: %s has no files map", dir, ManifestFile)
	}
	return &Package{
		Dir:           dir,
		Manifest:      manifest,
		ManifestSHA:   gotSHA,
		Root:          root,
		ProducerField: producerField,
		files:         files,
	}, nil
}

// Location is the verified handoff directory (for corpus harness errors).
func (p *Package) Location() string { return p.Dir }

func pinnedManifestSHA() (string, error) {
	if s := os.Getenv(EnvManifestSHA); s != "" {
		return normalizeManifestSHA(s)
	}
	if ExpectedManifestSHA256 != "" {
		return normalizeManifestSHA(ExpectedManifestSHA256)
	}
	return "", nil
}

func normalizeManifestSHA(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty manifest blob pin")
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.ToLower(s)
	if len(s) != 64 {
		return "", fmt.Errorf("manifest blob pin must be 64 hex characters, got %d", len(s))
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("manifest blob pin is not hex")
		}
	}
	return s, nil
}

func knownProducerField(field string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "" {
		return false
	}
	for _, sha := range KnownProducerSHAs {
		full := strings.ToLower(sha)
		if field == full || strings.HasPrefix(full, field) || strings.HasPrefix(field, full) {
			if len(field) >= 7 {
				return true
			}
		}
	}
	return false
}

type manifestHead struct {
	Root        string          `json:"root"`
	ProducerSha string          `json:"producerSha"`
	Commit      string          `json:"commit"`
	Producer    string          `json:"producer"`
	Contract    string          `json:"contractReady"`
	Files       json.RawMessage `json:"files"`
}

func parseManifest(raw []byte) (root, producer string, files map[string]string, err error) {
	var h manifestHead
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", "", nil, err
	}
	producer = h.ProducerSha
	if producer == "" {
		producer = h.Commit
	}
	if producer == "" {
		producer = h.Producer
	}
	if producer == "" {
		producer = h.Contract
	}
	files, err = parseFiles(h.Files)
	if err != nil {
		return "", "", nil, err
	}
	return h.Root, producer, files, nil
}

func parseFiles(raw json.RawMessage) (map[string]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("files missing")
	}
	out := map[string]string{}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err == nil {
		for path, v := range asMap {
			sha, err := shaFromValue(v)
			if err != nil {
				return nil, fmt.Errorf("files[%s]: %w", path, err)
			}
			out[filepath.ToSlash(path)] = strings.ToLower(sha)
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	var arr []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("files: want map[path]sha256 or [{path,sha256}]: %w", err)
	}
	for _, e := range arr {
		if e.Path == "" || e.SHA256 == "" {
			return nil, fmt.Errorf("files[] entry missing path/sha256")
		}
		out[filepath.ToSlash(e.Path)] = strings.ToLower(e.SHA256)
	}
	return out, nil
}

func shaFromValue(v json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(v, &s) == nil && s != "" {
		return s, nil
	}
	var obj struct {
		SHA256 string `json:"sha256"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(v, &obj); err != nil {
		return "", err
	}
	if obj.SHA256 != "" {
		return obj.SHA256, nil
	}
	if obj.Hash != "" {
		return obj.Hash, nil
	}
	return "", fmt.Errorf("no sha256")
}

// FilePaths returns nested paths exactly as declared in manifest.files.
func (p *Package) FilePaths() []string {
	out := make([]string, 0, len(p.files))
	for pth := range p.files {
		out = append(out, pth)
	}
	sort.Strings(out)
	return out
}

func (p *Package) lookup(nameOrPath string) (rel, wantSHA string, err error) {
	want := filepath.ToSlash(nameOrPath)
	if sha, ok := p.files[want]; ok {
		return want, sha, nil
	}
	var matches []string
	base := filepath.Base(want)
	for path := range p.files {
		if filepath.Base(path) == base {
			matches = append(matches, path)
		}
	}
	sort.Strings(matches)
	if len(matches) == 1 {
		return matches[0], p.files[matches[0]], nil
	}
	if len(matches) > 1 {
		return "", "", fmt.Errorf("ambiguous %q in manifest.files: %v (use the nested path)", nameOrPath, matches)
	}
	return "", "", fmt.Errorf("%s not declared in manifest.files", nameOrPath)
}

func (p *Package) diskPath(rel string) string {
	slash := filepath.FromSlash(rel)
	exact := filepath.Join(p.Dir, slash)
	if st, err := os.Stat(exact); err == nil && !st.IsDir() {
		return exact
	}
	if p.Root == "" {
		return exact
	}
	root := filepath.ToSlash(p.Root)
	relSlash := filepath.ToSlash(rel)
	if relSlash == root || strings.HasPrefix(relSlash, strings.TrimSuffix(root, "/")+"/") {
		return exact
	}
	rooted := filepath.Join(p.Dir, filepath.FromSlash(p.Root), slash)
	if st, err := os.Stat(rooted); err == nil && !st.IsDir() {
		return rooted
	}
	return exact
}

// ReadVerified reads the file at the nested manifest path (or unique basename)
// and requires its sha256 to equal the files map entry.
func (p *Package) ReadVerified(nameOrPath string) ([]byte, error) {
	rel, wantSHA, err := p.lookup(nameOrPath)
	if err != nil {
		return nil, fmt.Errorf("handoff %s: %w", p.Dir, err)
	}
	raw, err := os.ReadFile(p.diskPath(rel))
	if err != nil {
		return nil, fmt.Errorf("handoff %s: read %s: %w", p.Dir, rel, err)
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if got != strings.ToLower(wantSHA) {
		return nil, fmt.Errorf("handoff %s: %s sha256=%s want %s", p.Dir, rel, got, wantSHA)
	}
	return raw, nil
}
