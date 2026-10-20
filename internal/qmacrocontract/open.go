package qmacrocontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrAbsent = errors.New("q-macro-contract package absent")

// Package is a verified producer Q-MACRO evidence directory.
type Package struct {
	Dir        string
	Digest     string
	OracleFile string // decision-vectors.json (or legacy file whose sha256 is OracleDigest)
	files      map[string]string
	paths      []string
	fileDigest bool // true when OracleDigest is the sha256 of OracleFile bytes
}

func (p *Package) Location() string { return p.Dir }

func Open(dir string) (*Package, error) {
	st, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrAbsent, dir)
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("q-macro path %s is not a directory", dir)
	}

	digestRaw, digestPath, err := readDigestFile(dir)
	if err != nil {
		return nil, fmt.Errorf("q-macro %s: %w", dir, err)
	}
	got, filesFromDigest, err := parseDigest(digestRaw)
	if err != nil {
		return nil, fmt.Errorf("q-macro %s: %s: %w", dir, digestPath, err)
	}
	if !strings.EqualFold(got, OracleDigest) {
		return nil, fmt.Errorf("q-macro %s: %s oracleDigest=%s want compiled pin %s (producer %s %s)", dir, digestPath, got, OracleDigest, ProducerRepo, ProducerCommitPrefix)
	}

	paths, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("q-macro %s: empty package", dir)
	}

	files, err := collectDeclaredFiles(dir, paths, filesFromDigest)
	if err != nil {
		return nil, fmt.Errorf("q-macro %s: %w", dir, err)
	}

	oracleFile, fileDigest, err := pinOracle(dir, paths)
	if err != nil {
		return nil, fmt.Errorf("q-macro %s: %w", dir, err)
	}

	pkg := &Package{
		Dir:        dir,
		Digest:     OracleDigest,
		OracleFile: oracleFile,
		files:      files,
		paths:      paths,
		fileDigest: fileDigest,
	}
	if err := pkg.verifyDeclaredFiles(); err != nil {
		return nil, err
	}
	return pkg, nil
}

func readDigestFile(dir string) ([]byte, string, error) {
	for _, name := range []string{DigestFile, "DIGEST.txt", "oracleDigest", "ORACLE_DIGEST"} {
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p)
		if err == nil {
			return b, name, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, name, err
		}
	}
	return nil, DigestFile, fmt.Errorf("%s required (producer %s)", DigestFile, EvidencePath)
}

func parseDigest(raw []byte) (digest string, files map[string]string, err error) {
	files = map[string]string{}
	lines := strings.Split(string(raw), "\n")
	var found []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "oracledigest=") {
			line = strings.TrimSpace(line[len("oracleDigest="):])
			if i := strings.IndexByte(line, ' '); i >= 0 {
				line = line[:i]
			}
		} else if strings.HasPrefix(lower, "oracle_digest=") {
			line = strings.TrimSpace(line[len("oracle_digest="):])
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if hex64(fields[0]) {
			found = append(found, strings.ToLower(fields[0]))
			if len(fields) >= 2 && !hex64(fields[1]) {
				files[filepath.ToSlash(fields[1])] = strings.ToLower(fields[0])
			}
			continue
		}
		if len(fields) >= 2 && hex64(fields[1]) {
			files[filepath.ToSlash(fields[0])] = strings.ToLower(fields[1])
			found = append(found, strings.ToLower(fields[1]))
		}
	}
	if len(found) == 0 {
		return "", nil, fmt.Errorf("no 64-hex oracleDigest")
	}
	for _, h := range found {
		if h == OracleDigest {
			return OracleDigest, files, nil
		}
	}
	return found[0], files, nil
}

func hex64(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func listFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func pinOracle(dir string, paths []string) (oracleFile string, fileDigest bool, err error) {
	if rel, ok := findBindingVectors(paths); ok {
		corpus := filepath.ToSlash(filepath.Dir(rel))
		hashes := map[string]string{}
		for _, name := range BindingOracleFiles {
			p := joinCorpus(corpus, name)
			raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
			if err != nil {
				return "", false, fmt.Errorf("oracle file %s: %w (binding digest %s)", p, err, OracleDigest)
			}
			sum := sha256.Sum256(raw)
			hashes[name] = hex.EncodeToString(sum[:])
		}
		got, err := bindingDigest(hashes)
		if err != nil {
			return "", false, err
		}
		if got != OracleDigest {
			return "", false, fmt.Errorf("binding oracleDigest=%s want compiled pin %s (producer %s %s)", got, OracleDigest, ProducerRepo, ProducerCommitPrefix)
		}
		return rel, false, nil
	}
	rel, err := findOracleFile(dir, paths, nil)
	if err != nil {
		return "", false, err
	}
	return rel, true, nil
}

func findBindingVectors(paths []string) (string, bool) {
	var hits []string
	for _, p := range paths {
		if filepath.Base(p) == DecisionVectorsFile {
			hits = append(hits, p)
		}
	}
	if len(hits) != 1 {
		return "", false
	}
	corpus := filepath.ToSlash(filepath.Dir(hits[0]))
	for _, name := range BindingOracleFiles {
		want := joinCorpus(corpus, name)
		found := false
		for _, p := range paths {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			return "", false
		}
	}
	return hits[0], true
}

func joinCorpus(corpus, name string) string {
	name = filepath.ToSlash(name)
	if corpus == "." || corpus == "" {
		return name
	}
	return corpus + "/" + name
}

func bindingDigest(fileSHA map[string]string) (string, error) {
	raw, err := json.Marshal(fileSHA)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func collectDeclaredFiles(dir string, paths []string, fromDigest map[string]string) (map[string]string, error) {
	declared := map[string]string{}
	for k, v := range fromDigest {
		declared[k] = v
	}
	if man, err := os.ReadFile(filepath.Join(dir, ManifestFile)); err == nil {
		parsed, err := parseManifestFiles(man)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ManifestFile, err)
		}
		if len(parsed) > 0 {
			declared = parsed
		}
	}
	for _, rel := range paths {
		base := filepath.Base(rel)
		if base != InnerManifestFile && !(base == ManifestFile && rel != ManifestFile) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		parsed, err := parseManifestFiles(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		corpus := filepath.ToSlash(filepath.Dir(rel))
		for k, sha := range parsed {
			full := joinCorpus(corpus, k)
			if _, ok := declared[full]; !ok {
				declared[full] = sha
			}
			if _, ok := declared[k]; !ok {
				declared[k] = sha
			}
		}
	}

	resolved := map[string]string{}
	for rel, sha := range declared {
		actual, ok := resolveDeclared(paths, rel)
		if !ok {
			if isPackageLabel(rel) {
				continue
			}
			return nil, fmt.Errorf("declared %s not in package", rel)
		}
		resolved[actual] = strings.ToLower(sha)
	}
	return resolved, nil
}

func isPackageLabel(rel string) bool {
	if rel == "" || strings.Contains(rel, "/") {
		return false
	}
	return !strings.Contains(rel, ".")
}

func resolveDeclared(paths []string, declared string) (string, bool) {
	declared = filepath.ToSlash(declared)
	for _, p := range paths {
		if p == declared {
			return p, true
		}
	}
	var hits []string
	for _, p := range paths {
		if strings.HasSuffix(p, "/"+declared) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return "", false
}

func findOracleFile(dir string, paths []string, declared map[string]string) (string, error) {
	var matches []string
	want := strings.ToLower(OracleDigest)
	for _, rel := range paths {
		base := filepath.Base(rel)
		if base == DigestFile || base == "DIGEST.txt" || base == ReadmeFile || base == "oracleDigest" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got == want {
			matches = append(matches, rel)
			continue
		}
		if len(raw) > 0 && raw[len(raw)-1] == '\n' {
			s := sha256.Sum256(raw[:len(raw)-1])
			if hex.EncodeToString(s[:]) == want {
				matches = append(matches, rel)
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("several files hash to oracleDigest %s: %v", OracleDigest, matches)
	}
	for rel, sha := range declared {
		if strings.EqualFold(sha, OracleDigest) {
			return rel, nil
		}
	}
	return "", fmt.Errorf("no corpus file hashes to compiled oracleDigest %s (producer is the sole oracle; local fixtures do not close Q-MACRO-CONTRACT)", OracleDigest)
}

func (p *Package) verifyDeclaredFiles() error {
	for rel, want := range p.files {
		raw, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("q-macro %s: declared %s: %w", p.Dir, rel, err)
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got != strings.ToLower(want) {
			return fmt.Errorf("q-macro %s: %s sha256=%s want %s", p.Dir, rel, got, want)
		}
	}
	return nil
}

func (p *Package) FilePaths() []string {
	out := append([]string(nil), p.paths...)
	sort.Strings(out)
	return out
}

func (p *Package) lookup(nameOrPath string) (string, error) {
	want := filepath.ToSlash(nameOrPath)
	for _, pth := range p.paths {
		if pth == want {
			return pth, nil
		}
	}
	var matches []string
	base := filepath.Base(want)
	for _, pth := range p.paths {
		if filepath.Base(pth) == base {
			matches = append(matches, pth)
		}
	}
	sort.Strings(matches)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous %q in q-macro package: %v", nameOrPath, matches)
	}
	return "", fmt.Errorf("%s not in q-macro package", nameOrPath)
}

func (p *Package) ReadVerified(nameOrPath string) ([]byte, error) {
	rel, err := p.lookup(nameOrPath)
	if err != nil {
		return nil, fmt.Errorf("q-macro %s: %w", p.Dir, err)
	}
	raw, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("q-macro %s: read %s: %w", p.Dir, rel, err)
	}
	if want, ok := p.files[rel]; ok {
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got != strings.ToLower(want) {
			return nil, fmt.Errorf("q-macro %s: %s sha256=%s want %s", p.Dir, rel, got, want)
		}
	}
	if rel == p.OracleFile && p.fileDigest {
		if err := verifyOracleBytes(raw); err != nil {
			return nil, fmt.Errorf("q-macro %s: %s: %w", p.Dir, rel, err)
		}
	}
	return raw, nil
}

func verifyOracleBytes(raw []byte) error {
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if got == OracleDigest {
		return nil
	}
	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		s := sha256.Sum256(raw[:len(raw)-1])
		if hex.EncodeToString(s[:]) == OracleDigest {
			return nil
		}
	}
	return fmt.Errorf("sha256=%s want %s", got, OracleDigest)
}

func parseManifestFiles(raw []byte) (map[string]string, error) {
	var h struct {
		Files json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(h.Files)) == 0 || string(h.Files) == "null" {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(h.Files, &asMap); err == nil {
		for path, v := range asMap {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				out[filepath.ToSlash(path)] = strings.ToLower(s)
				continue
			}
			var obj struct {
				SHA256 string `json:"sha256"`
			}
			if json.Unmarshal(v, &obj) == nil && obj.SHA256 != "" {
				out[filepath.ToSlash(path)] = strings.ToLower(obj.SHA256)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("files: want map[path]sha256")
}

// CorpusNames are producer vector files this consumer knows how to execute.
func CorpusNames() []string {
	return []string{DecisionVectorsFile, MatcherVectorsFile, VectorsFile}
}
