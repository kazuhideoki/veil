package usecase

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type diffFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
}

type DiffTargets struct {
	FileSystem      diffFileSystem
	DocumentRuntime OnePasswordDocumentRuntime
	Stdout          io.Writer
	TargetPath      string
	Now             func() time.Time
	AllWorkspaces   bool
	Summary         bool
	ColorOutput     bool
}

type diffTargetResult struct {
	WorkspaceID   string
	Target        string
	State         string
	Detail        string
	Expired       bool
	RemoteData    []byte
	WorkspaceData []byte
}

func (u DiffTargets) Run() error {
	_, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return err
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	config = expandConfigPaths(config, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)
	if err := requireOnePasswordConfig(config); err != nil {
		return err
	}
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}
	if u.AllWorkspaces && u.TargetPath != "" {
		return fmt.Errorf("diff accepts either --all or a target path, not both")
	}

	_, state, err := loadState(statusStateFileSystem{statusFileSystem: u.FileSystem})
	if err != nil {
		return err
	}

	workspaces, err := resolveEmergeWorkspaces(u.FileSystem, config, u.AllWorkspaces, "")
	if err != nil {
		return err
	}

	targetPath := ""
	if u.TargetPath != "" {
		targetPath, err = normalizeEditTargetPath(u.TargetPath)
		if err != nil {
			return err
		}
	}

	now := currentTime(u.Now)
	layout := newDiffOutputLayout(u.AllWorkspaces, workspaces)
	found := false
	var diffErr error

	for _, entry := range workspaces {
		targets := entry.workspace.Targets
		explicitTarget := false
		if targetPath != "" {
			if !hasTarget(entry.workspace.Targets, targetPath) {
				return fmt.Errorf("target is not registered: %s", targetPath)
			}
			targets = []string{targetPath}
			explicitTarget = true
		}

		for _, target := range targets {
			result, include, err := u.diffTarget(config, state, entry.id, entry.workspace, target, explicitTarget, now)
			if err != nil {
				wrappedErr := wrapEmergeTargetError(u.AllWorkspaces, entry.id, target, err)
				if u.AllWorkspaces {
					layout.writeFailure(u.Stdout, entry.id, target, wrappedErr)
					diffErr = errors.Join(diffErr, wrappedErr)
					continue
				}
				return wrappedErr
			}
			if !include {
				continue
			}
			found = true
			layout.writeResult(u.Stdout, result)
			if !u.Summary && !bytes.Equal(result.RemoteData, result.WorkspaceData) {
				writeUnifiedDiff(u.Stdout, result.Target, result.RemoteData, result.WorkspaceData, u.ColorOutput)
			}
		}
	}

	if diffErr != nil {
		return diffErr
	}
	if !found {
		fmt.Fprintln(u.Stdout, "no modified targets")
	}
	return nil
}

func (u DiffTargets) diffTarget(config domain.Config, state domain.State, workspaceID string, workspace domain.Workspace, target string, explicit bool, now time.Time) (diffTargetResult, bool, error) {
	result := diffTargetResult{
		WorkspaceID: workspaceID,
		Target:      target,
	}

	document, ok, err := config.DocumentForTarget(workspaceID, target)
	if err != nil {
		return result, false, err
	}
	if !ok {
		if explicit {
			return result, false, fmt.Errorf("1Password document is not registered: %s", target)
		}
		return result, false, nil
	}

	workspaceTargetPath := filepath.Join(workspace.Root, target)
	lease, ok, err := state.FindLease(workspaceID, target)
	if err != nil {
		return result, false, err
	}
	if !ok {
		if explicit {
			return result, false, fmt.Errorf("target is not emerged: %s", target)
		}
		return result, false, nil
	}
	if lease.StoreID != onePasswordStoreID {
		if explicit {
			return result, false, fmt.Errorf("target is not emerged from 1Password document store: %s", target)
		}
		return result, false, nil
	}
	if lease.StorePath != "" && lease.StorePath != document.ItemID {
		if explicit {
			return result, false, fmt.Errorf("target lease does not match registered 1Password document: %s", target)
		}
		return result, false, nil
	}
	if lease.PlaintextHash == "" {
		if explicit {
			return result, false, fmt.Errorf("target has no recorded plaintext hash; re-run veil emerge before diff: %s", target)
		}
		return result, false, nil
	}
	if lease.WorkspacePath != "" && filepath.Clean(lease.WorkspacePath) != filepath.Clean(workspaceTargetPath) {
		if explicit {
			return result, false, fmt.Errorf("target workspace path does not match active lease: %s", target)
		}
		return result, false, nil
	}

	workspaceData, err := readRegularWorkspaceTarget(u.FileSystem, workspaceTargetPath, target)
	if err != nil {
		if explicit {
			return result, false, err
		}
		return result, false, nil
	}
	workspaceHash := sha256Hex(workspaceData)
	localModified := workspaceHash != lease.PlaintextHash
	if !explicit && !localModified {
		return result, false, nil
	}

	vault := onePasswordVault(config, document)
	remoteData, err := u.DocumentRuntime.ReadDocument(vault, document.ItemID)
	if err != nil {
		return result, false, fmt.Errorf("read 1Password document: %w", err)
	}

	remoteHash := sha256Hex(remoteData)
	remoteChanged := remoteHash != lease.PlaintextHash

	result.RemoteData = remoteData
	result.WorkspaceData = workspaceData
	result.Expired = !lease.ExpiresAt.After(now)

	switch {
	case workspaceHash == remoteHash:
		result.State = "already up to date"
	case localModified && remoteChanged:
		result.State = "conflict"
		result.Detail = "1Password document changed since last Veil sync"
	case localModified:
		result.State = "modified"
	case remoteChanged:
		result.State = "remote changed"
	default:
		result.State = "unchanged"
	}

	if result.Expired {
		if result.Detail == "" {
			result.Detail = "target lease is expired; re-run veil emerge before commit"
		} else {
			result.Detail += "; target lease is expired; re-run veil emerge before commit"
		}
	}

	return result, true, nil
}

func readRegularWorkspaceTarget(fs diffFileSystem, path, target string) ([]byte, error) {
	info, err := fs.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("workspace target does not exist: %s", target)
		}
		return nil, fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace target must be a Veil materialized regular file: %s", target)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace target must be a regular file: %s", target)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workspace target: %w", err)
	}
	return data, nil
}

type diffOutputLayout struct {
	allWorkspaces  bool
	actionWidth    int
	workspaceWidth int
}

func newDiffOutputLayout(allWorkspaces bool, workspaces []emergeWorkspace) diffOutputLayout {
	layout := diffOutputLayout{
		allWorkspaces: allWorkspaces,
		actionWidth:   len("already up to date"),
	}
	if !allWorkspaces {
		return layout
	}
	for _, entry := range workspaces {
		if len(entry.id) > layout.workspaceWidth {
			layout.workspaceWidth = len(entry.id)
		}
	}
	return layout
}

func (l diffOutputLayout) writeResult(w io.Writer, result diffTargetResult) {
	detail := ""
	if result.Detail != "" {
		detail = "  " + result.Detail
	}
	if !l.allWorkspaces {
		fmt.Fprintf(w, "%s target: %s%s\n", result.State, result.Target, detail)
		return
	}
	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s%s\n", l.actionWidth, result.State, l.workspaceWidth, result.WorkspaceID, result.Target, detail)
}

func (l diffOutputLayout) writeFailure(w io.Writer, workspaceID, target string, err error) {
	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s  error: %v\n", l.actionWidth, "failed", l.workspaceWidth, workspaceID, target, err)
}

func writeUnifiedDiff(w io.Writer, target string, remoteData, workspaceData []byte, colorOutput bool) {
	remoteLines := splitDiffLines(string(remoteData))
	workspaceLines := splitDiffLines(string(workspaceData))
	ops := unifiedDiffOps(remoteLines, workspaceLines)
	hunks := unifiedDiffHunks(ops, 2)

	fmt.Fprintf(w, "diff --veil %s\n", target)
	fmt.Fprintf(w, "--- 1password/%s\n", target)
	fmt.Fprintf(w, "+++ workspace/%s\n", target)
	for _, hunk := range hunks {
		fmt.Fprintf(w, "@@ -%d,%d +%d,%d @@\n", hunk.oldStart, hunk.oldCount, hunk.newStart, hunk.newCount)
		for _, op := range hunk.ops {
			writeDiffLine(w, op.prefix, op.line, colorOutput)
		}
	}
}

type diffOp struct {
	prefix byte
	line   string
}

type diffHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	ops      []diffOp
}

func unifiedDiffOps(a, b []string) []diffOp {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ops := make([]diffOp, 0, len(a)+len(b))
	for i, j := 0, 0; i < len(a) || j < len(b); {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			ops = append(ops, diffOp{prefix: ' ', line: a[i]})
			i++
			j++
		case j >= len(b) || (i < len(a) && dp[i+1][j] >= dp[i][j+1]):
			ops = append(ops, diffOp{prefix: '-', line: a[i]})
			i++
		default:
			ops = append(ops, diffOp{prefix: '+', line: b[j]})
			j++
		}
	}
	return ops
}

func unifiedDiffHunks(ops []diffOp, contextLines int) []diffHunk {
	if contextLines < 0 {
		contextLines = 0
	}

	type opRange struct {
		start int
		end   int
	}
	ranges := []opRange{}
	for idx, op := range ops {
		if op.prefix == ' ' {
			continue
		}
		start := idx - contextLines
		if start < 0 {
			start = 0
		}
		end := idx + contextLines + 1
		if end > len(ops) {
			end = len(ops)
		}
		if len(ranges) == 0 || start > ranges[len(ranges)-1].end {
			ranges = append(ranges, opRange{start: start, end: end})
			continue
		}
		if end > ranges[len(ranges)-1].end {
			ranges[len(ranges)-1].end = end
		}
	}

	hunks := make([]diffHunk, 0, len(ranges))
	for _, r := range ranges {
		oldStart, newStart := diffLineNumbersAt(ops, r.start)
		hunk := diffHunk{
			oldStart: oldStart,
			newStart: newStart,
			ops:      append([]diffOp(nil), ops[r.start:r.end]...),
		}
		for _, op := range hunk.ops {
			if op.prefix != '+' {
				hunk.oldCount++
			}
			if op.prefix != '-' {
				hunk.newCount++
			}
		}
		hunks = append(hunks, hunk)
	}
	return hunks
}

func diffLineNumbersAt(ops []diffOp, opIndex int) (int, int) {
	oldLine := 1
	newLine := 1
	for _, op := range ops[:opIndex] {
		if op.prefix != '+' {
			oldLine++
		}
		if op.prefix != '-' {
			newLine++
		}
	}
	return oldLine, newLine
}

func splitDiffLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := strings.SplitAfter(value, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeDiffLine(w io.Writer, prefix byte, line string, colorOutput bool) {
	color := ""
	if colorOutput {
		switch prefix {
		case '-':
			color = "\x1b[31m"
		case '+':
			color = "\x1b[32m"
		}
	}
	if color != "" {
		line = strings.TrimSuffix(line, "\n")
		fmt.Fprintf(w, "%s%c%s\x1b[0m\n", color, prefix, line)
		return
	}
	fmt.Fprintf(w, "%c%s", prefix, line)
	if !strings.HasSuffix(line, "\n") {
		fmt.Fprintln(w)
	}
}
