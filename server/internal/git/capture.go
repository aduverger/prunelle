package git

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	DefaultMaxFileBytes = 1 << 20
	captureAttempts     = 3
	binarySniffBytes    = 8000

	modeSymlink = "120000"
	modeGitlink = "160000"
)

type FileStatus string

const (
	StatusAdded       FileStatus = "ADDED"
	StatusModified    FileStatus = "MODIFIED"
	StatusDeleted     FileStatus = "DELETED"
	StatusRenamed     FileStatus = "RENAMED"
	StatusTypeChanged FileStatus = "TYPE_CHANGED"
	StatusUntracked   FileStatus = "UNTRACKED"
)

// Reasons a changed file is listed without content.
const (
	OmittedBinary    = "BINARY"
	OmittedTooLarge  = "TOO_LARGE"
	OmittedSubmodule = "SUBMODULE"
	OmittedSpecial   = "NOT_A_REGULAR_FILE"
)

type Options struct {
	BaseRef          string
	IncludeUntracked bool
	MaxFileBytes     int64

	// afterRead runs between reading contents and verifying them; tests use it to mutate the work tree.
	afterRead func(attempt int)
}

// Snapshot is the captured state of one repository: base..work tree.
type Snapshot struct {
	BaseRef        string
	BaseSHA        string
	HeadSHA        string
	Branch         string
	StagedCount    int
	UnstagedCount  int
	UntrackedCount int
	Files          []File
	Warnings       []string
	Consistent     bool
	Fingerprint    string
}

type File struct {
	Path     string
	OldPath  string
	Status   FileStatus
	BaseMode string
	HeadMode string
	BaseOID  string
	HeadOID  string
	Base     *Content
	Head     *Content
	Binary   bool
	Omitted  string
}

// Content is file text addressed by the SHA-256 of its bytes.
type Content struct {
	Hash string
	Data []byte
}

func NewContent(data []byte) *Content {
	sum := sha256.Sum256(data)
	return &Content{Hash: hex.EncodeToString(sum[:]), Data: data}
}

// Size returns the total bytes of stored file contents.
func (s Snapshot) Size() int64 {
	var total int64
	for _, f := range s.Files {
		if f.Base != nil {
			total += int64(len(f.Base.Data))
		}
		if f.Head != nil {
			total += int64(len(f.Head.Data))
		}
	}
	return total
}

// Capture records the difference between merge-base(HEAD, BaseRef) and the work tree of root,
// including staged, unstaged and (optionally) untracked files. root must come from ResolveRoot.
// If the repository changes while it is read, capture retries and finally returns a snapshot
// marked inconsistent.
func Capture(ctx context.Context, root string, opts Options) (Snapshot, error) {
	if opts.MaxFileBytes == 0 {
		opts.MaxFileBytes = DefaultMaxFileBytes
	}
	if err := ValidateRef(opts.BaseRef); err != nil {
		return Snapshot{}, err
	}
	format, err := runString(ctx, root, "rev-parse", "--show-object-format")
	if err != nil {
		return Snapshot{}, err
	}
	newHash, err := objectHasher(format)
	if err != nil {
		return Snapshot{}, err
	}

	var snapshot Snapshot
	for attempt := 1; attempt <= captureAttempts; attempt++ {
		snapshot, err = captureOnce(ctx, root, opts, newHash)
		if err != nil {
			return Snapshot{}, err
		}
		if opts.afterRead != nil {
			opts.afterRead(attempt)
		}
		snapshot.Consistent, err = verify(ctx, root, opts, newHash, snapshot)
		if err != nil {
			return Snapshot{}, err
		}
		if snapshot.Consistent {
			return snapshot, nil
		}
	}
	snapshot.Warnings = append(snapshot.Warnings,
		fmt.Sprintf("repository changed during capture in each of %d attempts; snapshot may mix states", captureAttempts))
	return snapshot, nil
}

// ValidateRef rejects refs that git could parse as options.
func ValidateRef(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\n") {
		return fmt.Errorf("invalid base ref %q", ref)
	}
	return nil
}

func captureOnce(ctx context.Context, root string, opts Options, newHash func() hash.Hash) (Snapshot, error) {
	l, err := list(ctx, root, opts)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		BaseRef:        opts.BaseRef,
		BaseSHA:        l.baseSHA,
		HeadSHA:        l.headSHA,
		Branch:         l.branch,
		StagedCount:    l.staged,
		UnstagedCount:  l.unstaged,
		UntrackedCount: l.untracked,
		Warnings:       l.warnings,
	}
	baseContents, err := readBaseContents(ctx, root, l.entries, opts.MaxFileBytes)
	if err != nil {
		return Snapshot{}, err
	}
	for _, e := range l.entries {
		f := File{Path: e.path, OldPath: e.oldPath, Status: e.status, BaseMode: e.baseMode, BaseOID: e.baseOID}
		if base, ok := baseContents[e.baseOID]; ok {
			f.Omitted = base.omitted
			f.Base = base.content
		} else if e.baseMode == modeGitlink {
			f.Omitted = OmittedSubmodule
		}
		if e.status != StatusDeleted {
			head, err := readWorktreeFile(root, e.path, opts.MaxFileBytes, newHash)
			if err != nil {
				return Snapshot{}, err
			}
			f.HeadMode, f.HeadOID = head.mode, head.oid
			f.Head = head.content
			if f.Omitted == "" {
				f.Omitted = head.omitted
			}
		}
		if f.Omitted != "" {
			// A diff with one side missing would mislead; keep the file listed without contents.
			f.Base, f.Head = nil, nil
			f.Binary = f.Omitted == OmittedBinary
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf("%s: content not stored (%s)", f.Path, f.Omitted))
		}
		snapshot.Files = append(snapshot.Files, f)
	}
	snapshot.Fingerprint = fingerprint(snapshot.BaseSHA, snapshot.HeadSHA, snapshot.Files)
	return snapshot, nil
}

// verify re-lists the repository and re-hashes work tree files, reporting whether they still
// match the snapshot.
func verify(ctx context.Context, root string, opts Options, newHash func() hash.Hash, s Snapshot) (bool, error) {
	l, err := list(ctx, root, opts)
	if err != nil {
		return false, err
	}
	if l.headSHA != s.HeadSHA || l.baseSHA != s.BaseSHA || len(l.entries) != len(s.Files) {
		return false, nil
	}
	for i, e := range l.entries {
		f := s.Files[i]
		if e.path != f.Path || e.status != f.Status || e.baseOID != f.BaseOID {
			return false, nil
		}
		if e.status == StatusDeleted {
			continue
		}
		head, err := readWorktreeFile(root, e.path, 0, newHash)
		if err != nil {
			return false, err
		}
		if head.oid != f.HeadOID || head.mode != f.HeadMode {
			return false, nil
		}
	}
	return true, nil
}

func fingerprint(baseSHA, headSHA string, files []File) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", baseSHA, headSHA)
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00", f.Path, f.OldPath, f.Status, f.BaseOID, f.HeadOID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type listing struct {
	headSHA, branch, baseSHA string
	staged, unstaged         int
	untracked                int
	entries                  []entry
	warnings                 []string
}

type entry struct {
	path, oldPath     string
	status            FileStatus
	baseMode, baseOID string
}

func list(ctx context.Context, root string, opts Options) (listing, error) {
	var l listing
	status, err := run(ctx, root, nil, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		return l, err
	}
	if err := parseStatus(status, &l); err != nil {
		return l, err
	}
	baseCommit, err := runString(ctx, root, "rev-parse", "--verify", "--end-of-options", opts.BaseRef+"^{commit}")
	if err != nil {
		return l, fmt.Errorf("base ref %q: %w", opts.BaseRef, err)
	}
	if l.baseSHA, err = runString(ctx, root, "merge-base", "HEAD", baseCommit); err != nil {
		return l, fmt.Errorf("no merge base between HEAD and %q: %w", opts.BaseRef, err)
	}
	raw, err := run(ctx, root, nil, "diff", "--raw", "-z", "-M", "--no-abbrev", "--no-ext-diff", "--no-textconv", l.baseSHA, "--")
	if err != nil {
		return l, err
	}
	if l.entries, err = parseRawDiff(raw); err != nil {
		return l, err
	}
	if opts.IncludeUntracked {
		out, err := run(ctx, root, nil, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return l, err
		}
		for _, path := range splitNUL(out) {
			if strings.HasSuffix(path, "/") {
				l.warnings = append(l.warnings, fmt.Sprintf("%s: nested repository not captured", path))
				continue
			}
			l.entries = append(l.entries, entry{path: path, status: StatusUntracked})
		}
	} else if l.untracked > 0 {
		l.warnings = append(l.warnings, fmt.Sprintf("%d untracked files not included", l.untracked))
	}
	slices.SortFunc(l.entries, func(a, b entry) int { return strings.Compare(a.path, b.path) })
	return l, nil
}

func parseStatus(out []byte, l *listing) error {
	fields := splitNUL(out)
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			l.headSHA = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.head "):
			if head := strings.TrimPrefix(line, "# branch.head "); head != "(detached)" {
				l.branch = head
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "):
			if len(line) < 4 {
				return fmt.Errorf("unexpected status line %q", line)
			}
			if line[2] != '.' {
				l.staged++
			}
			if line[3] != '.' {
				l.unstaged++
			}
			if line[0] == '2' {
				i++ // rename records carry the original path as an extra field
			}
		case strings.HasPrefix(line, "? "):
			l.untracked++
		}
	}
	if l.headSHA == "" || l.headSHA == "(initial)" {
		return errors.New("repository has no commits")
	}
	return nil
}

func parseRawDiff(out []byte) ([]entry, error) {
	fields := splitNUL(out)
	var entries []entry
	for i := 0; i < len(fields); i++ {
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) != 5 || i+1 >= len(fields) {
			return nil, fmt.Errorf("unexpected raw diff record %q", fields[i])
		}
		e := entry{baseMode: meta[0], baseOID: meta[2], path: fields[i+1]}
		i++
		switch meta[4][0] {
		case 'A':
			e.status = StatusAdded
		case 'D':
			e.status = StatusDeleted
		case 'M', 'U':
			e.status = StatusModified
		case 'T':
			e.status = StatusTypeChanged
		case 'R':
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("truncated rename record %q", fields[i-1])
			}
			e.status, e.oldPath, e.path = StatusRenamed, e.path, fields[i+1]
			i++
		default:
			return nil, fmt.Errorf("unsupported diff status %q", meta[4])
		}
		if e.status == StatusAdded {
			e.baseMode, e.baseOID = "", ""
		}
		entries = append(entries, e)
	}
	return entries, nil
}

type readContent struct {
	content *Content
	omitted string
}

// readBaseContents loads base blobs for entries, skipping submodules and oversized blobs.
func readBaseContents(ctx context.Context, root string, entries []entry, maxBytes int64) (map[string]readContent, error) {
	contents := map[string]readContent{}
	var oids []string
	for _, e := range entries {
		if e.baseOID != "" && e.baseMode != modeGitlink {
			if _, seen := contents[e.baseOID]; !seen {
				contents[e.baseOID] = readContent{}
				oids = append(oids, e.baseOID)
			}
		}
	}
	if len(oids) == 0 {
		return contents, nil
	}
	sizes, err := run(ctx, root, []byte(strings.Join(oids, "\n")+"\n"), "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	var fetch []string
	for _, line := range strings.Split(strings.TrimSpace(string(sizes)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected cat-file output %q", line)
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, err
		}
		if size > maxBytes {
			contents[parts[0]] = readContent{omitted: OmittedTooLarge}
			continue
		}
		fetch = append(fetch, parts[0])
	}
	if len(fetch) == 0 {
		return contents, nil
	}
	out, err := run(ctx, root, []byte(strings.Join(fetch, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(bytes.NewReader(out))
	for range fetch {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read cat-file header: %w", err)
		}
		parts := strings.Fields(header)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected cat-file header %q", header)
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, err
		}
		data := make([]byte, size+1) // content plus trailing newline
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("read blob %s: %w", parts[0], err)
		}
		contents[parts[0]] = textContent(data[:size])
	}
	return contents, nil
}

func textContent(data []byte) readContent {
	if isBinary(data) {
		return readContent{omitted: OmittedBinary}
	}
	return readContent{content: NewContent(data)}
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0
}

type worktreeFile struct {
	mode, oid string
	readContent
}

// readWorktreeFile reads path below root without following symlinks out of the repository.
// maxBytes of 0 hashes the file without keeping its content.
func readWorktreeFile(root, path string, maxBytes int64, newHash func() hash.Hash) (worktreeFile, error) {
	abs := filepath.Join(root, filepath.FromSlash(path))
	if !within(abs, root) {
		return worktreeFile{}, fmt.Errorf("%s: path escapes repository", path)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return worktreeFile{}, err
	}
	if !within(parent, root) {
		return worktreeFile{}, fmt.Errorf("%s: parent directory resolves outside repository", path)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return worktreeFile{}, err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		if err != nil {
			return worktreeFile{}, err
		}
		data := []byte(target)
		f := worktreeFile{mode: modeSymlink, oid: objectID(newHash, data)}
		if maxBytes > 0 {
			f.content = NewContent(data)
		}
		return f, nil
	case info.IsDir():
		return worktreeFile{mode: modeGitlink, readContent: readContent{omitted: OmittedSubmodule}}, nil
	case !info.Mode().IsRegular():
		return worktreeFile{readContent: readContent{omitted: OmittedSpecial}}, nil
	}

	mode := "100644"
	if info.Mode().Perm()&0o111 != 0 {
		mode = "100755"
	}
	if maxBytes == 0 || info.Size() > maxBytes {
		oid, err := hashFile(abs, info.Size(), newHash)
		if err != nil {
			return worktreeFile{}, err
		}
		f := worktreeFile{mode: mode, oid: oid}
		if maxBytes > 0 {
			f.omitted = OmittedTooLarge
		}
		return f, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return worktreeFile{}, err
	}
	return worktreeFile{mode: mode, oid: objectID(newHash, data), readContent: textContent(data)}, nil
}

func objectHasher(format string) (func() hash.Hash, error) {
	switch format {
	case "sha1":
		return sha1.New, nil
	case "sha256":
		return sha256.New, nil
	}
	return nil, fmt.Errorf("unsupported object format %q", format)
}

// objectID computes the git blob ID of data.
func objectID(newHash func() hash.Hash, data []byte) string {
	h := newHash()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(path string, size int64, newHash func() hash.Hash) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := newHash()
	fmt.Fprintf(h, "blob %d\x00", size)
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
